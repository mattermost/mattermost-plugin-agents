// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {applyMiddleware, createStore, Middleware} from 'redux';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import {CustomPromptsHandler, PinnedPromptIdsHandler} from '@/redux';
import manifest from '@/manifest';
import {CustomPrompt} from '@/types';

import CustomPromptsPage from './custom_prompts_page';

jest.mock('@/mm_webapp', () => ({
    AdvancedTextEditor: null,
    CreatePost: null,
    isRHSCompatable: () => false,
    PostMessagePreview: null,
    Timestamp: null,
    ThreadViewer: null,
    DatePicker: null,
    MenuItem: null,
    MenuSeparator: null,
    useWebSocketClient: () => null,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => {
            return defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '');
        },
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/license', () => ({
    ...jest.requireActual('@/license'),
    useIsLicensedFor: jest.fn(() => true),
}));

// @/client is the HTTP boundary. The fake keeps server-side state so the real
// redux thunks refetch and the UI reflects what the "server" holds.
let serverPrompts: CustomPrompt[] = [];
let serverPins: string[] = [];
const mockGetCustomPrompts = jest.fn(async () => serverPrompts.map((p) => ({...p})));
const mockGetCustomPromptPins = jest.fn(async () => [...serverPins]);
const mockCreateCustomPrompt = jest.fn();
const mockUpdateCustomPrompt = jest.fn();
const mockDeleteCustomPrompt = jest.fn();
const mockSetCustomPromptPin = jest.fn();
jest.mock('@/client', () => ({
    getCustomPrompts: () => mockGetCustomPrompts(),
    getCustomPromptPins: () => mockGetCustomPromptPins(),
    createCustomPrompt: (...a: unknown[]) => mockCreateCustomPrompt(...a),
    updateCustomPrompt: (...a: unknown[]) => mockUpdateCustomPrompt(...a),
    deleteCustomPrompt: (...a: unknown[]) => mockDeleteCustomPrompt(...a),
    setCustomPromptPin: (...a: unknown[]) => mockSetCustomPromptPin(...a),
}));

const ME = 'me';
const CONFIRM_TEXT = 'Are you sure you want to delete this prompt? This action cannot be undone.';
const key = `plugins-${manifest.id}`;

function makePrompt(overrides: Partial<CustomPrompt>): CustomPrompt {
    return {
        id: 'p',
        creator_id: ME,
        name: 'Prompt',
        description: '',
        template: 'template',
        is_shared: false,
        created_at: 0,
        updated_at: 0,
        deleted_at: 0,
        ...overrides,
    };
}

const mine = makePrompt({id: 'mine', name: 'My Own Prompt', description: 'Summarizes things', template: 'Summarize'});
const theirs = makePrompt({id: 'theirs', creator_id: 'other', name: 'Shared Admin Prompt', description: 'Made by admin', template: 'Admin template', is_shared: true});

// The plugin reducer is registered by the host at runtime; mirror its
// handling of the real action types here.
const thunk: Middleware = (api) => (next) => (action: any) => (typeof action === 'function' ? action(api.dispatch, api.getState) : next(action));

function makeStore() {
    const initial = {
        [key]: {customPrompts: null, pinnedPromptIds: null},
        entities: {users: {currentUserId: ME}},
    };
    const reducer = (state: any = initial, action: any) => {
        switch (action.type) {
        case CustomPromptsHandler:
            return {...state, [key]: {...state[key], customPrompts: action.customPrompts}};
        case PinnedPromptIdsHandler:
            return {...state, [key]: {...state[key], pinnedPromptIds: action.pinnedPromptIds}};
        default:
            return state;
        }
    };
    return createStore(reducer, applyMiddleware(thunk));
}

async function renderPage() {
    const store = makeStore();
    render(
        <Provider store={store}>
            <CustomPromptsPage/>
        </Provider>,
    );
    await screen.findByText(mine.name);
}

const rowNames = () => screen.queryAllByRole('button', {name: /^Open prompt /}).map((e) => e.getAttribute('aria-label'));
const heading = () => screen.getByRole('heading', {level: 1}).textContent;

describe('CustomPromptsPage', () => {
    let consoleError: jest.SpyInstance;

    beforeEach(() => {
        serverPrompts = [mine, theirs];
        serverPins = [];
        jest.clearAllMocks();
        consoleError = jest.spyOn(console, 'error').mockImplementation(jest.fn());
    });

    afterEach(() => {
        consoleError.mockRestore();
    });

    test('fetches on mount and renders the titled list without a back button', async () => {
        await renderPage();
        expect(heading()).toBe('Custom Prompts');
        expect(rowNames()).toHaveLength(2);
        expect(screen.getByText('Summarizes things')).not.toBeNull();
        expect(screen.queryByRole('button', {name: 'Back to prompts'})).toBeNull();
        expect(mockGetCustomPrompts).toHaveBeenCalledTimes(1);
        expect(mockGetCustomPromptPins).toHaveBeenCalledTimes(1);
    });

    test('list view shows the description and Create new in the header; forms drop both', async () => {
        await renderPage();
        const description = 'Reusable prompts you can run from the message composer in any channel.';
        expect(screen.getByText(description)).not.toBeNull();
        expect(screen.getByRole('tab', {name: 'All Prompts', selected: true})).not.toBeNull();
        fireEvent.click(screen.getByRole('tab', {name: 'Your Prompts'}));
        expect(rowNames()).toEqual(['Open prompt My Own Prompt']);
        expect(screen.getByPlaceholderText('Search prompts')).not.toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Create new'}));
        expect(screen.queryByText(description)).toBeNull();
        expect(screen.queryByRole('button', {name: 'Create new'})).toBeNull();
    });

    test.each([
        {
            name: 'create form',
            open: () => fireEvent.click(screen.getByRole('button', {name: 'Create new'})),
            title: 'New Prompt',
        },
        {
            name: 'owned prompt',
            open: () => fireEvent.click(screen.getByRole('button', {name: 'Open prompt My Own Prompt'})),
            title: 'My Own Prompt',
        },
        {
            name: 'non-owned prompt',
            open: () => fireEvent.click(screen.getByRole('button', {name: 'Open prompt Shared Admin Prompt'})),
            title: 'Shared Admin Prompt',
        },
    ])('$name shows its title and back returns to the list', async ({open, title}) => {
        await renderPage();
        open();
        expect(heading()).toBe(title);
        expect(screen.queryByRole('tab', {name: 'All Prompts'})).toBeNull();

        fireEvent.click(screen.getByRole('button', {name: 'Back to prompts'}));
        expect(heading()).toBe('Custom Prompts');
        expect(screen.getByRole('tab', {name: 'All Prompts'})).not.toBeNull();
        expect(mockCreateCustomPrompt).not.toHaveBeenCalled();
        expect(mockUpdateCustomPrompt).not.toHaveBeenCalled();
    });

    test('non-owner sees a read-only form with no save or delete', async () => {
        await renderPage();
        fireEvent.click(screen.getByRole('button', {name: 'Open prompt Shared Admin Prompt'}));

        expect(screen.getByText('Admin template')).not.toBeNull();
        expect(screen.queryByPlaceholderText('Enter a title for your prompt')).toBeNull();
        expect(screen.queryByRole('button', {name: 'Save'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Delete'})).toBeNull();
    });

    test('create saves, refetches and returns to the list', async () => {
        mockCreateCustomPrompt.mockImplementation(async (data) => {
            serverPrompts = [...serverPrompts, makePrompt({id: 'new', ...data})];
        });
        await renderPage();
        fireEvent.click(screen.getByRole('button', {name: 'Create new'}));
        fireEvent.change(screen.getByPlaceholderText('Enter a title for your prompt'), {target: {value: 'Brand New'}});
        fireEvent.change(screen.getByPlaceholderText('Enter the system prompt template'), {target: {value: 'Do it'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateCustomPrompt).toHaveBeenCalledTimes(1));
        expect(await screen.findByText('Brand New')).not.toBeNull();
        expect(heading()).toBe('Custom Prompts');
    });

    test('delete from the owner form goes through the confirmation', async () => {
        mockDeleteCustomPrompt.mockImplementation(async (id) => {
            serverPrompts = serverPrompts.filter((p) => p.id !== id);
        });
        await renderPage();
        fireEvent.click(screen.getByRole('button', {name: 'Open prompt My Own Prompt'}));
        fireEvent.click(screen.getByRole('button', {name: 'Delete'}));
        await screen.findByText(CONFIRM_TEXT);
        expect(mockDeleteCustomPrompt).not.toHaveBeenCalled();

        fireEvent.click(screen.getAllByRole('button', {name: 'Delete'}).pop()!);
        await waitFor(() => expect(mockDeleteCustomPrompt).toHaveBeenCalledWith('mine'));
        await waitFor(() => expect(screen.queryByText('My Own Prompt')).toBeNull());
        expect(heading()).toBe('Custom Prompts');
    });

    test('pin failure shows the error banner', async () => {
        mockSetCustomPromptPin.mockRejectedValueOnce(new Error('boom'));
        await renderPage();
        fireEvent.click(screen.getAllByRole('button', {name: 'Pin prompt'})[0]);
        expect(await screen.findByText('Failed to update pin. Please try again.')).not.toBeNull();
    });
});
