// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import manifest from '@/manifest';
import {renderCustomPrompt, createPost} from '@/client';

import {lastAdvancedTextEditorProps} from '../../../tests/fake_advanced_text_editor';

import AgentNewChat, {NEW_CHAT_GREETINGS} from './agent_new_chat';

jest.mock('@/client', () => ({
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    renderCustomPrompt: jest.fn(),
    createPost: jest.fn(),
}));

// The data fetches are covered elsewhere; the state the tests seed is what renders.
jest.mock('@/redux', () => ({
    fetchCustomPrompts: () => ({type: 'noop'}),
    fetchPinnedPromptIds: () => ({type: 'noop'}),
}));

jest.mock('@/mm_webapp', () => ({
    AdvancedTextEditor: jest.requireActual('../../../tests/fake_advanced_text_editor').FakeAdvancedTextEditor,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl};
});

const makePrompt = (id: string) => ({id, name: `Prompt ${id}`, description: '', template: '', creator_id: 'me', is_shared: false, created_at: 0, updated_at: 0, deleted_at: 0});

const makeStore = (prompts: ReturnType<typeof makePrompt>[] = [], pinnedPromptIds: string[] = []) => createStore(() => ({
    entities: {users: {currentUserId: 'me'}},
    [`plugins-${manifest.id}`]: {customPrompts: prompts, pinnedPromptIds},
}));

let store = makeStore();

const bot = {
    id: 'bot1',
    displayName: 'Matty',
    username: 'matty',
    lastIconUpdate: 0,
    dmChannelID: 'dm1',
    channelAccessLevel: 0,
    channelIDs: [],
    userAccessLevel: 0,
    userIDs: [],
    teamIDs: [],
    enabledMCPTools: [],
    autoEnableNewMCPTools: false,
} as any;

const ui = (layout: 'page' | 'panel' = 'panel', onCreated = jest.fn()) => (
    <Provider store={store}>
        <AgentNewChat
            bot={bot}
            channelId='dm1'
            layout={layout}
            onCreated={onCreated}
        />
    </Provider>
);

const text = (d: {defaultMessage?: unknown}) => d.defaultMessage as string;
const shown = () => ({
    title: screen.getByRole('heading', {level: 2}).textContent,
    subtitle: screen.getByRole('heading', {level: 2}).nextElementSibling?.textContent,
});

describe('AgentNewChat', () => {
    beforeEach(() => {
        store = makeStore();
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test.each(NEW_CHAT_GREETINGS.map((greeting, index) => ({index, greeting})))(
        'random index $index shows its own title and subtitle together',
        ({index, greeting}) => {
            jest.spyOn(Math, 'random').mockReturnValue((index + 0.5) / NEW_CHAT_GREETINGS.length);
            render(ui());

            const {title, subtitle} = shown();
            expect(title).toBe(text(greeting.title));
            expect(subtitle).toBe(text(greeting.subtitle));

            // Never mixed with another pair's subtitle.
            NEW_CHAT_GREETINGS.forEach((other, j) => {
                if (j !== index) {
                    expect(subtitle).not.toBe(text(other.subtitle));
                }
            });
        },
    );

    test('the pair is stable across re-renders', () => {
        const random = jest.spyOn(Math, 'random').mockReturnValue(0.99);
        const {rerender} = render(ui());
        const first = shown();

        random.mockReturnValue(0);
        rerender(ui());
        expect(shown()).toEqual(first);
        expect(first.title).toBe(text(NEW_CHAT_GREETINGS[NEW_CHAT_GREETINGS.length - 1].title));
    });

    test.each([
        {layout: 'page' as const, location: 'CENTER'},
        {layout: 'panel' as const, location: 'RHS_COMMENT'},
    ])('in the $layout layout, the host composer posts a root post to the agent\'s DM', ({layout, location}) => {
        render(ui(layout));
        expect(screen.getByTestId('agent-chat-new-composer')).toBeTruthy();
        expect(lastAdvancedTextEditorProps()).toEqual(expect.objectContaining({location, channelId: 'dm1', rootId: '', placeholder: 'Message Matty'}));
    });

    describe('starter prompts', () => {
        const chips = () => screen.queryAllByRole('button').filter((b) => b.textContent?.startsWith('Prompt '));

        test('shows nothing without custom prompts', () => {
            render(ui());
            expect(screen.queryByTestId('agent-chat-starter-prompts')).toBeNull();
        });

        test('shows only pinned prompts, in pin order', () => {
            store = makeStore(['1', '2', '3', '4'].map(makePrompt), ['3', '1']);
            render(ui());
            expect(chips().map((b) => b.textContent)).toEqual(['Prompt 3', 'Prompt 1']);
        });

        test('shows nothing when no prompt is pinned', () => {
            store = makeStore(['1', '2'].map(makePrompt), []);
            render(ui());
            expect(screen.queryByTestId('agent-chat-starter-prompts')).toBeNull();
        });

        test('shows five pinned prompts and the rest in a More menu', () => {
            const ids = ['1', '2', '3', '4', '5', '6', '7'];
            store = makeStore(ids.map(makePrompt), ids);
            render(ui());
            expect(chips().map((b) => b.textContent)).toEqual(['Prompt 1', 'Prompt 2', 'Prompt 3', 'Prompt 4', 'Prompt 5']);

            fireEvent.click(screen.getByRole('button', {name: 'More prompts'}));
            expect(screen.getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Prompt 6', 'Prompt 7']);
        });

        test('has no More menu at five pinned prompts or fewer', () => {
            const ids = ['1', '2', '3', '4', '5'];
            store = makeStore(ids.map(makePrompt), ids);
            render(ui());
            expect(screen.queryByRole('button', {name: 'More prompts'})).toBeNull();
        });

        test('choosing a prompt from the More menu runs it', async () => {
            (renderCustomPrompt as jest.Mock).mockResolvedValue({rendered: 'overflow text'});
            (createPost as jest.Mock).mockResolvedValue({id: 'post7'});
            const ids = ['1', '2', '3', '4', '5', '6'];
            store = makeStore(ids.map(makePrompt), ids);
            const onCreated = jest.fn();
            render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'More prompts'}));
            fireEvent.click(screen.getByRole('menuitem', {name: 'Prompt 6'}));

            await waitFor(() => expect(onCreated).toHaveBeenCalledWith('post7'));
            expect(renderCustomPrompt).toHaveBeenCalledWith('6', 'dm1', 'bot1');
        });

        test('ignores a pin whose prompt is gone', () => {
            store = makeStore(['1', '2'].map(makePrompt), ['gone', '2']);
            render(ui());
            expect(chips().map((b) => b.textContent)).toEqual(['Prompt 2']);
        });

        test('clicking a prompt posts its rendered text to the agent DM and opens the chat', async () => {
            (renderCustomPrompt as jest.Mock).mockResolvedValue({rendered: 'rendered text'});
            (createPost as jest.Mock).mockResolvedValue({id: 'post1'});
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));

            await waitFor(() => expect(onCreated).toHaveBeenCalledWith('post1'));
            expect(renderCustomPrompt).toHaveBeenCalledWith('1', 'dm1', 'bot1');
            expect(createPost).toHaveBeenCalledWith({channel_id: 'dm1', message: 'rendered text', root_id: ''});
        });

        test('a failed run posts nothing and re-enables the prompts', async () => {
            jest.spyOn(console, 'error').mockImplementation(() => 0);
            (renderCustomPrompt as jest.Mock).mockRejectedValue(new Error('nope'));
            (createPost as jest.Mock).mockClear();
            store = makeStore([makePrompt('1')], ['1']);
            const onCreated = jest.fn();
            render(ui('page', onCreated));

            fireEvent.click(screen.getByRole('button', {name: 'Prompt 1'}));

            await waitFor(() => expect((screen.getByRole('button', {name: 'Prompt 1'}) as HTMLButtonElement).disabled).toBe(false));
            expect(createPost).not.toHaveBeenCalled();
            expect(onCreated).not.toHaveBeenCalled();
        });
    });
});
