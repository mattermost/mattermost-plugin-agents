// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render} from '@testing-library/react';
import {Provider} from 'react-redux';
import {IntlProvider} from 'react-intl';

import manifest from '@/manifest';

import PostText from '../post_text';

const makeStore = (pluginState: Record<string, unknown>) => {
    const state = {
        entities: {
            general: {config: {SiteURL: 'http://localhost:8065'}},
            teams: {teams: {}},
            channels: {channels: {chan1: {id: 'chan1', team_id: 'team1', type: 'D'}}},
        },
        [`plugins-${manifest.id}`]: pluginState,
    };
    return {getState: () => state, subscribe: () => jest.fn(), dispatch: jest.fn()} as any;
};

export const installPostUtils = () => {
    (window as any).PostUtils = {
        formatText: (text: string) => text,
        messageHtmlToComponent: (html: string) => <span>{html}</span>,
    };
};

/** Renders an agent post through PostText, the production entry point for generative UI. */
export const renderAgentPost = (message: string, {
    pluginState = {generativeUIEnabled: true},
    allowGenerativeUI = true,
    streaming = false,
}: {pluginState?: Record<string, unknown>; allowGenerativeUI?: boolean; streaming?: boolean} = {}) => render(
    <Provider store={makeStore(pluginState)}>
        <IntlProvider locale='en'>
            <PostText
                message={message}
                channelID='chan1'
                postID='post1'
                allowGenerativeUI={allowGenerativeUI}
                showCursor={streaming}
            />
        </IntlProvider>
    </Provider>,
);

export const openUIMessage = (program: string, {closed = true} = {}) => `Intro.\n\n\`\`\`openui-lang\n${program}${closed ? '\n```' : ''}`;
