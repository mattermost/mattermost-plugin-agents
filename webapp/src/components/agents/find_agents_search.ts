// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {LLMBot} from '@/bots';
import type {AgentThreadsByBot} from '@/hooks/use_agent_threads';
import type {AIThread} from '@/types/ai_thread';

// Mirrors the host quick switcher's cap on its "Recent" section.
export const MAX_RECENT_AGENTS = 20;

// Keeps a very broad query (e.g. one letter) from rendering every chat.
export const MAX_RESULTS_PER_SECTION = 50;

export interface ChatResult {
    bot: LLMBot;
    thread: AIThread;

    // Trimmed title, or the placeholder when the title has not been generated yet.
    title: string;
}

export interface FindAgentsResults {
    agents: LLMBot[];
    chats: ChatResult[];
}

// Lower-cases and strips diacritics so "Café" matches "cafe".
export const normalize = (text: string): string => {
    return text.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase();
};

const TIER_PREFIX = 0;
const TIER_WORD_PREFIX = 1;
const TIER_SUBSTRING = 2;
const TIER_NONE = Number.POSITIVE_INFINITY;

// 0: starts with the query, 1: a word starts with it, 2: contains it, none otherwise.
const matchTier = (text: string, query: string): number => {
    const haystack = normalize(text);
    const index = haystack.indexOf(query);
    if (index < 0) {
        return TIER_NONE;
    }
    if (index === 0) {
        return TIER_PREFIX;
    }
    if ((/[\s\-_.@]/).test(haystack[index - 1])) {
        return TIER_WORD_PREFIX;
    }
    return TIER_SUBSTRING;
};

// The agent's most recent chat activity; 0 when it has no chats.
const lastActivity = (bot: LLMBot, threadsByBot: AgentThreadsByBot): number => {
    return threadsByBot[bot.id]?.[0]?.update_at ?? 0;
};

// Empty state: agents with the most recently updated chat first, then agents
// without chats in their original list order (Array.sort is stable).
export const recentAgents = (bots: LLMBot[], threadsByBot: AgentThreadsByBot): LLMBot[] => {
    return [...bots].
        sort((a, b) => lastActivity(b, threadsByBot) - lastActivity(a, threadsByBot)).
        slice(0, MAX_RECENT_AGENTS);
};

// Agents rank by name match (then username match); chats rank by title match,
// then most recently updated. Chats are only searched by their own title.
export const searchAgents = (
    bots: LLMBot[],
    threadsByBot: AgentThreadsByBot,
    rawQuery: string,
    placeholderTitle: string,
): FindAgentsResults => {
    const query = normalize(rawQuery.trim());
    if (!query) {
        return {agents: [], chats: []};
    }

    const agents = bots.
        map((bot) => {
            const nameTier = matchTier(bot.displayName, query);
            const usernameTier = matchTier(bot.username ?? '', query);
            return {
                bot,

                // Username-only matches sort after every name match.
                rank: nameTier === TIER_NONE ? usernameTier + TIER_SUBSTRING + 1 : nameTier,
                matched: nameTier !== TIER_NONE || usernameTier !== TIER_NONE,
            };
        }).
        filter((entry) => entry.matched).
        sort((a, b) => a.rank - b.rank).
        slice(0, MAX_RESULTS_PER_SECTION).
        map((entry) => entry.bot);

    const chats: Array<ChatResult & {rank: number}> = [];
    for (const bot of bots) {
        for (const thread of threadsByBot[bot.id] ?? []) {
            const title = thread.title.trim() || placeholderTitle;
            const rank = matchTier(title, query);
            if (rank !== TIER_NONE && thread.root_post_id) {
                chats.push({bot, thread, title, rank});
            }
        }
    }
    chats.sort((a, b) => (a.rank - b.rank) || (b.thread.update_at - a.thread.update_at));

    return {
        agents,
        chats: chats.slice(0, MAX_RESULTS_PER_SECTION).map(({bot, thread, title}) => ({bot, thread, title})),
    };
};
