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

type PostProps = {
    bot_username?: string;
    bot_display_name?: string;
    target_post_id?: string;
};

function renderPost(props?: PostProps, overrides?: {id?: string; message?: string}) {
    return render(
        <IntlProvider locale='en'>
            <AgentMentionReminderPost
                post={{
                    id: overrides?.id ?? POST_ID,
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
});
