// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider, useSelector} from 'react-redux';
import {createStore} from 'redux';
import {act, render, screen} from '@testing-library/react';

import {BotsHandler} from '@/redux';
import manifest from '@/manifest';

import {lastAdvancedTextEditorProps} from '../../../tests/fake_advanced_text_editor';

import RHSNewTab from './rhs_new_tab';

const mockGetBotDirectChannel = jest.fn();
const mockGetChannel = jest.fn();
const mockGetChannelMember = jest.fn();
const mockGetRolesByNames = jest.fn();

jest.mock('@/client', () => ({
    getBotDirectChannel: (...args: unknown[]) => mockGetBotDirectChannel(...args),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    Client4: {
        getChannel: (...args: unknown[]) => mockGetChannel(...args),
        getChannelMember: (...args: unknown[]) => mockGetChannelMember(...args),
        getRolesByNames: (...args: unknown[]) => mockGetRolesByNames(...args),
    },
}));

jest.mock('@/mm_webapp', () => ({
    AdvancedTextEditor: jest.requireActual('../../../tests/fake_advanced_text_editor').FakeAdvancedTextEditor,
}));

const mockRequestRefresh = jest.fn();
jest.mock('@/hooks/use_agent_threads', () => ({
    useRefreshAgentThreads: () => mockRequestRefresh,
}));
jest.mock('@/websocket', () => ({
    subscribeToAllPostUpdates: () => () => null,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

const pluginKey = `plugins-${manifest.id}`;

const makeBot = (id: string, dmChannelID: string) => ({
    id,
    displayName: `Agent ${id}`,
    username: id,
    lastIconUpdate: 0,
    dmChannelID,
    channelAccessLevel: 0,
    channelIDs: [],
    userAccessLevel: 0,
    userIDs: [],
    teamIDs: [],
    enabledMCPTools: [],
    autoEnableNewMCPTools: false,
});
type Bot = ReturnType<typeof makeBot>;

const makeStore = (bots: Bot[], loadedChannels: string[] = []) => {
    const initial = {
        [pluginKey]: {bots},
        entities: {
            users: {currentUserId: 'me'},
            channels: {
                channels: Object.fromEntries(loadedChannels.map((c) => [c, {id: c}])),
                myMembers: Object.fromEntries(loadedChannels.map((c) => [c, {channel_id: c, roles: 'channel_user'}])),
            },
            roles: {roles: {channel_user: {name: 'channel_user'}}},
        },
    };
    const reducer = (state: any = initial, action: any) => {
        switch (action.type) {
        case BotsHandler:
            return {...state, [pluginKey]: {...state[pluginKey], bots: action.bots}};
        case 'RECEIVED_CHANNEL':
            return {...state, entities: {...state.entities, channels: {...state.entities.channels, channels: {...state.entities.channels.channels, [action.data.id]: action.data}}}};
        case 'RECEIVED_MY_CHANNEL_MEMBER':
            return {...state, entities: {...state.entities, channels: {...state.entities.channels, myMembers: {...state.entities.channels.myMembers, [action.data.channel_id]: action.data}}}};
        default:
            return state;
        }
    };
    return createStore(reducer);
};

// The tab is handed the active bot from the (Redux-backed) bot list, as rhs.tsx does.
const Harness = ({botId, selectPost}: {botId: string; selectPost: jest.Mock}) => {
    const bot = useSelector((state: any) => state[pluginKey].bots.find((b: Bot) => b.id === botId) ?? null);
    return (
        <RHSNewTab
            activeBot={bot}
            selectPost={selectPost}
        />
    );
};

const renderTab = (store: ReturnType<typeof makeStore>, botId = 'bot1') => {
    const selectPost = jest.fn();
    const utils = render(
        <Provider store={store}>
            <Harness
                botId={botId}
                selectPost={selectPost}
            />
        </Provider>,
    );
    const rerenderBot = (nextBotId: string) => utils.rerender(
        <Provider store={store}>
            <Harness
                botId={nextBotId}
                selectPost={selectPost}
            />
        </Provider>,
    );
    return {selectPost, rerenderBot};
};

describe('RHSNewTab', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        jest.spyOn(console, 'error').mockImplementation(() => null);
        mockGetRolesByNames.mockResolvedValue([]);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('creates a missing DM channel, stores it on the bot and loads it before offering the composer', async () => {
        const store = makeStore([makeBot('bot1', '')]);
        mockGetBotDirectChannel.mockResolvedValue('dm1');
        mockGetChannel.mockResolvedValue({id: 'dm1'});
        mockGetChannelMember.mockResolvedValue({channel_id: 'dm1', roles: 'channel_user'});

        renderTab(store);
        expect(screen.getByText('Setting up chat…')).toBeTruthy();
        expect(screen.queryByTestId('host-advanced-text-editor')).toBeNull();

        expect(await screen.findByTestId('host-advanced-text-editor')).toBeTruthy();
        expect(lastAdvancedTextEditorProps()).toEqual(expect.objectContaining({channelId: 'dm1', rootId: '', placeholder: 'Message Agent bot1'}));
        expect(mockGetBotDirectChannel).toHaveBeenCalledWith('me', 'bot1');
        const state = store.getState() as any;
        expect(state[pluginKey].bots[0].dmChannelID).toBe('dm1');
        expect(state.entities.channels.channels.dm1).toBeTruthy();
        expect(state.entities.channels.myMembers.dm1).toBeTruthy();
    });

    test.each([
        {name: 'the DM channel cannot be created', setup: () => mockGetBotDirectChannel.mockRejectedValue(new Error('nope')), bot: makeBot('bot1', ''), loaded: []},
        {name: 'the DM channel cannot be loaded', setup: () => mockGetChannel.mockRejectedValue(new Error('nope')), bot: makeBot('bot1', 'dm1'), loaded: []},
    ])('shows an error and no composer when $name', async ({setup, bot, loaded}) => {
        setup();
        renderTab(makeStore([bot], loaded));
        expect(await screen.findByText('Failed to create chat channel. Please try again later.')).toBeTruthy();
        expect(screen.queryByTestId('host-advanced-text-editor')).toBeNull();
    });

    test.each([
        {name: 'a message opens the new thread', result: {created: {id: 'new-root'}}, opened: true},
        {name: 'a slash command (no post) stays on the new chat', result: {}, opened: false},
    ])('after the host composer submits, $name', async ({result, opened}) => {
        const {selectPost} = renderTab(makeStore([makeBot('bot1', 'dm1')], ['dm1']));
        await screen.findByTestId('host-advanced-text-editor');

        act(() => {
            lastAdvancedTextEditorProps().afterSubmit?.(result);
        });

        if (opened) {
            expect(selectPost).toHaveBeenCalledWith('new-root');
            expect(mockRequestRefresh).toHaveBeenCalledTimes(1);
        } else {
            expect(selectPost).not.toHaveBeenCalled();
            expect(mockRequestRefresh).not.toHaveBeenCalled();
        }
    });

    test('switching the active bot posts to the new bot\'s DM', async () => {
        const store = makeStore([makeBot('bot1', 'dm1'), makeBot('bot2', 'dm2')], ['dm1', 'dm2']);
        const {rerenderBot} = renderTab(store);
        await screen.findByText('Message Agent bot1');

        rerenderBot('bot2');
        await screen.findByText('Message Agent bot2');
        expect(lastAdvancedTextEditorProps()).toEqual(expect.objectContaining({channelId: 'dm2', rootId: ''}));
    });
});
