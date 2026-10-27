// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import type {LLMBot} from '@/bots';
import {getCustomPrompts as fetchCustomPromptsList, renderCustomPrompt} from '@/client';
import {AGENTS_ROUTE} from '@/components/agents/use_agents_route';
import {CustomPromptsHandler, fetchPinnedPromptIds, ShowCustomPromptsModalHandler} from '@/redux';
import {getCustomPrompts, getPinnedPromptIds} from '@/selectors';
import type {CustomPrompt} from '@/types';

export type CustomPromptsStatus = 'loading' | 'ready' | 'error';

export interface ComposerCustomPrompts {
    status: CustomPromptsStatus;

    // Pinned prompts first (in pin order), then the rest as the server lists them.
    pinned: CustomPrompt[];
    others: CustomPrompt[];
    reload: () => void;
}

// The custom prompts the host composer's "Custom prompts" AI action lists
// (CustomPromptsDropdown), from the same Redux slice. The list is fetched here
// rather than through fetchCustomPrompts so a failure shows as an error
// instead of an empty list; a success lands in Redux as that thunk's does.
export function useComposerCustomPrompts(): ComposerCustomPrompts {
    const dispatch = useDispatch();
    const prompts = useSelector(getCustomPrompts);
    const pinnedIds = useSelector(getPinnedPromptIds);
    const [status, setStatus] = useState<CustomPromptsStatus>('loading');
    const latest = useRef(0);

    const reload = useCallback(async () => {
        const id = ++latest.current;
        setStatus('loading');

        // Pins only order the list, so they stay best effort, as on the
        // Custom Prompts page and the RHS prompt buttons.
        dispatch(fetchPinnedPromptIds() as any);
        try {
            const list = await fetchCustomPromptsList();
            if (id === latest.current) {
                dispatch({type: CustomPromptsHandler, customPrompts: list});
                setStatus('ready');
            }
        } catch (e) {
            console.error('Failed to fetch custom prompts:', e); // eslint-disable-line no-console
            if (id === latest.current) {
                setStatus('error');
            }
        }
    }, [dispatch]);

    useEffect(() => {
        const requests = latest;
        reload();
        return () => {
            requests.current++;
        };
    }, [reload]);

    const {pinned, others} = useMemo(() => {
        const byId = new Map(prompts.map((p) => [p.id, p]));
        const pinnedList = pinnedIds.map((id) => byId.get(id)).filter((p): p is CustomPrompt => Boolean(p));
        const pinnedSet = new Set(pinnedList.map((p) => p.id));
        return {pinned: pinnedList, others: prompts.filter((p) => !pinnedSet.has(p.id))};
    }, [prompts, pinnedIds]);

    return {status, pinned, others, reload};
}

interface InsertCustomPromptArgs {
    bot?: LLMBot | null;
    channelId: string;
    setText: (text: string) => void;
    onError: () => void;
}

// Renders a prompt for this channel and agent and puts it in the composer,
// replacing its text, as CustomPromptsDropdown does. Outside the agent's own
// DM the text @mentions the agent so the post reaches it. Only the latest
// pick may write, and nothing is written after unmount.
export function useInsertCustomPrompt({bot, channelId, setText, onError}: InsertCustomPromptArgs) {
    const latest = useRef(0);
    const [pending, setPending] = useState(false);

    useEffect(() => () => {
        latest.current++;
    }, []);

    const insert = useCallback(async (prompt: CustomPrompt) => {
        const id = ++latest.current;
        setPending(true);
        try {
            const result = await renderCustomPrompt(prompt.id, channelId, bot?.id);
            if (id !== latest.current) {
                return;
            }
            const inOwnDM = Boolean(bot) && bot?.dmChannelID === channelId;
            setText(!inOwnDM && bot?.username ? `@${bot.username} ${result.rendered}` : result.rendered);
        } catch (e) {
            console.error('Failed to render custom prompt:', e); // eslint-disable-line no-console
            if (id === latest.current) {
                onError();
            }
        } finally {
            if (id === latest.current) {
                setPending(false);
            }
        }
    }, [bot, channelId, setText, onError]);

    return {insert, pending};
}

// The Agents product has a Custom Prompts page; elsewhere the modal opens.
export function isInAgentsProduct(): boolean {
    const history = window.WebappUtils?.browserHistory as {location?: {pathname?: string}} | undefined;
    const pathname = history?.location?.pathname ?? '';
    return pathname === AGENTS_ROUTE || pathname.startsWith(`${AGENTS_ROUTE}/`);
}

export function useManageCustomPrompts(toPrompts: () => void) {
    const dispatch = useDispatch();
    return useCallback(() => {
        if (isInAgentsProduct()) {
            toPrompts();
            return;
        }
        dispatch({type: ShowCustomPromptsModalHandler, show: true});
    }, [dispatch, toPrompts]);
}
