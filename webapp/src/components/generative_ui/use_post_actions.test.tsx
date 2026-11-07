// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, renderHook} from '@testing-library/react';
import {Provider} from 'react-redux';

import {doUIAction} from '@/client';
import type {ConversationResponse} from '@/types/conversation';

import {useGenerativeUIPostActions} from './use_post_actions';

jest.mock('@/client', () => ({doUIAction: jest.fn()}));

const REQUESTER = 'requester1234567890123456a';
const OTHER = 'other123456789012345678901';
const POST = 'reply12345678901234567890a';
const ROOT = 'root123456789012345678901a';

const record = {source_post_id: POST, block: 0, form: 'deploy', label: 'Deploy', fields: []};

const conversation = (turns: Array<{role: string; post_id: string | null; content?: unknown[]}>): ConversationResponse => ({
    id: 'conv1234567890123456789012',
    user_id: REQUESTER,
    turns: turns.map((turn, i) => ({id: `t${i}`, sequence: i + 1, content: [], ...turn})),
} as unknown as ConversationResponse);

const latestReply = conversation([{role: 'user', post_id: ROOT}, {role: 'assistant', post_id: POST}]);

const makeStore = (threadPosts: Record<string, any>) => {
    const state = {
        entities: {
            posts: {posts: threadPosts, postsInThread: {[ROOT]: Object.keys(threadPosts)}},
            users: {profiles: {[REQUESTER]: {id: REQUESTER, username: 'alice'}}},
        },
    };
    return {getState: () => state, subscribe: () => jest.fn(), dispatch: jest.fn()} as any;
};

const renderActions = (options: {conversation: ConversationResponse | null; currentUserId?: string; generating?: boolean; threadPosts?: Record<string, any>}) => renderHook(
    () => useGenerativeUIPostActions({
        post: {id: POST, root_id: ROOT},
        conversation: options.conversation,
        currentUserId: options.currentUserId ?? REQUESTER,
        generating: options.generating ?? false,
    }),
    {wrapper: ({children}) => <Provider store={makeStore(options.threadPosts ?? {})}>{children}</Provider>},
);

describe('useGenerativeUIPostActions', () => {
    it.each([
        {name: 'requester on the latest finished reply', conversation: latestReply, expected: {kind: 'editable'}},
        {name: 'channel onlooker', conversation: latestReply, currentUserId: OTHER, expected: {kind: 'onlooker', requesterUsername: 'alice'}},
        {name: 'reply still streaming', conversation: latestReply, generating: true, expected: {kind: 'readonly'}},
        {name: 'conversation not loaded', conversation: null, expected: {kind: 'readonly'}},
        {
            name: 'a later message',
            conversation: conversation([{role: 'user', post_id: ROOT}, {role: 'assistant', post_id: POST}, {role: 'user', post_id: 'later'}]),
            expected: {kind: 'stale'},
        },
        {
            name: 'reply waiting on a tool decision',
            conversation: conversation([{role: 'user', post_id: ROOT}, {role: 'assistant', post_id: POST, content: [{type: 'tool_use', status: 'pending'}]}]),
            expected: {kind: 'stale'},
        },
        {
            name: 'submission post in the thread',
            conversation: conversation([{role: 'user', post_id: ROOT}, {role: 'assistant', post_id: POST}, {role: 'user', post_id: 'sub'}]),
            threadPosts: {sub: {id: 'sub', user_id: REQUESTER, props: {from_plugin: 'true', ai_genui_submission: record}}},
            expected: {kind: 'submitted', submission: record},
        },
        {
            name: 'submission prop on a post the plugin did not create',
            conversation: latestReply,
            threadPosts: {forged: {id: 'forged', user_id: REQUESTER, props: {ai_genui_submission: record}}},
            expected: {kind: 'editable'},
        },
        {
            name: 'submission prop on another user\'s post',
            conversation: latestReply,
            threadPosts: {forged: {id: 'forged', user_id: OTHER, props: {from_plugin: 'true', ai_genui_submission: record}}},
            expected: {kind: 'editable'},
        },
    ])('$name', ({conversation: conv, currentUserId, generating, threadPosts, expected}) => {
        const {result} = renderActions({conversation: conv, currentUserId, generating, threadPosts});
        expect(result.current.interaction).toEqual(expected);
    });

    it('marks the reply submitted as soon as the server accepts the click', async () => {
        (doUIAction as jest.Mock).mockResolvedValue({post_id: 'sub'});
        const {result} = renderActions({conversation: latestReply});
        await act(async () => {
            await result.current.submit({block: 0, form: '', label: 'Show details', fields: []});
        });
        expect(doUIAction).toHaveBeenCalledWith(POST, expect.objectContaining({block: 0, form: '', label: 'Show details', fields: []}));
        expect((doUIAction as jest.Mock).mock.calls[0][1].idempotency_key).toMatch(/^[A-Za-z0-9-]{8,64}$/);
        expect(result.current.interaction).toEqual({kind: 'submitted', submission: {block: 0, form: '', label: 'Show details', fields: [], source_post_id: POST}});
    });
});
