// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';
import {fireEvent, render, screen} from '@testing-library/react';

import {agentDMRawView} from '@/agent_dm_view';
import manifest from '@/manifest';

import AgentDMViewToggle from './agent_dm_view_toggle';

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => children,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {...actual, useIntl: () => ({formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage})};
});

// The plugin's real raw-view reducer, so a click is seen through the state it changes.
const makeStore = (currentChannelId: string, raw: Record<string, true>) => createStore(combineReducers({
    [`plugins-${manifest.id}`]: combineReducers({
        bots: (state = [{id: 'agent1'}]) => state,
        agentDMRawView,
    }),
    entities: (state = {
        users: {currentUserId: 'me'},
        channels: {
            currentChannelId,
            channels: {
                dmAgent: {id: 'dmAgent', type: 'D', name: 'agent1__me'},
                dmHuman: {id: 'dmHuman', type: 'D', name: 'human__me'},
            },
        },
    }) => state,
} as any), {[`plugins-${manifest.id}`]: {bots: [{id: 'agent1'}], agentDMRawView: raw}} as any);

const button = () => screen.queryByRole('button', {name: 'View as agent chat'});

describe('AgentDMViewToggle', () => {
    test.each([
        ['an agent DM shown as messages', 'dmAgent', {dmAgent: true}, true],
        ['an agent DM shown as the agent chat', 'dmAgent', {}, false],
        ['a DM with a person', 'dmHuman', {dmHuman: true}, false],
        ['another DM shown as messages', 'dmAgent', {dmHuman: true}, false],
    ])('%s', (_name, currentChannelId, raw, shown) => {
        render(<Provider store={makeStore(currentChannelId, raw as Record<string, true>)}><AgentDMViewToggle/></Provider>);
        expect(Boolean(button())).toBe(shown);
    });

    test('switches the DM back to the agent chat', () => {
        const store = makeStore('dmAgent', {dmAgent: true});
        render(<Provider store={store}><AgentDMViewToggle/></Provider>);

        fireEvent.click(button()!);

        expect((store.getState() as any)[`plugins-${manifest.id}`].agentDMRawView).toEqual({});
        expect(button()).toBeNull();
    });
});
