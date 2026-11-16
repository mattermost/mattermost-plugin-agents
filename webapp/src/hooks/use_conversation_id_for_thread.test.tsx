// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {renderHook} from '@testing-library/react';

import manifest from '@/manifest';

import {useConversationIdForThread} from './use_conversation_id_for_thread';

const BOT_ID = 'botuserid12345678901234567';
const USER_ID = 'useruserid1234567890123456';
const BOT_CONV = 'botconvid12345678901234567';
const FORGED_CONV = 'forgedconv1234567890123456';

const makeStore = (posts: Record<string, {user_id: string; conversation_id?: string}>, replies: string[]) => createStore(() => ({
    entities: {
        posts: {
            posts: Object.fromEntries(Object.entries(posts).map(([id, p]) => [id, {id, user_id: p.user_id, props: {conversation_id: p.conversation_id}}])),
            postsInThread: {root: replies},
        },
    },
    ['plugins-' + manifest.id]: {bots: [{id: BOT_ID}]},
}));

describe('useConversationIdForThread', () => {
    test.each([
        {
            name: 'reads the id from a bot reply',
            posts: {root: {user_id: USER_ID}, reply: {user_id: BOT_ID, conversation_id: BOT_CONV}},
            expected: BOT_CONV,
        },
        {
            name: 'ignores an id forged on a user post',
            posts: {root: {user_id: USER_ID, conversation_id: FORGED_CONV}, reply: {user_id: BOT_ID, conversation_id: BOT_CONV}},
            expected: BOT_CONV,
        },
        {
            name: 'finds nothing when only user posts carry an id',
            posts: {root: {user_id: USER_ID}, reply: {user_id: USER_ID, conversation_id: FORGED_CONV}},
            expected: '',
        },
    ])('$name', ({posts, expected}) => {
        const store = makeStore(posts, ['reply']);
        const {result} = renderHook(() => useConversationIdForThread('root'), {
            wrapper: ({children}: {children: React.ReactNode}) => <Provider store={store}>{children}</Provider>,
        });
        expect(result.current).toBe(expected);
    });
});
