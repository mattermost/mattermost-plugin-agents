// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import {UserProfile} from '@mattermost/types/users';

import {getAgentVersions, getProfilesByIds} from '@/client';
import {AgentVersionList} from '@/types/agents';

export type AgentVersionHistory = {

    /** The newest page(s) of versions; null until the first load finishes. */
    list: AgentVersionList | null;
    listError: string;
    loadingOlder: boolean;
    olderError: string;

    /** Reloads the first page. Resolves to the new list, or null when superseded or failed. */
    refresh: () => Promise<AgentVersionList | null>;
    loadOlder: () => Promise<void>;
    authorLabel: (createdBy: string) => string;
}

export function errorMessage(e: unknown, fallback: string): string {
    const message = (e as {message?: unknown})?.message;
    return typeof message === 'string' && message.trim() ? message.trim() : fallback;
}

function userDisplayName(user: UserProfile): string {
    const fullName = [user.first_name, user.last_name].filter(Boolean).join(' ').trim();
    return user.nickname || fullName || user.username;
}

export function hasVersions(list: AgentVersionList | null): list is AgentVersionList {
    return list !== null && list.versions.length > 0 && list.currentVersion > 0;
}

/**
 * Loads an agent's version history and resolves the authors' display names.
 * Pass an empty id (e.g. while creating an agent) to skip loading.
 */
export function useAgentVersionHistory(agentId: string): AgentVersionHistory {
    const intl = useIntl();

    // Effects below must not re-run when a new intl object is created.
    const intlRef = useRef(intl);
    intlRef.current = intl;

    const [list, setList] = useState<AgentVersionList | null>(null);
    const [listError, setListError] = useState('');
    const [authors, setAuthors] = useState<Record<string, string>>({});
    const [loadingOlder, setLoadingOlder] = useState(false);
    const [olderError, setOlderError] = useState('');
    const requestedAuthors = useRef(new Set<string>());
    const mountedRef = useRef(true);

    // Bumped by every first-page load so that page responses requested before
    // it (e.g. older pages of the list a restore replaced) are dropped.
    const listGenerationRef = useRef(0);

    useEffect(() => {
        mountedRef.current = true;
        return () => {
            mountedRef.current = false;
        };
    }, []);

    const refresh = useCallback(async (): Promise<AgentVersionList | null> => {
        listGenerationRef.current += 1;
        const generation = listGenerationRef.current;
        setLoadingOlder(false);
        setOlderError('');
        if (!agentId) {
            setList(null);
            return null;
        }
        try {
            setListError('');
            const result = await getAgentVersions(agentId);
            if (!mountedRef.current || generation !== listGenerationRef.current) {
                return null;
            }
            const next = {...result, versions: result.versions ?? []};
            setList(next);
            return next;
        } catch (e) {
            if (mountedRef.current && generation === listGenerationRef.current) {
                setListError(errorMessage(e, intlRef.current.formatMessage({defaultMessage: 'Failed to load version history.'})));
            }
            return null;
        }
    }, [agentId]);

    const loadOlder = useCallback(async () => {
        if (!agentId || !list || loadingOlder || list.versions.length === 0) {
            return;
        }
        const generation = listGenerationRef.current;
        const before = list.versions[list.versions.length - 1].version;
        setLoadingOlder(true);
        setOlderError('');
        try {
            const result = await getAgentVersions(agentId, {before});
            if (!mountedRef.current || generation !== listGenerationRef.current) {
                return;
            }
            setList((prev) => {
                // A reload since the request replaced the list, so this page
                // no longer continues it.
                if (!prev || prev.versions[prev.versions.length - 1]?.version !== before) {
                    return prev;
                }
                const older = (result.versions ?? []).filter((v) => v.version < before);
                return {...prev, versions: [...prev.versions, ...older], hasMore: Boolean(result.hasMore)};
            });
        } catch (e) {
            if (mountedRef.current && generation === listGenerationRef.current) {
                setOlderError(errorMessage(e, intlRef.current.formatMessage({defaultMessage: 'Failed to load older versions.'})));
            }
        } finally {
            if (mountedRef.current && generation === listGenerationRef.current) {
                setLoadingOlder(false);
            }
        }
    }, [agentId, list, loadingOlder]);

    useEffect(() => {
        setList(null);
        setListError('');
        refresh();
    }, [refresh]);

    useEffect(() => {
        if (!list) {
            return () => {
                // Nothing to resolve before the list loads
            };
        }
        const ids = list.versions.
            map((v) => v.createdBy).
            filter((id) => id && !requestedAuthors.current.has(id));
        const unique = Array.from(new Set(ids));
        if (unique.length === 0) {
            return () => {
                // All authors already requested
            };
        }
        unique.forEach((id) => requestedAuthors.current.add(id));
        getProfilesByIds(unique).then((profiles) => {
            if (!mountedRef.current) {
                return;
            }
            setAuthors((prev) => {
                const next = {...prev};
                for (const profile of profiles) {
                    next[profile.id] = userDisplayName(profile);
                }
                return next;
            });
        }).catch(() => {
            // Unresolved authors fall back to a generic label.
            unique.forEach((id) => requestedAuthors.current.delete(id));
        });
        return () => {
            // Requests are tracked in requestedAuthors; results apply while mounted.
        };
    }, [list]);

    const authorLabel = useCallback((createdBy: string): string => {
        if (!createdBy) {
            return intl.formatMessage({defaultMessage: 'System'});
        }
        return authors[createdBy] ?? intl.formatMessage({defaultMessage: 'Unknown user'});
    }, [authors, intl]);

    return {list, listError, loadingOlder, olderError, refresh, loadOlder, authorLabel};
}
