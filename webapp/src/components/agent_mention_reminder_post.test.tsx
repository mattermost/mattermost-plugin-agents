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
                    id: overrides?.id ?? 'post_1',
                    message: overrides?.message ?? 'To respond to an agent you must @mention them.',
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

    test('renders the loop-in link with a capitalized "Click here" (MM-69160)', () => {
        renderPost({bot_username: 'matty', bot_display_name: 'Matty'});

        // The visible sentence and the capitalized link text.
        expect(screen.getByText(/To respond to an agent you must @mention them\./)).not.toBeNull();
        const link = screen.getByRole('link', {name: 'Click here to loop in @Matty'});
        expect(link).not.toBeNull();

        // Regression guard: the old lowercase copy must not appear.
        expect(screen.queryByText(/click here to loop in/)).toBeNull();
    });

    test('falls back to the bot username when no display name is set', () => {
        renderPost({bot_username: 'matty'});
        expect(screen.getByRole('link', {name: 'Click here to loop in @matty'})).not.toBeNull();
    });

    test('clicking the link loops in the agent and shows a confirmation', async () => {
        renderPost({bot_username: 'matty', bot_display_name: 'Matty', target_post_id: 'target_42'});

        fireEvent.click(screen.getByRole('link', {name: 'Click here to loop in @Matty'}));

        expect(mockedDoLoopInAgent).toHaveBeenCalledWith('target_42', 'matty');
        expect(await screen.findByText('Looped in @Matty.')).not.toBeNull();
    });

    test('renders the plain fallback message when no bot username is present', () => {
        renderPost({}, {message: 'To respond to an agent you must @mention them.'});
        expect(screen.getByText('To respond to an agent you must @mention them.')).not.toBeNull();
        expect(screen.queryByRole('link')).toBeNull();
    });
});
