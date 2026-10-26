// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {LLMBot} from '@/bots';
import type {AIThread} from '@/types/ai_thread';

import {MAX_RECENT_AGENTS, recentAgents, searchAgents} from './find_agents_search';

const bot = (id: string, displayName: string, username = id): LLMBot => ({id, displayName, username} as LLMBot);

const thread = (id: string, botId: string, title: string, updateAt: number): AIThread => ({
    id,
    channel_id: `dm_${botId}`,
    bot_id: botId,
    root_post_id: `root_${id}`,
    title,
    turn_count: 1,
    update_at: updateAt,
});

const bots = [
    bot('a', 'Alpha'),
    bot('b', 'Beta Reader'),
    bot('c', 'Café Helper'),
    bot('d', 'Delta', 'reader-bot'),
];

const threadsByBot = {
    a: [thread('a2', 'a', 'Reader notes', 50), thread('a1', 'a', '  ', 10)],
    b: [thread('b1', 'b', 'Plans', 30)],
};

describe('searchAgents', () => {
    const cases: Array<{name: string; query: string; agents: string[]; chats: string[]}> = [
        {name: 'prefix before word-prefix and username', query: 'rea', agents: ['b', 'd'], chats: ['a2']},
        {name: 'diacritics and case are ignored', query: 'CAFE', agents: ['c'], chats: []},
        {name: 'substring match', query: 'lph', agents: ['a'], chats: []},
        {name: 'empty title matches the placeholder', query: 'new', agents: [], chats: ['a1']},
        {name: 'no match', query: 'zzz', agents: [], chats: []},
        {name: 'blank query returns nothing', query: '   ', agents: [], chats: []},
    ];

    test.each(cases)('$name', ({query, agents, chats}) => {
        const result = searchAgents(bots, threadsByBot, query, 'New chat');
        expect(result.agents.map((b) => b.id)).toEqual(agents);
        expect(result.chats.map((c) => c.thread.id)).toEqual(chats);
    });

    test('chats rank by title match tier, then recency', () => {
        const byBot = {a: [thread('1', 'a', 'my plan', 99), thread('2', 'a', 'plan b', 1), thread('3', 'a', 'plan c', 5)]};
        const result = searchAgents(bots, byBot, 'plan', 'New chat');
        expect(result.chats.map((c) => c.thread.id)).toEqual(['3', '2', '1']);
    });
});

describe('recentAgents', () => {
    test('orders by latest thread, then agents without threads in list order', () => {
        expect(recentAgents(bots, threadsByBot).map((b) => b.id)).toEqual(['a', 'b', 'c', 'd']);
        const swapped = {b: [thread('b1', 'b', 'x', 99)], a: [thread('a1', 'a', 'y', 1)]};
        expect(recentAgents(bots, swapped).map((b) => b.id)).toEqual(['b', 'a', 'c', 'd']);
    });

    test('caps the list', () => {
        const many = Array.from({length: MAX_RECENT_AGENTS + 5}, (_, i) => bot(`x${i}`, `Agent ${i}`));
        expect(recentAgents(many, {})).toHaveLength(MAX_RECENT_AGENTS);
    });
});
