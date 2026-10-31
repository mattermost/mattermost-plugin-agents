// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useMemo} from 'react';
import {useSelector, useStore} from 'react-redux';
import type {Dispatch, UnknownAction} from 'redux';

import {GlobalState} from '@mattermost/types/store';

import {isCurrentUsersAgentPost} from '@/agent_posts';
import {archiveConversation, getAIThreads, renameConversation} from '@/client';
import type {LLMBot} from '@/bots';
import manifest from '@/manifest';
import {
    AIThreadsFailed,
    AIThreadsReceived,
    AIThreadsRequested,
    AIThreadRemoved,
    AIThreadRestored,
    AIThreadTitleSet,
    initialAIThreadsState,
    type AIThreadsState,
} from '@/redux';
import type {AIThread} from '@/types/ai_thread';
import type {PluginWebSocketMessage} from '@/types';
import type {PostUpdateWebsocketMessage} from '@/components/llmbot_post/llmbot_post';

export type AgentThreadsByBot = Record<string, AIThread[]>;

interface ThreadsStore {
    dispatch: Dispatch<UnknownAction>;
    getState: () => unknown;
}

// Groups DM threads by bot, in the server's order (most recent turn first). Fails closed: a bot
// without a dmChannelID contributes no threads, and threads for unknown bots are dropped.
export function groupDMThreadsByBot(threads: AIThread[], bots: LLMBot[]): AgentThreadsByBot {
    const dmChannelByBot = new Map<string, string>();
    for (const bot of bots) {
        if (bot.dmChannelID) {
            dmChannelByBot.set(bot.id, bot.dmChannelID);
        }
    }

    const grouped: AgentThreadsByBot = {};
    for (const thread of threads) {
        const dmChannelID = dmChannelByBot.get(thread.bot_id);
        if (!dmChannelID || thread.channel_id !== dmChannelID || !thread.root_post_id) {
            continue;
        }
        (grouped[thread.bot_id] ??= []).push(thread);
    }
    return grouped;
}

const pluginState = (state: unknown): Record<string, any> | undefined => (state as Record<string, any> | undefined)?.[`plugins-${manifest.id}`];

export const selectAIThreadsState = (state: unknown): AIThreadsState => pluginState(state)?.aiThreads ?? initialAIThreadsState;

export const selectAIThreads = (state: unknown): AIThread[] | null => selectAIThreadsState(state).threads;

// A view loaded the list (or tried to), so keep it current.
const isInUse = (state: unknown) => {
    const {threads, loading, error} = selectAIThreadsState(state);
    return threads !== null || loading || error;
};

let lastRequestId = 0;

// The one fetch path for the thread list. A newer request supersedes an older one in flight.
export async function fetchAIThreads(dispatch: Dispatch<UnknownAction>): Promise<void> {
    const requestId = ++lastRequestId;
    dispatch({type: AIThreadsRequested, requestId});
    try {
        const threads = await getAIThreads();
        dispatch({type: AIThreadsReceived, requestId, threads});
    } catch (e) {
        console.error('Failed to load AI threads:', e); // eslint-disable-line no-console
        dispatch({type: AIThreadsFailed, requestId});
    }
}

// Fetches unless the list is loaded or loading: views that mount together share one request.
export function loadAIThreadsIfNeeded(store: ThreadsStore): Promise<void> {
    const {threads, loading} = selectAIThreadsState(store.getState());
    if (threads !== null || loading) {
        return Promise.resolve();
    }
    return fetchAIThreads(store.dispatch);
}

// Refetches a list that is in use, e.g. after a new chat was created.
export function refreshAIThreadsIfInUse(store: ThreadsStore): Promise<void> {
    if (!isInUse(store.getState())) {
        return Promise.resolve();
    }
    return fetchAIThreads(store.dispatch);
}

const storedTitle = (state: unknown, conversationId: string) => selectAIThreads(state)?.find((t) => t.id === conversationId)?.title;

// Renames a chat, showing the new title right away. A failed rename restores the old one (unless a
// newer title arrived meanwhile) and rejects.
export async function renameAIThread(store: ThreadsStore, conversationId: string, title: string): Promise<void> {
    const previous = storedTitle(store.getState(), conversationId);
    if (typeof previous === 'string') {
        store.dispatch({type: AIThreadTitleSet, conversationId, title});
    }
    try {
        await renameConversation(conversationId, title);
    } catch (e) {
        if (typeof previous === 'string' && storedTitle(store.getState(), conversationId) === title) {
            store.dispatch({type: AIThreadTitleSet, conversationId, title: previous});
        }
        throw e;
    }
}

// Archives a chat, taking it out of the list right away. A failed archive puts it back where it
// was and rejects.
export async function archiveAIThread(store: ThreadsStore, conversationId: string): Promise<void> {
    const threads = selectAIThreads(store.getState()) ?? [];
    const index = threads.findIndex((t) => t.id === conversationId);
    const previous = index >= 0 ? threads[index] : null;
    store.dispatch({type: AIThreadRemoved, conversationId});
    try {
        await archiveConversation(conversationId);
    } catch (e) {
        if (previous) {
            store.dispatch({type: AIThreadRestored, thread: previous, index});
        }
        throw e;
    }
}

// Refetches after a burst of events settles, so a busy stream of replies costs one request.
export const THREADS_REFRESH_DEBOUNCE_MS = 1000;

export interface AgentThreadsSync {

    // custom_mattermost-ai_postupdate.
    handlePostUpdate: (msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => void;

    // custom_mattermost-ai_conversation_updated: a rename, a generated title, or an archive.
    handleConversationUpdated: (data: {conversation_id?: string; title?: string; archived?: boolean}) => void;

    // The websocket reconnected, so events may have been missed.
    handleReconnect: () => void;
}

// Keeps the stored thread list current from websocket events; index.tsx creates one for the
// plugin's lifetime. A list no view has loaded is never fetched.
export function createAgentThreadsSync(store: {dispatch: Dispatch<UnknownAction>; getState: () => GlobalState}): AgentThreadsSync {
    let timer: ReturnType<typeof setTimeout> | null = null;

    const scheduleRefresh = () => {
        if (timer !== null) {
            clearTimeout(timer);
        }
        timer = setTimeout(() => {
            timer = null;
            refreshAIThreadsIfInUse(store);
        }, THREADS_REFRESH_DEBOUNCE_MS);
    };

    return {
        handlePostUpdate: (msg) => {
            // A finished turn moves its chat to the top. postupdate events reach every member of
            // the channel, so only the current user's own agent chats count.
            if (msg.data?.control === 'end' && isInUse(store.getState()) && isCurrentUsersAgentPost(store.getState(), msg.data.post_id)) {
                scheduleRefresh();
            }
        },
        handleConversationUpdated: (data) => {
            const conversationId = data?.conversation_id;
            if (!conversationId) {
                return;
            }

            // Archived here or in another window: a reply in it restores it, and the finished
            // turn's refetch brings it back.
            if (data.archived === true) {
                store.dispatch({type: AIThreadRemoved, conversationId});
                return;
            }
            if (typeof data.title !== 'string') {
                return;
            }
            store.dispatch({type: AIThreadTitleSet, conversationId, title: data.title});

            // A chat the list does not have yet: fetch it.
            const threads = selectAIThreads(store.getState());
            if (threads && !threads.some((t) => t.id === conversationId)) {
                scheduleRefresh();
            }
        },
        handleReconnect: () => {
            if (isInUse(store.getState())) {
                scheduleRefresh();
            }
        },
    };
}

// The list views' data: the stored thread list (loaded on first use) grouped by agent.
export function useAgentThreads(bots: LLMBot[] | null) {
    const store = useStore<GlobalState>();
    const {threads, loading, error} = useSelector<GlobalState, AIThreadsState>(selectAIThreadsState);

    useEffect(() => {
        loadAIThreadsIfNeeded(store);
    }, [store]);

    const refresh = useCallback(() => fetchAIThreads(store.dispatch), [store]);

    const threadsByBot = useMemo<AgentThreadsByBot>(() => {
        if (!threads || !bots) {
            return {};
        }
        return groupDMThreadsByBot(threads, bots);
    }, [threads, bots]);

    return {
        threads,
        threadsByBot,

        // Loading until the first response, even before the request starts.
        loading: loading || (threads === null && !error),
        error,
        refresh,
    };
}

// For a view that just created a chat: the list picks it up (its title follows over the websocket).
export function useRefreshAgentThreads() {
    const store = useStore<GlobalState>();
    return useCallback(() => {
        refreshAIThreadsIfInUse(store);
    }, [store]);
}
