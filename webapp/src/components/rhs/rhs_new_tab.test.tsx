// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider, useSelector} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen} from '@testing-library/react';

import {BotsHandler} from '@/redux';
import manifest from '@/manifest';

import RHSNewTab from './rhs_new_tab';

const mockGetBotDirectChannel = jest.fn();
const mockGetChannel = jest.fn();
const mockGetChannelMember = jest.fn();
const mockGetRolesByNames = jest.fn();
const mockCreatePost = jest.fn();

jest.mock('@/client', () => ({
    getBotDirectChannel: (...args: unknown[]) => mockGetBotDirectChannel(...args),
    createPost: (...args: unknown[]) => mockCreatePost(...args),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    Client4: {
        getChannel: (...args: unknown[]) => mockGetChannel(...args),
        getChannelMember: (...args: unknown[]) => mockGetChannelMember(...args),
        getRolesByNames: (...args: unknown[]) => mockGetRolesByNames(...args),
    },
}));

// '@/redux' pulls in host components through its hooks; none are rendered here.
jest.mock('@/mm_webapp', () => ({}));

const mockRequestRefresh = jest.fn();
jest.mock('@/websocket', () => ({
    requestAgentThreadsRefresh: () => mockRequestRefresh(),
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
const Harness = ({botId, selectPost, setCurrentTab}: {botId: string; selectPost: jest.Mock; setCurrentTab: jest.Mock}) => {
    const bot = useSelector((state: any) => state[pluginKey].bots.find((b: Bot) => b.id === botId) ?? null);
    return (
        <RHSNewTab
            activeBot={bot}
            selectPost={selectPost}
            setCurrentTab={setCurrentTab}
        />
    );
};

const renderTab = (store: ReturnType<typeof makeStore>, botId = 'bot1') => {
    const selectPost = jest.fn();
    const setCurrentTab = jest.fn();
    const utils = render(
        <Provider store={store}>
            <Harness
                botId={botId}
                selectPost={selectPost}
                setCurrentTab={setCurrentTab}
            />
        </Provider>,
    );
    const rerenderBot = (nextBotId: string) => utils.rerender(
        <Provider store={store}>
            <Harness
                botId={nextBotId}
                selectPost={selectPost}
                setCurrentTab={setCurrentTab}
            />
        </Provider>,
    );
    return {selectPost, setCurrentTab, rerenderBot};
};

const deferred = <T, >() => {
    let resolve!: (v: T) => void;
    const promise = new Promise<T>((res) => {
        resolve = res;
    });
    return {promise, resolve};
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
        expect(screen.getByText('Setting up chat channel...')).toBeTruthy();
        expect(screen.queryByRole('textbox')).toBeNull();

        expect(await screen.findByRole('textbox', {name: 'Message Agent bot1'})).toBeTruthy();
        expect(mockGetBotDirectChannel).toHaveBeenCalledWith('me', 'bot1');
        const state = store.getState() as any;
        expect(state[pluginKey].bots[0].dmChannelID).toBe('dm1');
        expect(state.entities.channels.channels.dm1).toBeTruthy();
        expect(state.entities.channels.myMembers.dm1).toBeTruthy();

        // The empty state has no pinned-prompt buttons (prompts are in the composer's "+" menu).
        expect(screen.queryByRole('button', {name: /prompt/i})).toBeNull();
    });

    test.each([
        {name: 'the DM channel cannot be created', setup: () => mockGetBotDirectChannel.mockRejectedValue(new Error('nope')), bot: makeBot('bot1', ''), loaded: []},
        {name: 'the DM channel cannot be loaded', setup: () => mockGetChannel.mockRejectedValue(new Error('nope')), bot: makeBot('bot1', 'dm1'), loaded: []},
    ])('shows an error and no composer when $name', async ({setup, bot, loaded}) => {
        setup();
        renderTab(makeStore([bot], loaded));
        expect(await screen.findByText('Failed to create chat channel. Please try again later.')).toBeTruthy();
        expect(screen.queryByRole('textbox')).toBeNull();
    });

    test('the first message creates a root post in the DM and opens its thread', async () => {
        mockCreatePost.mockResolvedValue({id: 'new-root'});
        const {selectPost, setCurrentTab} = renderTab(makeStore([makeBot('bot1', 'dm1')], ['dm1']));

        const composer = await screen.findByRole('textbox', {name: 'Message Agent bot1'});
        fireEvent.change(composer, {target: {value: 'hello'}});
        await act(async () => {
            fireEvent.keyDown(composer, {key: 'Enter'});
        });

        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({channel_id: 'dm1', root_id: '', message: 'hello'}));
        expect(selectPost).toHaveBeenCalledWith('new-root');
        expect(setCurrentTab).toHaveBeenCalledWith('thread');
        expect(mockRequestRefresh).toHaveBeenCalledTimes(1);
    });

    test('a failed post shows an inline error and does not open a thread', async () => {
        mockCreatePost.mockRejectedValue(new Error('500'));
        const {selectPost, setCurrentTab} = renderTab(makeStore([makeBot('bot1', 'dm1')], ['dm1']));

        const composer = await screen.findByRole('textbox', {name: 'Message Agent bot1'});
        fireEvent.change(composer, {target: {value: 'hello'}});
        await act(async () => {
            fireEvent.keyDown(composer, {key: 'Enter'});
        });

        expect(screen.getByRole('alert').textContent).toBe('Couldn\'t send your message. Try again.');
        expect(selectPost).not.toHaveBeenCalled();
        expect(setCurrentTab).not.toHaveBeenCalled();
        expect(mockRequestRefresh).not.toHaveBeenCalled();
    });

    test('repeated submits while posting create one chat', async () => {
        const pending = deferred<{id: string}>();
        mockCreatePost.mockReturnValue(pending.promise);
        const {selectPost} = renderTab(makeStore([makeBot('bot1', 'dm1')], ['dm1']));

        const composer = await screen.findByRole('textbox', {name: 'Message Agent bot1'});
        fireEvent.change(composer, {target: {value: 'hello'}});
        fireEvent.keyDown(composer, {key: 'Enter'});
        fireEvent.keyDown(composer, {key: 'Enter'});
        fireEvent.click(screen.getByRole('button', {name: 'Send message'}));
        expect(mockCreatePost).toHaveBeenCalledTimes(1);

        await act(async () => {
            pending.resolve({id: 'new-root'});
        });
        expect(selectPost).toHaveBeenCalledTimes(1);
    });

    test('switching the active bot posts to the new bot\'s DM', async () => {
        mockCreatePost.mockResolvedValue({id: 'r2'});
        const store = makeStore([makeBot('bot1', 'dm1'), makeBot('bot2', 'dm2')], ['dm1', 'dm2']);
        const {rerenderBot} = renderTab(store);
        await screen.findByRole('textbox', {name: 'Message Agent bot1'});

        rerenderBot('bot2');
        const composer = await screen.findByRole('textbox', {name: 'Message Agent bot2'});
        fireEvent.change(composer, {target: {value: 'hi'}});
        await act(async () => {
            fireEvent.keyDown(composer, {key: 'Enter'});
        });
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({channel_id: 'dm2'}));
    });
});
