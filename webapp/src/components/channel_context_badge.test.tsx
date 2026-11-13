// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';
import {render, screen} from '@testing-library/react';

import {Post} from '@mattermost/types/posts';

import {ChannelContextPosts} from '@/client';
import {channelContextPostsReducer} from '@/channel_context';
import manifest from '@/manifest';

import ChannelContextBadge from './channel_context_badge';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('@/license', () => ({
    licenseAllows: jest.fn(() => true),
}));

jest.mock('@/client', () => ({
    getChannelContextPosts: jest.fn(() => new Promise(jest.fn())),
}));

const CHANNEL_ID = 'chan1';
const pinned: ChannelContextPosts = {
    posts: [{post_id: 'pinned', user_id: 'u', root_id: '', message: 'm', create_at: 1, pinned_by: 'u', pinned_at: 1}],
    max_posts: 10,
};

function renderBadge(postId: string, loaded: ChannelContextPosts | null) {
    const entities = {
        general: {config: {}, license: {}},
        channels: {channels: {[CHANNEL_ID]: {id: CHANNEL_ID, type: 'O', team_id: 'team1'}}},
    };
    const store = createStore(combineReducers({
        entities: (state = entities) => state,
        ['plugins-' + manifest.id]: combineReducers({channelContextPosts: channelContextPostsReducer}),
    }), {['plugins-' + manifest.id]: {channelContextPosts: loaded ? {[CHANNEL_ID]: loaded} : {}}} as never);

    render(
        <Provider store={store}>
            <ChannelContextBadge post={{id: postId, channel_id: CHANNEL_ID} as Post}/>
        </Provider>,
    );
}

describe('ChannelContextBadge', () => {
    test.each([
        {name: 'a pinned post', postId: 'pinned', loaded: pinned, want: true},
        {name: 'an unpinned post', postId: 'other', loaded: pinned, want: false},
        {name: 'a post whose channel pins are still loading', postId: 'pinned', loaded: null, want: false},
    ])('labels $name -> $want', ({postId, loaded, want}) => {
        renderBadge(postId, loaded);

        expect(screen.queryByTestId('channel-context-badge') !== null).toBe(want);
        if (want) {
            expect(screen.getByText('Agent context')).not.toBeNull();
        }
    });
});
