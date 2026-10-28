// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {doDisableAgentMentionReminder, doLoopInAgent} from '@/client';

import {AgentMentionReminderPost} from './agent_mention_reminder_post';

jest.mock('@/client', () => ({
    doLoopInAgent: jest.fn(),
    doDisableAgentMentionReminder: jest.fn(),
}));

const mockedDoLoopInAgent = doLoopInAgent as jest.MockedFunction<typeof doLoopInAgent>;
const mockedDoDisableAgentMentionReminder = doDisableAgentMentionReminder as jest.MockedFunction<typeof doDisableAgentMentionReminder>;

// Mattermost IDs are 26 characters of lowercase letters and digits.
const POST_ID = 'ehz9k3wqr7t1a5m2xd8pnb4jsc';
const BOT_USER_ID = 'b7m2xq9d4wn1c6zr3t8kv5jhya';
const TARGET_POST_ID = 'kq3n7vd1x9r4bz2m8sw6t5jhpc';
const POST_MESSAGE = 'To respond to an agent you must @mention them.';

// Only the server stamps this type onto a post.
const EPHEMERAL_POST_TYPE = 'system_ephemeral';

type PostProps = {
    bot_user_id?: unknown;
    bot_username?: unknown;
    bot_display_name?: unknown;
    target_post_id?: unknown;
};

type PostOverrides = {id?: string; type?: string; message?: string};

function renderPost(props?: PostProps, overrides?: PostOverrides) {
    return render(
        <IntlProvider locale='en'>
            <AgentMentionReminderPost
                post={{
                    id: overrides?.id ?? POST_ID,
                    type: overrides?.type ?? EPHEMERAL_POST_TYPE,
                    message: overrides?.message ?? POST_MESSAGE,
                    props,
                }}
            />
        </IntlProvider>,
    );
}

describe('AgentMentionReminderPost', () => {
    beforeEach(() => {
        mockedDoLoopInAgent.mockReset();
        mockedDoLoopInAgent.mockImplementation(() => Promise.resolve());
        mockedDoDisableAgentMentionReminder.mockReset();
        mockedDoDisableAgentMentionReminder.mockImplementation(() => Promise.resolve());
    });

    test('falls back to the bot username when no display name is set', () => {
        renderPost({bot_user_id: BOT_USER_ID, bot_username: 'matty'});
        expect(screen.getByRole('link', {name: 'Click here to loop in @matty'})).not.toBeNull();
    });

    test('clicking the link loops in the agent and shows a confirmation', async () => {
        renderPost({bot_user_id: BOT_USER_ID, bot_username: 'matty', bot_display_name: 'Matty', target_post_id: TARGET_POST_ID});

        fireEvent.click(screen.getByRole('link', {name: 'Click here to loop in @Matty'}));

        expect(mockedDoLoopInAgent).toHaveBeenCalledWith(TARGET_POST_ID, BOT_USER_ID);
        expect(await screen.findByText('Looped in @Matty.')).not.toBeNull();
    });

    test('clicking "Don\'t show again" persists the preference and confirms', async () => {
        renderPost({bot_user_id: BOT_USER_ID, bot_username: 'matty', bot_display_name: 'Matty', target_post_id: TARGET_POST_ID});

        fireEvent.click(screen.getByRole('button', {name: "Don't show again"}));

        expect(mockedDoDisableAgentMentionReminder).toHaveBeenCalledTimes(1);
        expect(await screen.findByText("You won't see this reminder again.")).not.toBeNull();
    });

    test('a failed "Don\'t show again" surfaces an error and keeps the loop-in action', async () => {
        mockedDoDisableAgentMentionReminder.mockImplementation(() => Promise.reject(new Error('nope')));
        renderPost({bot_user_id: BOT_USER_ID, bot_username: 'matty', bot_display_name: 'Matty', target_post_id: TARGET_POST_ID});

        fireEvent.click(screen.getByRole('button', {name: "Don't show again"}));

        expect(await screen.findByText('Failed to update your preference. Please try again.')).not.toBeNull();
        expect(screen.getByRole('link', {name: 'Click here to loop in @Matty'})).not.toBeNull();
    });

    test('the plain fallback message offers no "Don\'t show again" button', () => {
        renderPost({}, {message: POST_MESSAGE});
        expect(screen.queryByRole('button', {name: "Don't show again"})).toBeNull();
    });

    test('falls back to the post id when no target post id is set', async () => {
        renderPost({bot_user_id: BOT_USER_ID, bot_username: 'matty', bot_display_name: 'Matty'});

        fireEvent.click(screen.getByRole('link', {name: 'Click here to loop in @Matty'}));

        expect(mockedDoLoopInAgent).toHaveBeenCalledWith(POST_ID, BOT_USER_ID);
        expect(await screen.findByText('Looped in @Matty.')).not.toBeNull();
    });

    test('renders the plain fallback message when no bot user id is present', () => {
        renderPost({}, {message: POST_MESSAGE});
        expect(screen.getByText(POST_MESSAGE)).not.toBeNull();
        expect(screen.queryByRole('link')).toBeNull();
    });

    test('offers no loop-in when only the username is present', () => {
        renderPost({bot_username: 'matty', bot_display_name: 'Matty', target_post_id: TARGET_POST_ID});

        expect(screen.queryByRole('link', {name: /Click here to loop in/})).toBeNull();
        expect(mockedDoLoopInAgent).not.toHaveBeenCalled();
    });

    test('a renamed agent is looped in by its bot user id, not the displayed name', () => {
        renderPost({bot_user_id: BOT_USER_ID, bot_username: 'new-name', bot_display_name: 'New Name', target_post_id: TARGET_POST_ID});

        fireEvent.click(screen.getByRole('link', {name: 'Click here to loop in @New Name'}));

        expect(mockedDoLoopInAgent).toHaveBeenCalledWith(TARGET_POST_ID, BOT_USER_ID);
    });

    const notWellFormedTargetPostIds: Array<{name: string; targetPostId: string}> = [
        {name: 'relative path segments', targetPostId: '../../some/other/route'},
        {name: 'right length but contains a separator', targetPostId: 'abcdefghijklmnopqrstuvwxy/'},
        {name: 'well-formed id with leading whitespace', targetPostId: ` ${TARGET_POST_ID}`},
    ];

    test.each(notWellFormedTargetPostIds)('renders a plain hint when target_post_id is not well-formed: $name', ({targetPostId}) => {
        renderPost({bot_user_id: BOT_USER_ID, bot_username: 'matty', bot_display_name: 'Matty', target_post_id: targetPostId});

        expect(screen.queryByRole('link', {name: /Click here to loop in/})).toBeNull();
        expect(mockedDoLoopInAgent).not.toHaveBeenCalled();
        expect(screen.getByText(POST_MESSAGE)).not.toBeNull();
    });

    const notStrings: Array<{name: string; value: unknown}> = [
        {name: 'a number', value: 42},
        {name: 'a boolean', value: true},
        {name: 'an object', value: {}},
        {name: 'an array', value: ['matty']},
    ];

    const notStringProps = (['bot_user_id', 'bot_username', 'bot_display_name', 'target_post_id'] as Array<keyof PostProps>).
        flatMap((prop) => notStrings.map(({name, value}) => ({prop, name, value})));

    test.each(notStringProps)('renders when $prop is $name', ({prop, value}) => {
        const props: PostProps = {
            bot_user_id: BOT_USER_ID,
            bot_username: 'matty',
            bot_display_name: 'Matty',
            target_post_id: TARGET_POST_ID,
        };
        props[prop] = value;

        expect(() => renderPost(props)).not.toThrow();
    });

    test.each([
        {name: 'an empty type', type: ''},
        {name: 'the custom post type from the props', type: 'custom_agent_mention_reminder'},
        {name: 'an unrelated custom type', type: 'custom_llmbot'},
    ])('offers no action on a post that is not ephemeral: $name', ({type}) => {
        renderPost(
            {bot_user_id: BOT_USER_ID, bot_username: 'matty', bot_display_name: 'Matty', target_post_id: TARGET_POST_ID},
            {type},
        );

        expect(screen.queryByRole('link', {name: /Click here to loop in/})).toBeNull();
        expect(mockedDoLoopInAgent).not.toHaveBeenCalled();
        expect(screen.getByText(POST_MESSAGE)).not.toBeNull();
    });
});
