// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useMemo, useState} from 'react';

import {getChannel, getPost, getProfilesByIds, getTeamsByIds} from '@/client';

import {JSONValue} from './tool_types';

export type IdKind = 'user' | 'channel' | 'team' | 'post';

const POST_SNIPPET_LENGTH = 80;

const kindPatterns: Array<[IdKind, RegExp]> = [
    ['user', /(^|_)user_ids?$/],
    ['channel', /(^|_)channel_ids?$/],
    ['team', /(^|_)team_ids?$/],
    ['post', /(^|_)(post|root|thread|parent)_ids?$/],
];

// Resolved names outlive a single card so a re-rendered or reloaded post
// doesn't refetch what it already looked up.
const resolvedNames = new Map<string, string>();
const failedRefs = new Set<string>();
const inflight = new Map<string, Promise<void>>();

function refKey(kind: IdKind, id: string) {
    return `${kind}:${id}`;
}

export function idKindForKey(key: string): IdKind | null {
    const match = kindPatterns.find(([, pattern]) => pattern.test(key));
    return match ? match[0] : null;
}

function idsOf(value: JSONValue): string[] {
    if (typeof value === 'string') {
        return value === '' ? [] : [value];
    }
    if (Array.isArray(value)) {
        return value.filter((item): item is string => typeof item === 'string' && item !== '');
    }
    return [];
}

function snippet(message: string) {
    const flat = message.replace(/\s+/g, ' ').trim();
    return flat.length > POST_SNIPPET_LENGTH ? `${flat.slice(0, POST_SNIPPET_LENGTH)}…` : flat;
}

// Mirrors llm.SanitizeNonPrintableChars: names and post text are user-authored,
// so bidi and invisible characters must not alter what an approval card says.
const unsafeChars = /(?![ \n\t\r])[\p{C}\p{Z}\p{Variation_Selector}\p{Default_Ignorable_Code_Point}]/gu;

function escapeUnsafe(text: string) {
    return text.replace(unsafeChars, (ch) => `[U+${ch.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}]`);
}

async function fetchName(kind: Exclude<IdKind, 'user'>, id: string): Promise<string> {
    switch (kind) {
    case 'team': {
        const [team] = await getTeamsByIds([id]);
        return team.display_name || team.name;
    }
    case 'channel': {
        const channel = await getChannel(id);
        return channel.display_name || channel.name;
    }
    default: {
        const post = await getPost(id);
        return snippet(post.message);
    }
    }
}

// Each ID settles on its own, so one missing team, channel or post never
// discards the names that did resolve.
async function fetchNames(kind: IdKind, ids: string[]): Promise<Array<[string, string]>> {
    if (kind === 'user') {
        const profiles = await getProfilesByIds(ids);
        return profiles.map((profile) => [profile.id, escapeUnsafe(profile.username)]);
    }
    const settled = await Promise.allSettled(ids.map((id) => fetchName(kind, id)));
    return settled.flatMap((result, i) => (
        result.status === 'fulfilled' && result.value ? [[ids[i], escapeUnsafe(result.value)] as [string, string]] : []
    ));
}

// Starts one batched lookup per kind for the IDs nobody has resolved, failed or
// started yet, and calls onSettled as each lookup finishes. Each ID's result is
// stored independently of the others and of whichever hook asked.
function resolveRefs(refs: Array<{kind: IdKind; id: string}>, onSettled: () => void) {
    const idsByKind = new Map<IdKind, string[]>();
    for (const {kind, id} of refs) {
        const key = refKey(kind, id);
        if (!resolvedNames.has(key) && !failedRefs.has(key) && !inflight.has(key)) {
            idsByKind.set(kind, [...(idsByKind.get(kind) ?? []), id]);
        }
    }
    idsByKind.forEach((ids, kind) => {
        const lookup = fetchNames(kind, ids).catch(() => [] as Array<[string, string]>).then((names) => {
            names.forEach(([id, name]) => resolvedNames.set(refKey(kind, id), name));
            ids.forEach((id) => {
                inflight.delete(refKey(kind, id));
                if (!resolvedNames.has(refKey(kind, id))) {
                    failedRefs.add(refKey(kind, id));
                }
            });
        });
        ids.forEach((id) => inflight.set(refKey(kind, id), lookup));
    });
    new Set(refs.map(({kind, id}) => inflight.get(refKey(kind, id)))).forEach((lookup) => lookup?.then(onSettled));
}

/**
 * Resolves the opaque Mattermost IDs held by a tool call's ID-named arguments
 * (user_id, channel_ids, team_id, post_id, ...) to human-readable names.
 * Returns a lookup that yields undefined until an ID is resolved, or for
 * IDs that can't be (missing permission, deleted, other kinds of ID), so the
 * caller keeps showing the raw value.
 */
export function useResolvedIds(entries: Array<[string, JSONValue]>): (key: string, id: string) => string | undefined {
    const refs = useMemo(() => {
        const found = new Map<string, {kind: IdKind; id: string}>();
        for (const [key, value] of entries) {
            const kind = idKindForKey(key);
            if (kind) {
                idsOf(value).forEach((id) => found.set(refKey(kind, id), {kind, id}));
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
        return kind ? resolvedNames.get(refKey(kind, id)) : undefined; // eslint-disable-line no-undefined
    };
}

export function clearResolvedIdsForTest() {
    resolvedNames.clear();
    failedRefs.clear();
    inflight.clear();
}
