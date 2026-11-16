// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useMemo, useState} from 'react';

import {getChannel, getPost, getProfilesByIds, getTeamsByIds} from '@/client';
import {isValidId} from '@/utils/ids';

import {JSONValue} from './tool_types';

export type IdKind = 'user' | 'channel' | 'team' | 'post';

const POST_SNIPPET_LENGTH = 80;

// Cards from bulk tools can list many IDs; past this many per kind the rest
// simply keep showing the raw ID, which stays the primary value anyway.
const MAX_LOOKUPS_PER_KIND = 20;

const kindPatterns: Array<[IdKind, RegExp]> = [
    ['user', /(^|_)user_ids?$/],
    ['channel', /(^|_)channel_ids?$/],
    ['team', /(^|_)team_ids?$/],
    ['post', /(^|_)(post|root|thread|parent)_ids?$/],
];

// Hints are secondary text shown next to the raw ID, which always stays the
// primary value: user-authored names and post text must never stand in for the
// identifier the server will act on. Cached per session.
const hints = new Map<string, string>();
const inflight = new Map<string, Promise<void>>();
const teamLookups = new Map<string, Promise<string>>();

function refKey(kind: IdKind, id: string) {
    return `${kind}:${id}`;
}

export function idKindForKey(key: string): IdKind | null {
    const match = kindPatterns.find(([, pattern]) => pattern.test(key));
    return match ? match[0] : null;
}

function idsOf(value: JSONValue): string[] {
    if (typeof value === 'string') {
        return isValidId(value) ? [value] : [];
    }
    if (Array.isArray(value)) {
        return value.filter(isValidId);
    }
    return [];
}

// Mirrors llm.SanitizeNonPrintableChars, and folds all whitespace (including
// newlines) so a display name can't fake a second line.
const unsafeChars = /[\p{C}\p{Z}\p{Variation_Selector}\p{Default_Ignorable_Code_Point}]/gu;

function escapeUnsafe(text: string) {
    return text.replace(unsafeChars, (ch) => {
        if ((/\s/).test(ch)) {
            return ' ';
        }
        return `[U+${ch.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}]`;
    }).replace(/ {2,}/g, ' ').trim();
}

function clip(text: string) {
    return text.length > POST_SNIPPET_LENGTH ? `${text.slice(0, POST_SNIPPET_LENGTH)}…` : text;
}

// One lookup per team ID, shared by every channel in that team. Falls back to
// the team ID when the team can't be read; a failed lookup isn't remembered.
function teamHandle(teamId: string): Promise<string> {
    let lookup = teamLookups.get(teamId);
    if (!lookup) {
        lookup = getTeamsByIds([teamId]).then(([team]) => (team?.id === teamId ? team.name : teamId)).catch(() => {
            teamLookups.delete(teamId);
            return teamId;
        });
        teamLookups.set(teamId, lookup);
    }
    return lookup;
}

// Responses for any other ID than the one requested are ignored.
async function fetchHint(kind: Exclude<IdKind, 'user'>, id: string): Promise<string> {
    switch (kind) {
    case 'team': {
        const [team] = await getTeamsByIds([id]);
        return team?.id === id ? `${team.display_name} · ${team.name}` : '';
    }
    case 'channel': {
        const channel = await getChannel(id);
        if (channel.id !== id) {
            return '';
        }
        const team = channel.team_id && isValidId(channel.team_id) ? await teamHandle(channel.team_id) : '';
        return [channel.display_name, `~${channel.name}`, team].filter(Boolean).join(' · ');
    }
    default: {
        const post = await getPost(id);
        return post.id === id ? clip(post.message.replace(/\s+/g, ' ').trim()) : '';
    }
    }
}

// Each ID settles on its own, so one missing team, channel or post never
// discards the hints that did resolve.
async function fetchHints(kind: IdKind, ids: string[]): Promise<Array<[string, string]>> {
    if (kind === 'user') {
        const profiles = await getProfilesByIds(ids);
        return profiles.filter((profile) => ids.includes(profile.id)).map((profile) => [profile.id, `@${profile.username}`]);
    }
    const settled = await Promise.allSettled(ids.map((id) => fetchHint(kind, id)));
    return settled.flatMap((result, i) => (
        result.status === 'fulfilled' && result.value ? [[ids[i], result.value] as [string, string]] : []
    ));
}

// Starts one batched lookup per kind for the IDs nobody has resolved or
// started yet, and calls onSettled as each lookup finishes. Each ID's result
// is stored independently of the others and of whichever hook asked.
function resolveRefs(refs: Array<{kind: IdKind; id: string}>, onSettled: () => void) {
    const idsByKind = new Map<IdKind, string[]>();
    for (const {kind, id} of refs) {
        const key = refKey(kind, id);
        if (!hints.has(key) && !inflight.has(key)) {
            idsByKind.set(kind, [...(idsByKind.get(kind) ?? []), id]);
        }
    }
    idsByKind.forEach((ids, kind) => {
        const lookup = fetchHints(kind, ids).catch(() => [] as Array<[string, string]>).then((found) => {
            found.forEach(([id, hint]) => hints.set(refKey(kind, id), escapeUnsafe(hint)));
            ids.forEach((id) => inflight.delete(refKey(kind, id)));
        });
        ids.forEach((id) => inflight.set(refKey(kind, id), lookup));
    });
    new Set(refs.map(({kind, id}) => inflight.get(refKey(kind, id)))).forEach((lookup) => lookup?.then(onSettled));
}

/**
 * Looks up secondary hints (@username, display name · ~channel · team, post
 * snippet) for the Mattermost IDs held by a tool call's ID-named arguments.
 * Only well-formed IDs are ever sent to the server. Returns a lookup that
 * yields undefined until a hint is available, or for IDs that have none.
 */
export function useResolvedIds(entries: Array<[string, JSONValue]>): (key: string, id: string) => string | undefined {
    const refs = useMemo(() => {
        const found = new Map<string, {kind: IdKind; id: string}>();
        const perKind = new Map<IdKind, number>();
        for (const [key, value] of entries) {
            const kind = idKindForKey(key);
            if (kind) {
                for (const id of idsOf(value)) {
                    const count = perKind.get(kind) ?? 0;
                    if (!found.has(refKey(kind, id)) && count < MAX_LOOKUPS_PER_KIND) {
                        found.set(refKey(kind, id), {kind, id});
                        perKind.set(kind, count + 1);
                    }
                }
            }
        }
        return [...found.values()];
    }, [entries]);

    const [, setVersion] = useState(0);
    const refsKey = refs.map(({kind, id}) => refKey(kind, id)).sort().join(',');

    useEffect(() => {
        let mounted = true;
        resolveRefs(refs, () => {
            if (mounted) {
                setVersion((v) => v + 1);
            }
        });
        return () => {
            mounted = false;
        };

        // refs is derived from refsKey; keying on it keeps the lookup stable
        // while the set of IDs is unchanged.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [refsKey]);

    return (key, id) => {
        const kind = idKindForKey(key);
        return kind ? hints.get(refKey(kind, id)) : undefined; // eslint-disable-line no-undefined
    };
}

export function clearResolvedIdsForTest() {
    hints.clear();
    inflight.clear();
    teamLookups.clear();
}
