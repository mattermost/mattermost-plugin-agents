// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {applyMiddleware, createStore, Middleware} from 'redux';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import {CustomPromptsHandler, PinnedPromptIdsHandler, ShowCustomPromptsModalHandler} from '@/redux';
import manifest from '@/manifest';
import {CustomPrompt} from '@/types';

import CustomPromptsManagement from './custom_prompts_management';

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
        [key]: {customPrompts: null, pinnedPromptIds: null, showCustomPromptsModal: false},
        entities: {users: {currentUserId: ME}},
    };
    const reducer = (state: any = initial, action: any) => {
        switch (action.type) {
        case CustomPromptsHandler:
            return {...state, [key]: {...state[key], customPrompts: action.customPrompts}};
        case PinnedPromptIdsHandler:
            return {...state, [key]: {...state[key], pinnedPromptIds: action.pinnedPromptIds}};
        case ShowCustomPromptsModalHandler:
            return {...state, [key]: {...state[key], showCustomPromptsModal: action.show}};
        default:
            return state;
        }
    };
    return createStore(reducer, applyMiddleware(thunk));
}

async function renderOpen() {
    const store = makeStore();
    render(
        <Provider store={store}>
            <CustomPromptsManagement/>
        </Provider>,
    );
    await act(async () => {
        store.dispatch({type: ShowCustomPromptsModalHandler, show: true});
    });
    await screen.findByText(mine.name);
    return store;
}

const rowNames = () => screen.queryAllByRole('button', {name: /^Open prompt /}).map((e) => e.getAttribute('aria-label'));

describe('CustomPromptsManagement', () => {
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

    test('renders nothing until shown, then fetches and lists prompts', async () => {
        const store = makeStore();
        render(
            <Provider store={store}>
                <CustomPromptsManagement/>
            </Provider>,
        );
        expect(screen.queryByText('All Prompts')).toBeNull();
        expect(mockGetCustomPrompts).not.toHaveBeenCalled();

        await act(async () => {
            store.dispatch({type: ShowCustomPromptsModalHandler, show: true});
        });

        expect(await screen.findByText('My Own Prompt')).not.toBeNull();
        expect(screen.getByText('Shared Admin Prompt')).not.toBeNull();
        expect(screen.getByText('Summarizes things')).not.toBeNull();
        expect(mockGetCustomPrompts).toHaveBeenCalledTimes(1);
        expect(mockGetCustomPromptPins).toHaveBeenCalledTimes(1);
    });

    test('tabs filter between all prompts and the current user\'s prompts', async () => {
        await renderOpen();
        expect(rowNames()).toHaveLength(2);

        fireEvent.click(screen.getByRole('tab', {name: 'Your Prompts'}));
        expect(rowNames()).toEqual(['Open prompt My Own Prompt']);
        expect(screen.getByRole('tab', {name: 'Your Prompts'}).getAttribute('aria-selected')).toBe('true');

        fireEvent.click(screen.getByRole('tab', {name: 'All Prompts'}));
        expect(rowNames()).toHaveLength(2);
    });

    test.each([
        {query: 'own', expected: ['Open prompt My Own Prompt'], why: 'matches name case-insensitively'},
        {query: 'ADMIN', expected: ['Open prompt Shared Admin Prompt'], why: 'matches name or description'},
        {query: 'made by', expected: ['Open prompt Shared Admin Prompt'], why: 'matches description'},
        {query: 'zzz-nothing', expected: [], why: 'no match'},
    ])('search $why ($query)', async ({query, expected}) => {
        await renderOpen();
        fireEvent.change(screen.getByPlaceholderText('Search prompts'), {target: {value: query}});
        expect(rowNames()).toEqual(expected);
        if (expected.length === 0) {
            expect(screen.getByText('No prompts found')).not.toBeNull();
        }
    });

    test('unpinned prompt shows Pin and pins', async () => {
        await renderOpen();
        const pin = screen.getAllByRole('button', {name: 'Pin prompt'})[0];
        expect(pin.getAttribute('aria-pressed')).toBe('false');
        fireEvent.click(pin);
        await waitFor(() => expect(mockSetCustomPromptPin).toHaveBeenCalledWith('mine', true));
        await waitFor(() => expect(mockGetCustomPromptPins).toHaveBeenCalledTimes(2));
    });

    test('pinned prompt shows Unpin and unpins', async () => {
        serverPins = ['mine'];
        await renderOpen();
        const unpin = await screen.findByRole('button', {name: 'Unpin prompt'});
        expect(unpin.getAttribute('aria-pressed')).toBe('true');
        fireEvent.click(unpin);
        await waitFor(() => expect(mockSetCustomPromptPin).toHaveBeenCalledWith('mine', false));
    });

    test('pin failure shows the error banner', async () => {
        mockSetCustomPromptPin.mockRejectedValueOnce(new Error('boom'));
        await renderOpen();
        fireEvent.click(screen.getAllByRole('button', {name: 'Pin prompt'})[0]);
        expect(await screen.findByText('Failed to update pin. Please try again.')).not.toBeNull();
    });

    test('create saves via API, refetches and returns to the list', async () => {
        mockCreateCustomPrompt.mockImplementation(async (data) => {
            serverPrompts = [...serverPrompts, makePrompt({id: 'new', ...data})];
        });
        await renderOpen();

        fireEvent.click(screen.getByRole('button', {name: 'Create new'}));
        expect(screen.getByText('New Prompt')).not.toBeNull();
        expect(screen.queryByRole('tab', {name: 'All Prompts'})).toBeNull();

        fireEvent.change(screen.getByPlaceholderText('Enter a title for your prompt'), {target: {value: 'Brand New'}});
        fireEvent.change(screen.getByPlaceholderText('Enter the system prompt template'), {target: {value: 'Do it'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockCreateCustomPrompt).toHaveBeenCalledWith({
            name: 'Brand New',
            description: '',
            template: 'Do it',
            is_shared: false,
        }));
        expect(await screen.findByText('Brand New')).not.toBeNull();
        expect(screen.getByRole('tab', {name: 'All Prompts'})).not.toBeNull();
    });

    test('create failure keeps the form open and shows the banner', async () => {
        mockCreateCustomPrompt.mockRejectedValueOnce(new Error('nope'));
        await renderOpen();
        fireEvent.click(screen.getByRole('button', {name: 'Create new'}));
        fireEvent.change(screen.getByPlaceholderText('Enter a title for your prompt'), {target: {value: 'X'}});
        fireEvent.change(screen.getByPlaceholderText('Enter the system prompt template'), {target: {value: 'Y'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        expect(await screen.findByText('Failed to create prompt. Please try again.')).not.toBeNull();
        expect(screen.getByPlaceholderText('Enter a title for your prompt')).not.toBeNull();
    });

    test('edit opens the owner\'s prompt, saves changes and returns to the list', async () => {
        mockUpdateCustomPrompt.mockImplementation(async (id, data) => {
            serverPrompts = serverPrompts.map((p) => (p.id === id ? {...p, ...data} : p));
        });
        await renderOpen();

        fireEvent.click(screen.getByRole('button', {name: 'Open prompt My Own Prompt'}));
        const title = screen.getByPlaceholderText('Enter a title for your prompt') as HTMLInputElement;
        expect(title.value).toBe('My Own Prompt');

        fireEvent.change(title, {target: {value: 'Renamed'}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        await waitFor(() => expect(mockUpdateCustomPrompt).toHaveBeenCalledWith('mine', expect.objectContaining({name: 'Renamed'})));
        expect(await screen.findByText('Renamed')).not.toBeNull();
        expect(screen.queryByText('My Own Prompt')).toBeNull();
    });

    test('back from the form returns to the list without saving', async () => {
        await renderOpen();
        fireEvent.click(screen.getByRole('button', {name: 'Open prompt My Own Prompt'}));
        fireEvent.click(screen.getByRole('button', {name: 'Back to prompts'}));
        expect(screen.getByRole('tab', {name: 'All Prompts'})).not.toBeNull();
        expect(mockUpdateCustomPrompt).not.toHaveBeenCalled();
    });

    test('non-owner sees a read-only form with no save or delete', async () => {
        await renderOpen();
        fireEvent.click(screen.getByRole('button', {name: 'Open prompt Shared Admin Prompt'}));

        expect(screen.getByText('Admin template')).not.toBeNull();
        expect(screen.queryByPlaceholderText('Enter a title for your prompt')).toBeNull();
        expect(screen.queryByRole('button', {name: 'Save'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Delete'})).toBeNull();
    });

    test('delete requires confirmation, then deletes and refetches', async () => {
        mockDeleteCustomPrompt.mockImplementation(async (id) => {
            serverPrompts = serverPrompts.filter((p) => p.id !== id);
        });
        await renderOpen();
        fireEvent.click(screen.getByRole('button', {name: 'Open prompt My Own Prompt'}));
        fireEvent.click(screen.getByRole('button', {name: 'Delete'}));

        expect(mockDeleteCustomPrompt).not.toHaveBeenCalled();
        expect(await screen.findByText(CONFIRM_TEXT)).not.toBeNull();

        // Cancel leaves the prompt alone.
        fireEvent.click(screen.getByRole('button', {name: 'Cancel'}));
        expect(mockDeleteCustomPrompt).not.toHaveBeenCalled();
        await waitFor(() => expect(screen.queryByText(CONFIRM_TEXT)).toBeNull());

        fireEvent.click(screen.getByRole('button', {name: 'Delete'}));
        await screen.findByText(CONFIRM_TEXT);
        fireEvent.click(screen.getAllByRole('button', {name: 'Delete'}).pop()!);

        await waitFor(() => expect(mockDeleteCustomPrompt).toHaveBeenCalledWith('mine'));
        await waitFor(() => expect(screen.queryByText('My Own Prompt')).toBeNull());
        expect(screen.getByText('Shared Admin Prompt')).not.toBeNull();
    });

    test('delete failure shows the banner and closes the confirmation', async () => {
        mockDeleteCustomPrompt.mockRejectedValueOnce(new Error('fail'));
        await renderOpen();
        fireEvent.click(screen.getByRole('button', {name: 'Open prompt My Own Prompt'}));
        fireEvent.click(screen.getByRole('button', {name: 'Delete'}));
        await screen.findByText(CONFIRM_TEXT);
        fireEvent.click(screen.getAllByRole('button', {name: 'Delete'}).pop()!);

        expect(await screen.findByText('Failed to delete prompt. Please try again.')).not.toBeNull();
        await waitFor(() => expect(screen.queryByText(CONFIRM_TEXT)).toBeNull());
    });

    test('closing the modal hides it and resets to the list view', async () => {
        const store = await renderOpen();
        fireEvent.click(screen.getByRole('button', {name: 'Open prompt My Own Prompt'}));
        fireEvent.click(screen.getByRole('button', {name: 'Close'}));
        expect((store.getState() as any)[key].showCustomPromptsModal).toBe(false);
    });
});
