// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import {BotsHandler} from '@/redux';
import manifest from '@/manifest';

import AgentChatPage, {emptyChatTitles, newChatStorageKey} from './agent_chat_page';

const mockGetBotDirectChannel = jest.fn();
const mockGetPost = jest.fn();
const mockUpdateRead = jest.fn();
const mockGetChannel = jest.fn();
const mockGetChannelMember = jest.fn();
const mockGetRolesByNames = jest.fn();

jest.mock('@/client', () => ({
    getBotDirectChannel: (...args: unknown[]) => mockGetBotDirectChannel(...args),
    getPost: (...args: unknown[]) => mockGetPost(...args),
    updateRead: (...args: unknown[]) => mockUpdateRead(...args),
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    getAIBots: jest.fn(),
    savePreferences: jest.fn(),
    Client4: {
        getChannel: (...args: unknown[]) => mockGetChannel(...args),
        getChannelMember: (...args: unknown[]) => mockGetChannelMember(...args),
        getRolesByNames: (...args: unknown[]) => mockGetRolesByNames(...args),
    },
}));

const mockRequestRefresh = jest.fn();
jest.mock('@/websocket', () => ({
    requestAgentThreadsRefresh: () => mockRequestRefresh(),
}));

const mockEditorProps = jest.fn();
const mockThreadViewerProps = jest.fn();
jest.mock('@/mm_webapp', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');
    return {
        AdvancedTextEditor: (props: {afterSubmit: (r: unknown) => void}) => {
            mockEditorProps(props);
            return ReactActual.createElement('div', null,
                ReactActual.createElement('textarea', {'aria-label': 'composer'}),
                ReactActual.createElement('button', {onClick: () => props.afterSubmit({created: {id: 'new-root'}})}, 'send'),
            );
        },
        ThreadViewer: (props: Record<string, unknown>) => {
            mockThreadViewerProps(props);
            return ReactActual.createElement('div', {'data-testid': 'thread-viewer'});
        },
    };
});

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
    enabledMCPTools: [],
    autoEnableNewMCPTools: false,
});

interface StateOptions {
    bots: ReturnType<typeof makeBot>[];
    loadedChannels?: string[];
    currentTeamId?: string;
    teamMembers?: string[];
    posts?: Record<string, {id: string; channel_id: string; root_id: string}>;
}

// Minimal reducer covering only the slices and host action types the page touches.
const makeStore = ({bots, loadedChannels = [], currentTeamId = 'team1', teamMembers = [], posts = {}}: StateOptions) => {
    const initial = {
        [pluginKey]: {bots},
        entities: {
            users: {currentUserId: 'me'},
            teams: {currentTeamId, myMembers: Object.fromEntries(teamMembers.map((t) => [t, {team_id: t}]))},
            channels: {
                channels: Object.fromEntries(loadedChannels.map((c) => [c, {id: c}])),
                myMembers: Object.fromEntries(loadedChannels.map((c) => [c, {channel_id: c, roles: 'channel_user'}])),
            },
            roles: {roles: {channel_user: {name: 'channel_user'}}},
            posts: {posts},
            preferences: {myPreferences: {}},
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
        case 'RECEIVED_ROLES': {
            const roles = {...state.entities.roles.roles};
            action.data.forEach((r: {name: string}) => {
                roles[r.name] = r;
            });
            return {...state, entities: {...state.entities, roles: {roles}}};
        }
        default:
            return state;
        }
    };
    return createStore(reducer);
};

const deferred = <T, >() => {
    let resolve!: (v: T) => void;
    let reject!: (e: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return {promise, resolve, reject};
};

const renderPage = (store: ReturnType<typeof makeStore>, props: Partial<React.ComponentProps<typeof AgentChatPage>> = {}) => {
    const toChat = jest.fn();
    const toAll = jest.fn();
    const all = {botId: 'bot1', toChat, toAll, ...props};
    const utils = render(
        <Provider store={store}>
            <AgentChatPage {...all}/>
        </Provider>,
    );
    const rerender = (next: Partial<React.ComponentProps<typeof AgentChatPage>>) => utils.rerender(
        <Provider store={store}>
            <AgentChatPage {...{...all, ...next}}/>
        </Provider>,
    );
    return {...utils, rerender, toChat, toAll};
};

const titles = Object.values(emptyChatTitles).map((d) => d.defaultMessage as string);

describe('AgentChatPage', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockUpdateRead.mockResolvedValue({});
        mockGetRolesByNames.mockResolvedValue([]);
    });

    test('fails closed for an agent the user cannot access', () => {
        const {toAll} = renderPage(makeStore({bots: [makeBot('other', 'dm-other')]}));
        expect(screen.getByText('This agent isn\'t available')).toBeTruthy();
        expect(mockEditorProps).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', {name: 'View all agents'}));
        expect(toAll).toHaveBeenCalled();
    });

    test('creates the DM channel, loads it into redux, then mounts the composer and navigates on submit', async () => {
        const store = makeStore({bots: [makeBot('bot1', '')]});
        mockGetBotDirectChannel.mockResolvedValue('dm1');
        mockGetChannel.mockResolvedValue({id: 'dm1'});
        mockGetChannelMember.mockResolvedValue({channel_id: 'dm1', roles: 'channel_user channel_admin'});
        mockGetRolesByNames.mockResolvedValue([{name: 'channel_admin'}]);

        const {toChat} = renderPage(store);
        expect(mockEditorProps).not.toHaveBeenCalled();

        await screen.findByRole('textbox', {name: 'composer'});
        expect(mockGetBotDirectChannel).toHaveBeenCalledWith('me', 'bot1');
        const state = store.getState() as any;
        expect(state[pluginKey].bots[0].dmChannelID).toBe('dm1');
        expect(state.entities.channels.channels.dm1).toBeTruthy();
        expect(state.entities.channels.myMembers.dm1).toBeTruthy();
        expect(mockGetRolesByNames).toHaveBeenCalledWith(['channel_admin']);
        expect(state.entities.roles.roles.channel_admin).toBeTruthy();

        expect(mockEditorProps).toHaveBeenLastCalledWith(expect.objectContaining({
            channelId: 'dm1',
            rootId: '',
            location: 'RHS_COMMENT',
            isThreadView: true,
            storageKey: newChatStorageKey('dm1'),
        }));
        expect(newChatStorageKey('dm1')).not.toMatch(/^(draft_|comment_draft_)/);

        fireEvent.click(screen.getByRole('button', {name: 'send'}));
        expect(toChat).toHaveBeenCalledWith('bot1', 'new-root');
        expect(mockRequestRefresh).toHaveBeenCalledTimes(1);
    });

    test('shows an error and no composer when the DM channel cannot be created', async () => {
        mockGetBotDirectChannel.mockRejectedValue(new Error('nope'));
        renderPage(makeStore({bots: [makeBot('bot1', '')]}));
        expect(await screen.findByText('Couldn\'t open this chat')).toBeTruthy();
        expect(mockEditorProps).not.toHaveBeenCalled();
    });

    test('ignores a superseded DM channel response after switching agents', async () => {
        const store = makeStore({bots: [makeBot('bot1', ''), makeBot('bot2', 'dm2')], loadedChannels: ['dm2']});
        const pending = deferred<string>();
        mockGetBotDirectChannel.mockReturnValue(pending.promise);

        const {rerender} = renderPage(store);
        rerender({botId: 'bot2'});
        await screen.findByRole('textbox', {name: 'composer'});

        await act(async () => {
            pending.resolve('dm1');
        });
        expect((store.getState() as any)[pluginKey].bots[0].dmChannelID).toBe('');
        expect(mockEditorProps).toHaveBeenLastCalledWith(expect.objectContaining({channelId: 'dm2'}));
    });

    test('auto-focuses the composer and keeps the greeting stable across re-renders', async () => {
        const random = jest.spyOn(Math, 'random').mockReturnValue(0.99);
        const store = makeStore({bots: [makeBot('bot1', 'dm1')], loadedChannels: ['dm1']});
        const {rerender} = renderPage(store);

        const composer = await screen.findByRole('textbox', {name: 'composer'});
        expect(document.activeElement).toBe(composer);
        expect(screen.getByRole('heading', {level: 2}).textContent).toBe(titles[titles.length - 1]);

        random.mockReturnValue(0);
        rerender({});
        act(() => {
            store.dispatch({type: BotsHandler, bots: [{...makeBot('bot1', 'dm1'), displayName: 'Renamed'}]});
        });
        expect(screen.getByRole('heading', {level: 2}).textContent).toBe(titles[titles.length - 1]);
        random.mockRestore();
    });

    test.each([
        {name: 'root post in the agent DM', post: {id: 'r1', channel_id: 'dm1', root_id: ''}, valid: true},
        {name: 'post in another channel', post: {id: 'r1', channel_id: 'someone-else', root_id: ''}, valid: false},
        {name: 'reply rather than a root', post: {id: 'r1', channel_id: 'dm1', root_id: 'r0'}, valid: false},
    ])('thread validation: $name', async ({post, valid}) => {
        mockGetPost.mockResolvedValue(post);
        renderPage(makeStore({bots: [makeBot('bot1', 'dm1')], loadedChannels: ['dm1']}), {rootId: 'r1'});

        if (valid) {
            expect(await screen.findByTestId('thread-viewer')).toBeTruthy();
            expect(mockThreadViewerProps).toHaveBeenLastCalledWith(expect.objectContaining({rootPostId: 'r1', isThreadView: true}));
        } else {
            expect(await screen.findByText('This chat doesn\'t belong to this agent, or it no longer exists.')).toBeTruthy();
            expect(mockThreadViewerProps).not.toHaveBeenCalled();
            expect(mockUpdateRead).not.toHaveBeenCalled();
        }
    });

    test('fails closed when the root post cannot be loaded', async () => {
        const error = jest.spyOn(console, 'error').mockImplementation(() => null);
        mockGetPost.mockRejectedValue(new Error('403'));
        renderPage(makeStore({bots: [makeBot('bot1', 'dm1')], loadedChannels: ['dm1']}), {rootId: 'r1'});
        expect(await screen.findByText('Something went wrong while loading this chat. Please try again later.')).toBeTruthy();
        expect(mockThreadViewerProps).not.toHaveBeenCalled();
        error.mockRestore();
    });

    test('uses the post already in the store without refetching', async () => {
        const store = makeStore({
            bots: [makeBot('bot1', 'dm1')],
            loadedChannels: ['dm1'],
            posts: {r1: {id: 'r1', channel_id: 'dm1', root_id: ''}},
        });
        renderPage(store, {rootId: 'r1'});
        expect(await screen.findByTestId('thread-viewer')).toBeTruthy();
        expect(mockGetPost).not.toHaveBeenCalled();
    });

    test('ignores a superseded root post response after switching threads', async () => {
        const first = deferred<unknown>();
        mockGetPost.mockImplementation((id: string) => (id === 'r1' ? first.promise : Promise.resolve({id, channel_id: 'dm1', root_id: ''})));
        const {rerender} = renderPage(makeStore({bots: [makeBot('bot1', 'dm1')], loadedChannels: ['dm1']}), {rootId: 'r1'});

        rerender({rootId: 'r2'});
        await screen.findByTestId('thread-viewer');
        await act(async () => {
            first.resolve({id: 'r1', channel_id: 'elsewhere', root_id: ''});
        });
        expect(screen.getByTestId('thread-viewer')).toBeTruthy();
        expect(mockThreadViewerProps).toHaveBeenLastCalledWith(expect.objectContaining({rootPostId: 'r2'}));
    });

    test.each([
        {name: 'current team', currentTeamId: 'team1', teamMembers: ['teamA'], expected: 'team1'},
        {name: 'first team fallback', currentTeamId: '', teamMembers: ['teamA', 'teamB'], expected: 'teamA'},
        {name: 'no team: skip', currentTeamId: '', teamMembers: [], expected: null},
    ])('updateRead on open and unmount: $name', async ({currentTeamId, teamMembers, expected}) => {
        mockGetPost.mockResolvedValue({id: 'r1', channel_id: 'dm1', root_id: ''});
        const {unmount} = renderPage(makeStore({bots: [makeBot('bot1', 'dm1')], loadedChannels: ['dm1'], currentTeamId, teamMembers}), {rootId: 'r1'});
        await screen.findByTestId('thread-viewer');

        if (expected === null) {
            unmount();
            expect(mockUpdateRead).not.toHaveBeenCalled();
            return;
        }
        expect(mockUpdateRead).toHaveBeenCalledTimes(1);
        expect(mockUpdateRead).toHaveBeenLastCalledWith('me', expected, 'r1', expect.any(Number));
        unmount();
        expect(mockUpdateRead).toHaveBeenCalledTimes(2);
    });

    test.each([
        {name: 'missing thread membership is tolerated', message: 'User thread membership doesn\'t exist', warns: true},
        {name: 'other errors are logged', message: 'boom', warns: false},
    ])('updateRead failure: $name', async ({message, warns}) => {
        const warn = jest.spyOn(console, 'warn').mockImplementation(() => null);
        const error = jest.spyOn(console, 'error').mockImplementation(() => null);
        mockUpdateRead.mockRejectedValue(new Error(message));
        mockGetPost.mockResolvedValue({id: 'r1', channel_id: 'dm1', root_id: ''});
        renderPage(makeStore({bots: [makeBot('bot1', 'dm1')], loadedChannels: ['dm1']}), {rootId: 'r1'});
        await screen.findByTestId('thread-viewer');

        await waitFor(() => expect((warns ? warn : error)).toHaveBeenCalled());
        expect((warns ? error : warn)).not.toHaveBeenCalled();
        warn.mockRestore();
        error.mockRestore();
    });
});
