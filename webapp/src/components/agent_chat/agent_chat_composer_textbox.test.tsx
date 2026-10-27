// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen} from '@testing-library/react';

import {fakeHostTextboxRenders} from '../../../tests/fake_host_textbox';

import AgentChatComposer from './agent_chat_composer';

const mockCreatePost = jest.fn();
const mockExecuteCommand = jest.fn();
const mockGetProfilesByIds = jest.fn();
jest.mock('@/client', () => ({
    createPost: (...args: unknown[]) => mockCreatePost(...args),
    uploadFile: jest.fn(),
    getFileThumbnailUrl: (id: string) => `/api/v4/files/${id}/thumbnail`,
    FileUploadAbortedError: class extends Error {},
    getProfilesByIds: (...args: unknown[]) => mockGetProfilesByIds(...args),
    Client4: {executeCommand: (...args: unknown[]) => mockExecuteCommand(...args)},
}));

// The host publishes window.Components.Textbox; toggled per test.
let mockHostTextbox = true;
jest.mock('@/mm_webapp', () => ({
    get Textbox() {
        return mockHostTextbox ? jest.requireActual('../../../tests/fake_host_textbox').FakeHostTextbox : null;
    },
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string | number>) => (
            defaultMessage.replace(/\{(\w+)(?:, ?\w+)?\}/g, (_m, k) => String(values?.[k] ?? ''))
        ),
        formatNumber: (n: number) => String(n),
    };
    return {...actual, useIntl: () => intl};
});

interface StoreOptions {
    config?: Record<string, string>;
    currentTeamId?: string;
    channels?: Record<string, {id: string; team_id: string}>;
    posts?: Record<string, {id: string; user_id: string}>;
    postsInThread?: Record<string, string[]>;
    profiles?: Record<string, {id: string}>;
}

const makeStore = ({config = {EnableFileAttachments: 'true', MaxFileSize: '1000'}, currentTeamId = 'team1', channels = {}, posts = {}, postsInThread = {}, profiles = {}}: StoreOptions = {}) => {
    const actions: Array<{type: string; data?: unknown}> = [];
    const state = {
        entities: {
            users: {currentUserId: 'me', profiles},
            posts: {posts, postsInThread},
            teams: {currentTeamId, myMembers: {}},
            channels: {channels, myMembers: {}},
            general: {config},
            preferences: {myPreferences: {}},
            roles: {roles: {}},
        },
    };
    const store = createStore((s: typeof state = state, action: {type: string; data?: unknown}) => {
        actions.push(action);
        return s;
    });
    return {store, actions};
};

interface SetupOptions extends StoreOptions {
    channelId?: string;
    rootId?: string;
    agentName?: string;
}

const setup = ({channelId = 'dm1', rootId = '', agentName = 'Matty', ...storeOptions}: SetupOptions = {}) => {
    const {store, actions} = makeStore(storeOptions);
    const onCreated = jest.fn();
    const view = render(
        <Provider store={store}>
            <AgentChatComposer
                channelId={channelId}
                rootId={rootId}
                agentName={agentName}
                onCreated={onCreated}
            />
        </Provider>,
    );
    const textbox = screen.getByRole('textbox') as HTMLTextAreaElement;
    return {...view, store, actions, onCreated, textbox};
};

const type = (textbox: HTMLElement, value: string) => fireEvent.change(textbox, {target: {value}});

const press = async (textbox: HTMLElement, init: Record<string, unknown> = {key: 'Enter'}) => {
    await act(async () => {
        fireEvent.keyDown(textbox, init);
    });
};

const lastProps = () => fakeHostTextboxRenders[fakeHostTextboxRenders.length - 1];

describe('AgentChatComposer with the host Textbox', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockHostTextbox = true;
        fakeHostTextboxRenders.length = 0;
        window.sessionStorage.clear();
        jest.spyOn(console, 'error').mockImplementation(() => null);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test.each([
        {name: 'new chat in the agent DM', channelId: 'dm1', rootId: '', agentName: 'Matty', label: 'Message Matty', placeholder: 'Message Matty'},
        {name: 'reply in the agent DM', channelId: 'dm1', rootId: 'root0', agentName: 'Matty', label: 'Reply to Matty', placeholder: 'Reply…'},
        {name: 'reply in a channel thread the agent was mentioned in', channelId: 'town', rootId: 'root9', agentName: '', label: 'Reply', placeholder: 'Reply…'},
    ])('$name: the host Textbox is scoped to the channel and thread', ({channelId, rootId, agentName, label, placeholder}) => {
        setup({channelId, rootId, agentName, config: {MaxPostSize: '16383'}});
        const textbox = screen.getByRole('textbox', {name: label});
        expect(screen.getByTestId('fake-host-textbox')).toBeTruthy();
        expect(document.activeElement).toBe(textbox);
        expect(lastProps()).toEqual(expect.objectContaining({
            channelId,
            rootId,
            createMessage: placeholder,
            supportsCommands: true,
            useChannelMentions: true,
            suggestionListPosition: 'top',
            characterLimit: 16383,
            preview: false,
        }));
    });

    test('loads missing thread posters so @mention suggestions can rank them', async () => {
        mockGetProfilesByIds.mockResolvedValue([{id: 'bot1', username: 'matty'}]);
        const {actions} = setup({
            rootId: 'root0',
            posts: {root0: {id: 'root0', user_id: 'me'}, r1: {id: 'r1', user_id: 'bot1'}, r2: {id: 'r2', user_id: 'other'}},
            postsInThread: {root0: ['r1', 'r2']},
            profiles: {me: {id: 'me'}, other: {id: 'other'}},
        });
        await act(async () => {
            await Promise.resolve();
        });
        expect(mockGetProfilesByIds).toHaveBeenCalledWith(['bot1']);
        expect(actions).toContainEqual({type: 'RECEIVED_PROFILES', data: [{id: 'bot1', username: 'matty'}]});
    });

    // jsdom lays nothing out: the mirrors' scrollHeight stands in for the text
    // laid out at the inline width (wrapLines lines) and for one line.
    const layOut = (wrapLines: number) => {
        Object.defineProperty(screen.getByTestId('agent-chat-composer-wrap-mirror'), 'scrollHeight', {configurable: true, get: () => (wrapLines * 20) + 20});
        Object.defineProperty(screen.getByTestId('agent-chat-composer-line-mirror'), 'scrollHeight', {configurable: true, get: () => 40});
    };
    const pill = () => screen.getByTestId('agent-chat-composer-pill');
    const tabOrder = () => Array.from(pill().querySelectorAll('button, textarea')).map((el) => el.getAttribute('aria-label') ?? el.tagName.toLowerCase());

    test.each([
        {name: 'one line', value: 'short', inlineLines: 1, expanded: false},
        {name: 'empty', value: '', inlineLines: 1, expanded: false},
        {name: 'a newline', value: 'one\ntwo', inlineLines: 2, expanded: true},
        {name: 'a newline that still fits one line wide', value: 'a\n', inlineLines: 1, expanded: true},
        {name: 'text wrapping at the inline width', value: 'long '.repeat(30), inlineLines: 3, expanded: true},
    ])('$name: expanded is $expanded, with the same tab order either way', ({value, inlineLines, expanded}) => {
        mockHostTextbox = false;
        const {textbox} = setup();
        const inline = tabOrder();
        layOut(inlineLines);
        type(textbox, value);
        expect(pill().getAttribute('data-expanded')).toBe(String(expanded));
        expect(tabOrder()).toEqual(inline);
        expect(inline).toEqual(['Add to message', 'Message Matty', 'Send message']);
    });

    test('expanded collapses only once the text fits one inline line again', () => {
        mockHostTextbox = false;
        const {textbox} = setup();
        layOut(3);
        type(textbox, 'long '.repeat(30));
        expect(pill().getAttribute('data-expanded')).toBe('true');

        // Shorter, but still two lines at the inline width: stays expanded
        // although the wider expanded box would fit it on one.
        layOut(2);
        type(textbox, 'long '.repeat(15));
        expect(pill().getAttribute('data-expanded')).toBe('true');

        layOut(1);
        type(textbox, 'long');
        expect(pill().getAttribute('data-expanded')).toBe('false');
    });

    test('without a host Textbox, a plain textarea stands in', async () => {
        mockHostTextbox = false;
        mockCreatePost.mockResolvedValue({id: 'p1'});
        const {textbox} = setup();
        expect(screen.queryByTestId('fake-host-textbox')).toBeNull();
        expect(textbox.getAttribute('aria-label')).toBe('Message Matty');
        type(textbox, 'hi');
        await press(textbox);
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({channel_id: 'dm1', message: 'hi'}));
    });

    test.each([
        {name: '@mention with Enter', typed: 'hey @ma', key: 'Enter', completed: 'hey @matty '},
        {name: '@mention with Tab', typed: 'hey @ma', key: 'Tab', completed: 'hey @matty '},
        {name: ':emoji: with Enter', typed: 'nice :smi', key: 'Enter', completed: 'nice :smile: '},
        {name: '~channel with Enter', typed: 'see ~to', key: 'Enter', completed: 'see ~town-square '},
    ])('$name picks the open suggestion without sending; the next Enter sends', async ({typed, key, completed}) => {
        mockCreatePost.mockResolvedValue({id: 'p1'});
        const {textbox, onCreated} = setup();
        type(textbox, typed);
        expect(screen.getByRole('listbox', {name: 'Suggestions'})).toBeTruthy();

        await press(textbox, {key});
        expect(mockCreatePost).not.toHaveBeenCalled();
        expect(textbox.value).toBe(completed);
        expect(screen.queryByRole('listbox')).toBeNull();

        await press(textbox);
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({message: completed.trim()}));
        expect(onCreated).toHaveBeenCalledWith('p1');
        expect(textbox.value).toBe('');
    });

    test('Enter the suggestion box stops (list still loading) does not send', async () => {
        const {textbox} = setup();
        type(textbox, 'hey @lag');
        await press(textbox);
        expect(mockCreatePost).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'Shift+Enter', init: {key: 'Enter', shiftKey: true}},
        {name: 'Enter while composing (IME)', init: {key: 'Enter', isComposing: true}},
        {name: 'Enter with IME keyCode 229', init: {key: 'Enter', keyCode: 229}},
    ])('$name does not send', async ({init}) => {
        const {textbox} = setup();
        type(textbox, 'hello');
        await press(textbox, init);
        expect(mockCreatePost).not.toHaveBeenCalled();
    });

    test.each<{name: string; channelId: string; rootId: string; currentTeamId: string; channels: Record<string, {id: string; team_id: string}>; teamId: string}>([
        {name: 'in the agent DM, under the current team', channelId: 'dm1', rootId: '', currentTeamId: 'team1', channels: {}, teamId: 'team1'},
        {name: 'in a channel thread', channelId: 'town', rootId: 'root9', currentTeamId: 'team1', channels: {town: {id: 'town', team_id: 'team1'}}, teamId: 'team1'},
        {name: 'with no current team, under the channel\'s team', channelId: 'town', rootId: 'root9', currentTeamId: '', channels: {town: {id: 'town', team_id: 'team2'}}, teamId: 'team2'},
    ])('a slash command runs through the commands API $name and clears the box', async ({channelId, rootId, currentTeamId, channels, teamId}) => {
        mockExecuteCommand.mockResolvedValue({});
        const {textbox} = setup({channelId, rootId, currentTeamId, channels});
        type(textbox, '/Echo   hello ');
        await press(textbox);
        expect(mockExecuteCommand).toHaveBeenCalledWith('/echo hello', {channel_id: channelId, team_id: teamId, root_id: rootId});
        expect(mockCreatePost).not.toHaveBeenCalled();
        expect(textbox.value).toBe('');
        expect(screen.queryByRole('alert')).toBeNull();
    });

    test('a failed command keeps the text and shows the server error', async () => {
        mockExecuteCommand.mockRejectedValueOnce({message: 'Unable to run that.', server_error_id: 'api.command.execute_command.failed.app_error'});
        const {textbox} = setup({rootId: 'root0'});
        type(textbox, '/echo hi');
        await press(textbox);
        expect(screen.getByRole('alert').textContent).toBe('Unable to run that.');
        expect(textbox.value).toBe('/echo hi');
        expect(textbox.getAttribute('aria-invalid')).toBe('true');
        expect(mockCreatePost).not.toHaveBeenCalled();

        // Still a command on retry.
        mockExecuteCommand.mockResolvedValueOnce({});
        await press(textbox);
        expect(mockExecuteCommand).toHaveBeenCalledTimes(2);
        expect(textbox.value).toBe('');
    });

    test('an unknown command can be sent again as a message', async () => {
        mockExecuteCommand.mockRejectedValueOnce({message: 'Command with a trigger of \'nope\' not found.', server_error_id: 'api.command.execute_command.not_found.app_error'});
        mockCreatePost.mockResolvedValue({id: 'p1'});
        const {textbox} = setup();
        type(textbox, '/nope');
        await press(textbox);
        expect(screen.getByRole('alert').textContent).toBe('Command with a trigger of \'nope\' not found. Send it again to post it as a message.');
        expect(textbox.value).toBe('/nope');

        await press(textbox);
        expect(mockExecuteCommand).toHaveBeenCalledTimes(1);
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({message: '/nope'}));
    });

    test('"//" posts the text literally', async () => {
        mockCreatePost.mockResolvedValue({id: 'p1'});
        const {textbox} = setup();
        type(textbox, '//not a command');
        await press(textbox);
        expect(mockExecuteCommand).not.toHaveBeenCalled();
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({message: '//not a command'}));
    });

    test('a command that opens a dialog hands its trigger id to the host store', async () => {
        mockExecuteCommand.mockResolvedValue({trigger_id: 'trig1'});
        const {textbox, actions} = setup();
        type(textbox, '/poll');
        await press(textbox);
        expect(actions).toContainEqual({type: 'RECEIVED_DIALOG_TRIGGER_ID', data: 'trig1'});
    });

    test('over the server MaxPostSize the message is held with an error', async () => {
        const {textbox} = setup({config: {MaxPostSize: '5'}});
        type(textbox, '123456');
        expect(screen.getByRole('alert').textContent).toBe('Your message is too long. Character count: 6/5');
        expect((screen.getByRole('button', {name: 'Send message'}) as HTMLButtonElement).disabled).toBe(true);
        await press(textbox);
        expect(mockCreatePost).not.toHaveBeenCalled();

        type(textbox, '12345');
        expect(screen.queryByRole('alert')).toBeNull();
    });

    test('drafts: kept per channel and thread across remounts, cleared by a send', async () => {
        const first = setup({rootId: 'root0'});
        type(first.textbox, 'half a thought');
        first.unmount();

        const other = setup({rootId: 'root1'});
        expect(other.textbox.value).toBe('');
        other.unmount();

        mockCreatePost.mockResolvedValue({id: 'p1'});
        const again = setup({rootId: 'root0'});
        expect(again.textbox.value).toBe('half a thought');
        await press(again.textbox);
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({message: 'half a thought', root_id: 'root0'}));
        again.unmount();

        const after = setup({rootId: 'root0'});
        expect(after.textbox.value).toBe('');

        // A new chat's composer goes away as its first post creates the thread;
        // its draft must not come back.
        after.unmount();
        const {store} = makeStore();
        const NewChat = () => {
            const [open, setOpen] = React.useState(true);
            return open ? (
                <AgentChatComposer
                    channelId='dm1'
                    agentName='Matty'
                    onCreated={() => setOpen(false)}
                />
            ) : null;
        };
        const newChat = render(<Provider store={store}><NewChat/></Provider>);
        type(screen.getByRole('textbox'), 'first message');
        await press(screen.getByRole('textbox'));
        expect(screen.queryByRole('textbox')).toBeNull();
        newChat.unmount();
        expect(setup().textbox.value).toBe('');

        // Never the host's own draft keys.
        expect(Object.keys(window.sessionStorage).some((k) => k.startsWith('draft_'))).toBe(false);
    });
});
