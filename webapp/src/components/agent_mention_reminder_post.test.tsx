// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {doLoopInAgent} from '@/client';

import {AgentMentionReminderPost} from './agent_mention_reminder_post';

jest.mock('@/client', () => ({
    doLoopInAgent: jest.fn(),
}));

const mockedDoLoopInAgent = doLoopInAgent as jest.MockedFunction<typeof doLoopInAgent>;

function makePost(overrides: Partial<React.ComponentProps<typeof AgentMentionReminderPost>['post']> = {}) {
    return {
        id: 'post_1',
        message: 'To respond to an agent you must @mention them.',
        props: {
            bot_username: 'matty',
            bot_display_name: 'Matty',
            target_post_id: 'target_1',
        },
        ...overrides,
    };
}

function renderPost(post = makePost()) {
    return render(
        <IntlProvider locale='en'>
            <AgentMentionReminderPost post={post}/>
        </IntlProvider>,
    );
}

describe('AgentMentionReminderPost', () => {
    beforeEach(() => {
        mockedDoLoopInAgent.mockReset();
    });

    test('renders the loop-in link with a capitalized "Click here"', () => {
        renderPost();

        const link = screen.getByRole('link');
        expect(link.textContent).toBe('Click here to loop in @Matty');
    });

    test('loops in the agent and shows the confirmation on click', async () => {
        mockedDoLoopInAgent.mockImplementationOnce(() => Promise.resolve());
        renderPost();

        fireEvent.click(screen.getByRole('link'));

        expect(mockedDoLoopInAgent).toHaveBeenCalledWith('target_1', 'matty');
        await waitFor(() => expect(screen.getByText('Looped in @Matty.')).not.toBeNull());
    });

    test('shows an error message when loop-in fails', async () => {
        jest.spyOn(console, 'error').mockImplementation(jest.fn());
        mockedDoLoopInAgent.mockRejectedValueOnce(new Error('boom'));
        renderPost();

        fireEvent.click(screen.getByRole('link'));

        await waitFor(() => expect(screen.getByText('Failed to loop in @Matty. Please try again.')).not.toBeNull());
    });

    test('falls back to the plain message when no bot username is present', () => {
        renderPost(makePost({props: {bot_display_name: 'Matty', target_post_id: 'target_1'}}));

        expect(screen.queryByRole('link')).toBeNull();
        expect(screen.getByText('To respond to an agent you must @mention them.')).not.toBeNull();
    });
});
