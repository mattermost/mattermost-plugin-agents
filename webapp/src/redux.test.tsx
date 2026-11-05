// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getCustomPromptPins, getCustomPrompts} from './client';
import {AIThreadTitleSet, AIThreadsReceived, AIThreadsRequested, CustomPromptsHandler, PinnedPromptIdsHandler, aiThreads, fetchCustomPrompts, fetchPinnedPromptIds, initialAIThreadsState, rhsTab, selectedPostId} from './redux';
import {showRHSList} from './redux_actions';

// Only the two loaders under test are replaced.
jest.mock('./client', () => ({
    ...jest.requireActual('./client'),
    getCustomPrompts: jest.fn(),
    getCustomPromptPins: jest.fn(),
}));

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
            name: 'a title set renames the listed chat',
            actions: [{type: AIThreadsRequested, requestId: 1}, {type: AIThreadsReceived, requestId: 1, threads: [thread('a', 'Old'), thread('b', 'B')]}, {type: AIThreadTitleSet, conversationId: 'a', title: 'New'}],
            expected: ['a:New', 'b:B'],
        },
        {
            name: 'a title set before any list loaded leaves it unloaded',
            actions: [{type: AIThreadTitleSet, conversationId: 'a', title: 'New'}],
            expected: null,
        },
    ])('$name', ({actions, expected}) => {
        const state = apply(actions);
        expect(state.threads?.map((t) => `${t.id}:${t.title}`) ?? null).toEqual(expected);
    });
});

// A loader that finishes after a newer one was started must not overwrite the newer result.
describe('prompt and pin loaders', () => {
    const deferred = <T, >() => {
        let resolve!: (value: T) => void;
        const promise = new Promise<T>((res) => {
            resolve = res;
        });
        return {promise, resolve};
    };

    test.each([
        {name: 'prompts', api: getCustomPrompts, load: fetchCustomPrompts, type: CustomPromptsHandler, key: 'customPrompts', older: [{id: 'old'}], newer: [{id: 'new'}]},
        {name: 'pins', api: getCustomPromptPins, load: fetchPinnedPromptIds, type: PinnedPromptIdsHandler, key: 'pinnedPromptIds', older: ['old'], newer: ['new']},
    ])('keeps the newest $name response when an older request finishes last', async ({api, load, type, key, older, newer}) => {
        const first = deferred<unknown>();
        const second = deferred<unknown>();
        (api as jest.Mock).mockReset().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
        const dispatch = jest.fn();

        const a = load()(dispatch);
        const b = load()(dispatch);
        second.resolve(newer);
        await b;
        first.resolve(older);
        await a;

        expect(dispatch).toHaveBeenCalledTimes(1);
        expect(dispatch).toHaveBeenCalledWith({type, [key]: newer});
    });

    test.each([
        {name: 'prompts', api: getCustomPrompts, load: fetchCustomPrompts},
        {name: 'pins', api: getCustomPromptPins, load: fetchPinnedPromptIds},
    ])('still stores a single $name response', async ({api, load}) => {
        (api as jest.Mock).mockReset().mockResolvedValue([]);
        const dispatch = jest.fn();
        await load()(dispatch);
        expect(dispatch).toHaveBeenCalledTimes(1);
    });
});
