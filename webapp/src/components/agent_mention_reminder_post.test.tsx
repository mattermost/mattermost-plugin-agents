// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {doLoopInAgent} from '@/client';

import {AgentMentionReminderPost} from './agent_mention_reminder_post';

jest.mock('@/client', () => ({
    doLoopInAgent: jest.fn(),
}));

const mockedDoLoopInAgent = doLoopInAgent as jest.MockedFunction<typeof doLoopInAgent>;

// Mattermost IDs are 26 characters of lowercase letters and digits.
const POST_ID = 'ehz9k3wqr7t1a5m2xd8pnb4jsc';
const TARGET_POST_ID = 'kq3n7vd1x9r4bz2m8sw6t5jhpc';
const POST_MESSAGE = 'To respond to an agent you must @mention them.';

// The reminder is delivered as an ephemeral post, and the server stamps that
// type onto every ephemeral it publishes.
const EPHEMERAL_POST_TYPE = 'system_ephemeral';

type PostProps = {
    bot_username?: string;
    bot_display_name?: string;
    target_post_id?: string;
};

type PostOverrides = {id?: string; type?: string; message?: string};

function renderPost(props?: PostProps, overrides?: PostOverrides) {
    return render(
        <IntlProvider locale='en'>
            <AgentMentionReminderPost
                post={{
                    id: overrides?.id ?? POST_ID,
                    type: 'type' in (overrides ?? {}) ? overrides?.type : EPHEMERAL_POST_TYPE,
                    message: overrides?.message ?? POST_MESSAGE,
                    props,
                }}
            />
        </IntlProvider>,
    );
}

// Post props are decoded from JSON off the websocket, so any prop can hold any
// JSON value at runtime no matter what the component's Props type declares.
function renderPostWithRawProps(props: Record<string, unknown>) {
    return renderPost(props as PostProps);
}

describe('AgentMentionReminderPost', () => {
    beforeEach(() => {
        mockedDoLoopInAgent.mockReset();
        mockedDoLoopInAgent.mockImplementation(() => Promise.resolve());
    });

    test('falls back to the bot username when no display name is set', () => {
        renderPost({bot_username: 'matty'});
        expect(screen.getByRole('link', {name: 'Click here to loop in @matty'})).not.toBeNull();
    });

    test('clicking the link loops in the agent and shows a confirmation', async () => {
        renderPost({bot_username: 'matty', bot_display_name: 'Matty', target_post_id: TARGET_POST_ID});

        fireEvent.click(screen.getByRole('link', {name: 'Click here to loop in @Matty'}));

        expect(mockedDoLoopInAgent).toHaveBeenCalledWith(TARGET_POST_ID, 'matty');
        expect(await screen.findByText('Looped in @Matty.')).not.toBeNull();
    });

    test('falls back to the post id when no target post id is set', async () => {
        renderPost({bot_username: 'matty', bot_display_name: 'Matty'});

        fireEvent.click(screen.getByRole('link', {name: 'Click here to loop in @Matty'}));

        expect(mockedDoLoopInAgent).toHaveBeenCalledWith(POST_ID, 'matty');
        expect(await screen.findByText('Looped in @Matty.')).not.toBeNull();
    });

    test('renders the plain fallback message when no bot username is present', () => {
        renderPost({}, {message: POST_MESSAGE});
        expect(screen.getByText(POST_MESSAGE)).not.toBeNull();
        expect(screen.queryByRole('link')).toBeNull();
    });

    const notWellFormedTargetPostIds: Array<{name: string; targetPostId: string}> = [
        {name: 'relative path segments', targetPostId: '../../some/other/route'},
        {name: 'percent-encoded separators', targetPostId: '..%2f..%2fsome%2froute'},
        {name: 'right length but contains a separator', targetPostId: 'abcdefghijklmnopqrstuvwxy/'},
        {name: 'right length but contains query and fragment markers', targetPostId: 'abcdefghijklmnopqrstuvw?x#'},
        {name: 'too short', targetPostId: 'abc'},
        {name: 'too long', targetPostId: 'abcdefghijklmnopqrstuvwxyz7'},
        {name: 'right length but uppercase', targetPostId: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'},
        {name: 'right length but not ascii', targetPostId: 'abcdefghijklmnopqrstuvwxy\u00e9'},
        {name: 'empty', targetPostId: ''},
        {name: 'well-formed id with leading whitespace', targetPostId: ` ${TARGET_POST_ID}`},
    ];

    test.each(notWellFormedTargetPostIds)('renders a plain hint when target_post_id is not well-formed: $name', ({targetPostId}) => {
        renderPost({bot_username: 'matty', bot_display_name: 'Matty', target_post_id: targetPostId});

        // Clicking is a no-op when the affordance is absent; it keeps the
        // assertion honest whichever way the component withholds the action.
        const link = screen.queryByRole('link', {name: /Click here to loop in/});
        if (link) {
            fireEvent.click(link);
        }

        expect(mockedDoLoopInAgent).not.toHaveBeenCalled();
        expect(link).toBeNull();
        expect(screen.getByText(POST_MESSAGE)).not.toBeNull();
    });

    const notStrings: Array<{name: string; value: unknown}> = [
        {name: 'a number', value: 42},
        {name: 'a boolean', value: true},
        {name: 'an object', value: {}},
        {name: 'an array', value: ['matty']},
    ];

    test.each(notStrings)('renders when bot_display_name is $name', ({value}) => {
        expect(() => renderPostWithRawProps({
            bot_username: 'matty',
            bot_display_name: value,
            target_post_id: TARGET_POST_ID,
        })).not.toThrow();
    });

    test.each(notStrings)('renders when bot_username is $name', ({value}) => {
        expect(() => renderPostWithRawProps({
            bot_username: value,
            target_post_id: TARGET_POST_ID,
        })).not.toThrow();
    });

    test.each(notStrings)('renders when target_post_id is $name', ({value}) => {
        expect(() => renderPostWithRawProps({
            bot_username: 'matty',
            bot_display_name: 'Matty',
            target_post_id: value,
        })).not.toThrow();
    });

    // Only the server can stamp the ephemeral type onto a post; every other
    // field this component reads can be set by whoever authored the post.
    test.each([
        {name: 'no type', type: undefined}, // eslint-disable-line no-undefined
        {name: 'an empty type', type: ''},
        {name: 'the custom post type from the props', type: 'custom_agent_mention_reminder'},
        {name: 'an unrelated custom type', type: 'custom_llmbot'},
    ])('offers no action on a post that is not ephemeral: $name', ({type}) => {
        renderPost(
            {bot_username: 'matty', bot_display_name: 'Matty', target_post_id: TARGET_POST_ID},
            {type},
        );

        const link = screen.queryByRole('link', {name: /Click here to loop in/});
        if (link) {
            fireEvent.click(link);
        }

        expect(link).toBeNull();
        expect(mockedDoLoopInAgent).not.toHaveBeenCalled();
        expect(screen.getByText(POST_MESSAGE)).not.toBeNull();
    });
});
