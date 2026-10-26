// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useMemo, useRef, useState} from 'react';

import {getAIThreads} from '@/client';
import type {LLMBot} from '@/bots';
import type {AIThread} from '@/types/ai_thread';

// Titles are generated asynchronously and no websocket event announces them,
// so a refresh after a new chat is retried once after this delay.
export const TITLE_RETRY_DELAY_MS = 4000;

export type AgentThreadsByBot = Record<string, AIThread[]>;

// Groups DM threads by bot. Fails closed: a bot without a dmChannelID
// contributes no threads, and threads for unknown bots are dropped.
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
    for (const list of Object.values(grouped)) {
        list.sort((a, b) => b.update_at - a.update_at);
    }
    return grouped;
}

export function useAgentThreads(bots: LLMBot[] | null) {
    const [allThreads, setAllThreads] = useState<AIThread[] | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<Error | null>(null);

    const requestCounter = useRef(0);
    const mounted = useRef(true);
    const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

    const clearRetry = useCallback(() => {
        if (retryTimer.current !== null) {
            clearTimeout(retryTimer.current);
            retryTimer.current = null;
        }
    }, []);

    useEffect(() => {
        const counter = requestCounter;
        mounted.current = true;
        return () => {
            mounted.current = false;
            counter.current++;
            clearRetry();
        };
    }, [clearRetry]);

    const refresh = useCallback(async () => {
        const requestID = ++requestCounter.current;
        setLoading(true);
        try {
            const result = await getAIThreads();
            if (!mounted.current || requestID !== requestCounter.current) {
                return;
            }
            setAllThreads(Array.isArray(result) ? result : []);
            setError(null);
            setLoading(false);
        } catch (e) {
            if (!mounted.current || requestID !== requestCounter.current) {
                return;
            }
            setError(e instanceof Error ? e : new Error(String(e)));
            setLoading(false);
        }
    }, []);

    const refreshWithTitleRetry = useCallback(async () => {
        clearRetry();
        retryTimer.current = setTimeout(() => {
            retryTimer.current = null;
            refresh();
        }, TITLE_RETRY_DELAY_MS);
        await refresh();
    }, [clearRetry, refresh]);

    useEffect(() => {
        refresh();
    }, [refresh]);

    const threadsByBot = useMemo<AgentThreadsByBot>(() => {
        if (!allThreads || !bots) {
            return {};
        }
        return groupDMThreadsByBot(allThreads, bots);
    }, [allThreads, bots]);

    return {threadsByBot, loading, error, refresh, refreshWithTitleRetry};
}
