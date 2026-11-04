// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import {ChannelContextPosts, unpinChannelContextPost} from '@/client';
import {channelContextPostsReducer, ChannelContextPostsState} from '@/channel_context';
import manifest from '@/manifest';

import {ChannelContextPostsSection} from './channel_context_posts_section';
import {setChannelInstructionsDraft} from './channel_instructions_state';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, unknown>}) => (
            Object.entries(values ?? {}).reduce((text, [key, value]) => text.replace(`{${key}}`, String(value)), defaultMessage)
        ),
    };
});

jest.mock('@/license', () => ({
    licenseAllows: jest.fn(() => true),
}));

jest.mock('@/client', () => ({
    getChannelContextPosts: jest.fn(),
    unpinChannelContextPost: jest.fn(),
}));

const mockedUnpin = unpinChannelContextPost as jest.MockedFunction<typeof unpinChannelContextPost>;
const CHANNEL_ID = 'chan1';

function pins(entries: Array<{id: string; userId?: string; message?: string}>, max = 10): ChannelContextPosts {
    return {
        posts: entries.map(({id, userId = 'alice-id', message = `message ${id}`}) => ({
            post_id: id, user_id: userId, root_id: '', message, create_at: 1, pinned_by: 'u', pinned_at: 1,
        })),
        max_posts: max,
    };
}

function renderSection(loaded: ChannelContextPosts | null | undefined) {
    const channelContext: ChannelContextPostsState = typeof loaded === 'undefined' ? {} : {[CHANNEL_ID]: loaded};
    const entities = {
        general: {config: {}, license: {}},
        users: {currentUserId: 'me', profiles: {'alice-id': {username: 'alice'}}},
        channels: {channels: {[CHANNEL_ID]: {id: CHANNEL_ID, type: 'O', team_id: 'team1'}}},
    };
    const store = createStore(combineReducers({
        entities: (state = entities) => state,
        ['plugins-' + manifest.id]: combineReducers({channelContextPosts: channelContextPostsReducer}),
    }), {['plugins-' + manifest.id]: {channelContextPosts: channelContext}} as never);

    render(
        <Provider store={store}>
            <ChannelContextPostsSection/>
        </Provider>,
    );
}

beforeEach(() => {
    setChannelInstructionsDraft({channelId: CHANNEL_ID, saved: '', saveError: null});
    mockedUnpin.mockReset();
});

describe('ChannelContextPostsSection', () => {
    test('lists pinned posts with their author and the pin count', () => {
        renderSection(pins([{id: 'p1', message: 'Deploys freeze on Fridays.'}, {id: 'p2', userId: 'unknown-id'}], 10));

        expect(screen.getByText('2 of 10 posts pinned. Agents working in this channel receive them with every request, as optional background they use only when it is relevant.')).not.toBeNull();
        const items = screen.getAllByTestId('channel-context-post');
        expect(items).toHaveLength(2);
        expect(items[0].textContent).toContain('@alice');
        expect(items[0].textContent).toContain('Deploys freeze on Fridays.');
        expect(items[1].textContent).not.toContain('@');
    });

    test('truncates long posts', () => {
        renderSection(pins([{id: 'p1', message: 'x'.repeat(500)}]));

        expect(screen.getByTestId('channel-context-post').textContent).toContain(`${'x'.repeat(160)}…`);
    });

    test('unpins immediately and shows the updated list', async () => {
        mockedUnpin.mockResolvedValue(pins([{id: 'p2'}]));
        renderSection(pins([{id: 'p1'}, {id: 'p2'}]));

        fireEvent.click(screen.getAllByLabelText('Unpin from agent context')[0]);

        await waitFor(() => expect(screen.getAllByTestId('channel-context-post')).toHaveLength(1));
        expect(mockedUnpin).toHaveBeenCalledWith(CHANNEL_ID, 'p1');
    });

    test('keeps the list and reports a failed unpin', async () => {
        mockedUnpin.mockRejectedValue(new Error('500'));
        renderSection(pins([{id: 'p1'}]));

        fireEvent.click(screen.getByLabelText('Unpin from agent context'));

        await waitFor(() => expect(screen.getByText('Failed to unpin the post. Please try again.')).not.toBeNull());
        expect(screen.getAllByTestId('channel-context-post')).toHaveLength(1);
    });

    test('shows an empty state', () => {
        renderSection(pins([]));

        expect(screen.getByText('No posts are pinned to agent context yet.')).not.toBeNull();
        expect(screen.queryAllByTestId('channel-context-post')).toHaveLength(0);
    });

    test('shows a load failure', () => {
        renderSection(null);

        expect(screen.getByText('Pinned agent context could not be loaded.')).not.toBeNull();
    });
});
