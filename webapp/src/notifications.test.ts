// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {LLM_BOT_REPLY_DEBOUNCE_TIMEOUT_MS, shouldSuppressBotNotification} from './notifications';

describe('shouldSuppressBotNotification', () => {
    const fakeNow = 10_000;

    it('suppresses any custom_llmbot post regardless of root_id (MM-66720)', () => {
        const rootlessAgentPost = {
            user_id: 'agent-bot',
            type: 'custom_llmbot',
        };

        expect(shouldSuppressBotNotification(rootlessAgentPost, {now: fakeNow})).toBe(true);

        const threadedAgentPost = {
            user_id: 'agent-bot',
            root_id: 'parent',
            type: 'custom_llmbot',
        };

        expect(shouldSuppressBotNotification(threadedAgentPost, {now: fakeNow})).toBe(true);
    });

    it('does not suppress notifications for posts authored by a human user', () => {
        const humanPost = {
            user_id: 'human-user',
            type: '',
        };

        expect(shouldSuppressBotNotification(humanPost, {now: fakeNow})).toBe(false);
    });

    it('does not suppress posts without a user_id (malformed events)', () => {
        expect(shouldSuppressBotNotification({type: 'custom_llmbot'}, {now: fakeNow})).toBe(false);
        expect(shouldSuppressBotNotification(null, {now: fakeNow})).toBe(false);
    });

    it('suppresses fast bot replies to the current user inside the debounce window', () => {
        const post = {
            user_id: 'some-bot',
            root_id: 'parent-1',
            props: {from_bot: 'true'},
        };

        expect(
            shouldSuppressBotNotification(post, {
                now: fakeNow,
                currentUserId: 'user-1',
                parentPost: {user_id: 'user-1', create_at: fakeNow - 100},
            }),
        ).toBe(true);
    });

    it('does not suppress threaded bot replies outside the debounce window', () => {
        const post = {
            user_id: 'some-bot',
            root_id: 'parent-1',
            props: {from_bot: 'true'},
        };

        expect(
            shouldSuppressBotNotification(post, {
                now: fakeNow,
                currentUserId: 'user-1',
                parentPost: {
                    user_id: 'user-1',
                    create_at: fakeNow - (LLM_BOT_REPLY_DEBOUNCE_TIMEOUT_MS + 5),
                },
            }),
        ).toBe(false);
    });

    it('does not suppress bot replies to another user inside the debounce window', () => {
        const post = {
            user_id: 'some-bot',
            root_id: 'parent-1',
            props: {from_bot: 'true'},
        };

        expect(
            shouldSuppressBotNotification(post, {
                now: fakeNow,
                currentUserId: 'user-1',
                parentPost: {user_id: 'user-2', create_at: fakeNow - 100},
            }),
        ).toBe(false);
    });

    it('does not suppress threaded posts without from_bot=true', () => {
        const post = {
            user_id: 'some-user',
            root_id: 'parent-1',
            props: {},
        };

        expect(
            shouldSuppressBotNotification(post, {
                now: fakeNow,
                currentUserId: 'user-1',
                parentPost: {user_id: 'user-1', create_at: fakeNow - 100},
            }),
        ).toBe(false);
    });

    it('treats from_bot as the string "true" only (Mattermost emits it as a string)', () => {
        const post = {
            user_id: 'some-bot',
            root_id: 'parent-1',
            props: {from_bot: true as unknown as string},
        };

        expect(
            shouldSuppressBotNotification(post, {
                now: fakeNow,
                currentUserId: 'user-1',
                parentPost: {user_id: 'user-1', create_at: fakeNow - 100},
            }),
        ).toBe(false);
    });

    it('does not suppress when currentUserId is unknown (login/logout race)', () => {
        const post = {
            user_id: 'some-bot',
            root_id: 'parent-1',
            props: {from_bot: 'true'},
        };

        expect(
            shouldSuppressBotNotification(post, {
                now: fakeNow,
                parentPost: {user_id: 'user-1', create_at: fakeNow - 100},
            }),
        ).toBe(false);
    });
});
