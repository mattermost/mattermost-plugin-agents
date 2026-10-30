// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {AIThreadTitleSet, AIThreadsReceived, AIThreadsRequested, aiThreads, initialAIThreadsState, rhsTab, selectedPostId} from './redux';
import {showRHSList} from './redux_actions';

describe('rhsTab reducer', () => {
    test.each([
        // eslint-disable-next-line no-undefined -- the reducer's initial state is an undefined argument
        {name: 'starts on the new chat', state: undefined, action: {type: 'NONE'}, expected: 'new'},
        {name: 'shows the chat history', state: 'new' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'threads'}, expected: 'threads'},
        {name: 'shows the new chat', state: 'threads' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'new'}, expected: 'new'},
        {name: 'ignores an unknown tab', state: 'threads' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'bogus'}, expected: 'new'},
        {name: 'unrelated actions keep it', state: 'threads' as const, action: {type: 'SELECT_AI_POST', postId: ''}, expected: 'threads'},
    ])('$name', ({state, action, expected}) => {
        expect(rhsTab(state, action)).toBe(expected);
    });
});

describe('selectedPostId reducer', () => {
    test.each([
        {name: 'selects a thread', action: {type: 'SELECT_AI_POST', postId: 'root2'}, expected: 'root2'},
        {name: 'showing a list view closes the thread', action: showRHSList('threads'), expected: ''},
    ])('$name', ({action, expected}) => {
        expect(selectedPostId('root1', action)).toBe(expected);
    });
});

describe('aiThreads reducer', () => {
    const thread = (id: string, title: string) => ({id, channel_id: 'dm', bot_id: 'b', root_post_id: `root_${id}`, title, turn_count: 1, update_at: 1});
    const apply = (actions: Array<Record<string, unknown>>) => actions.reduce((state, action) => aiThreads(state, action as never), initialAIThreadsState);

    test.each([
        {
            name: 'stores a response',
            actions: [{type: AIThreadsRequested, requestId: 1}, {type: AIThreadsReceived, requestId: 1, threads: [thread('a', 'A')]}],
            expected: ['a:A'],
        },
        {
            name: 'drops a superseded response',
            actions: [{type: AIThreadsRequested, requestId: 1}, {type: AIThreadsRequested, requestId: 2}, {type: AIThreadsReceived, requestId: 1, threads: [thread('a', 'stale')]}],
            expected: null,
        },
        {
            name: 'a title set after the request started wins over its response',
            actions: [{type: AIThreadsRequested, requestId: 1}, {type: AIThreadTitleSet, conversationId: 'a', title: 'New'}, {type: AIThreadsReceived, requestId: 1, threads: [thread('a', '')]}],
            expected: ['a:New'],
        },
        {
            name: 'a title set before the request started is left to the response',
            actions: [{type: AIThreadTitleSet, conversationId: 'a', title: 'Old edit'}, {type: AIThreadsRequested, requestId: 1}, {type: AIThreadsReceived, requestId: 1, threads: [thread('a', 'Server')]}],
            expected: ['a:Server'],
        },
    ])('$name', ({actions, expected}) => {
        const state = apply(actions);
        expect(state.threads?.map((t) => `${t.id}:${t.title}`) ?? null).toEqual(expected);
    });
});
