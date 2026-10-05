// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {Post} from '@mattermost/types/posts';

import {ChannelContextPosts} from '@/client';
import {channelContextPostsReducer, ChannelContextPostsState} from '@/channel_context';
import manifest from '@/manifest';
import {PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES} from '@/utils/permissions';

import PostMenu from './post_menu';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, unknown>}) => (
            Object.entries(values ?? {}).reduce((text, [key, value]) => text.replace(`{${key}}`, String(value)), defaultMessage)
        ),
    };
});

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
    licenseAllows: jest.fn(() => true),
}));

jest.mock('@/hooks', () => ({
    useSelectPost: () => jest.fn(),
}));

jest.mock('@/bots', () => ({
    useBotlistForChannel: () => ({
        bots: [{username: 'matty', displayName: 'Matty'}],
        activeBot: {username: 'matty', displayName: 'Matty'},
        setActiveBot: jest.fn(),
    }),
}));

jest.mock('../client', () => ({
    doReaction: jest.fn(),
    doThreadAnalysis: jest.fn(),
    getChannelContextPosts: jest.fn(),
    pinChannelContextPost: jest.fn(),
    unpinChannelContextPost: jest.fn(),
}));

jest.mock('./dot_menu', () => ({
    __esModule: true,
    default: ({children}: {children: React.ReactNode}) => <div data-testid='ai-actions-menu'>{children}</div>,
    DropdownMenu: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
    DropdownMenuItem: ({children, onClick}: {children: React.ReactNode; onClick?: () => void}) => (
        <button
            type='button'
            onClick={onClick}
        >
            {children}
        </button>
    ),
    DisabledDropdownMenuItemStyled: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}));

jest.mock('./bot_selector', () => ({
    DropdownBotSelector: () => <div>{'bot-selector'}</div>,
}));

const CHANNEL_ID = 'chan1';
const post = {id: 'post1', channel_id: CHANNEL_ID} as Post;

const {useIsLicensedFor, licenseAllows} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock; licenseAllows: jest.Mock};
const client = jest.requireMock('../client') as {
    getChannelContextPosts: jest.Mock;
    pinChannelContextPost: jest.Mock;
    unpinChannelContextPost: jest.Mock;
};

function contextPosts(postIds: string[], max = 10): ChannelContextPosts {
    return {
        posts: postIds.map((id) => ({post_id: id, user_id: 'u', root_id: '', message: 'm', create_at: 1, pinned_by: 'u', pinned_at: 1})),
        max_posts: max,
    };
}

function renderMenu(opts: {channelType?: string; canManage?: boolean; pins?: ChannelContextPosts | null; menuPost?: Post} = {}) {
    const channelContext: ChannelContextPostsState = typeof opts.pins === 'undefined' ? {} : {[CHANNEL_ID]: opts.pins};
    const entities = {
        general: {config: {}, license: {}},
        users: {currentUserId: 'me', profiles: {me: {roles: 'system_user'}}},
        teams: {myMembers: {team1: {roles: 'team_user'}}},
        channels: {
            channels: {[CHANNEL_ID]: {id: CHANNEL_ID, type: opts.channelType ?? 'O', team_id: 'team1'}},
            roles: {[CHANNEL_ID]: new Set(['channel_role'])},
        },
        roles: {
            roles: {
                system_user: {permissions: []},
                team_user: {permissions: []},
                channel_role: {permissions: opts.canManage === false ? [] : [PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES]},
            },
        },
    };
    const store = createStore(combineReducers({
        entities: (state = entities) => state,
        ['plugins-' + manifest.id]: combineReducers({channelContextPosts: channelContextPostsReducer}),
    }), {['plugins-' + manifest.id]: {channelContextPosts: channelContext}} as never);

    render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                <PostMenu post={opts.menuPost ?? post}/>
            </IntlProvider>
        </Provider>,
    );
    return store;
}

beforeEach(() => {
    useIsLicensedFor.mockReturnValue(true);
    licenseAllows.mockReturnValue(true);
    client.getChannelContextPosts.mockReset();
    client.getChannelContextPosts.mockResolvedValue(contextPosts([]));
    client.pinChannelContextPost.mockReset();
    client.unpinChannelContextPost.mockReset();
});

describe('PostMenu license gating', () => {
    test('shows thread summarization actions at Professional', () => {
        renderMenu({pins: contextPosts([])});

        expect(screen.getByText('Summarize Thread')).not.toBeNull();
        expect(screen.getByText('Find action items')).not.toBeNull();
        expect(screen.getByText('Find open questions')).not.toBeNull();
        expect(screen.getByText('React for me')).not.toBeNull();
    });

    test('hides thread summarization actions below Professional and keeps React for me', () => {
        useIsLicensedFor.mockReturnValue(false);

        renderMenu({pins: contextPosts([])});

        expect(screen.queryByText('Summarize Thread')).toBeNull();
        expect(screen.queryByText('Find action items')).toBeNull();
        expect(screen.queryByText('Find open questions')).toBeNull();
        expect(screen.getByText('React for me')).not.toBeNull();
    });
});

describe('PostMenu agent context pinning', () => {
    test('offers to pin an unpinned post and stores the updated pins', async () => {
        client.pinChannelContextPost.mockResolvedValue(contextPosts(['post1']));
        renderMenu({pins: contextPosts([])});

        fireEvent.click(screen.getByText('Pin to agent context'));

        await waitFor(() => expect(screen.getByText('Unpin from agent context')).not.toBeNull());
        expect(client.pinChannelContextPost).toHaveBeenCalledWith(CHANNEL_ID, 'post1');
    });

    test('offers to unpin a pinned post', async () => {
        client.unpinChannelContextPost.mockResolvedValue(contextPosts([]));
        renderMenu({pins: contextPosts(['post1'])});

        fireEvent.click(screen.getByText('Unpin from agent context'));

        await waitFor(() => expect(screen.getByText('Pin to agent context')).not.toBeNull());
        expect(client.unpinChannelContextPost).toHaveBeenCalledWith(CHANNEL_ID, 'post1');
    });

    test('shows a disabled notice instead of pin when the channel is full', () => {
        renderMenu({pins: contextPosts(['a', 'b'], 2)});

        expect(screen.queryByText('Pin to agent context')).toBeNull();
        expect(screen.getByText('Agent context is full (2 posts)')).not.toBeNull();
    });

    test('a pinned post can still be unpinned when the channel is full', () => {
        renderMenu({pins: contextPosts(['post1', 'b'], 2)});

        expect(screen.getByText('Unpin from agent context')).not.toBeNull();
    });

    test('re-syncs from the server when a pin fails', async () => {
        client.pinChannelContextPost.mockRejectedValue(new Error('conflict'));
        client.getChannelContextPosts.mockResolvedValue(contextPosts(['x', 'y'], 2));
        renderMenu({pins: contextPosts([])});

        fireEvent.click(screen.getByText('Pin to agent context'));

        await waitFor(() => expect(screen.getByText('Agent context is full (2 posts)')).not.toBeNull());
    });

    test('fetches the channel pins when they are not loaded yet', async () => {
        client.getChannelContextPosts.mockResolvedValue(contextPosts(['post1']));
        renderMenu();

        await waitFor(() => expect(screen.getByText('Unpin from agent context')).not.toBeNull());
        expect(client.getChannelContextPosts).toHaveBeenCalledWith(CHANNEL_ID);
    });

    test.each([
        {name: 'without the manage permission', opts: {canManage: false}},
        {name: 'in a direct message', opts: {channelType: 'D'}},
        {name: 'for a system message', opts: {menuPost: {...post, type: 'system_join_channel'} as Post}},
    ])('hides the pin action $name', ({opts}) => {
        renderMenu({pins: contextPosts([]), ...opts});

        expect(screen.queryByText('Pin to agent context')).toBeNull();
        expect(screen.queryByText('Unpin from agent context')).toBeNull();
    });

    test('hides the pin action below Enterprise Advanced', () => {
        licenseAllows.mockReturnValue(false);
        renderMenu({pins: contextPosts([])});

        expect(screen.queryByText('Pin to agent context')).toBeNull();
        expect(client.getChannelContextPosts).not.toHaveBeenCalled();
    });
});
