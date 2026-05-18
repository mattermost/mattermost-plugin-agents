// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render} from '@testing-library/react';
import {Provider} from 'react-redux';
import {applyMiddleware, combineReducers, createStore, Reducer} from 'redux';

// Stub heavy modules pulled in transitively by `bots.tsx` / `redux.tsx`.
jest.mock('@/components/system_console/bot', () => ({
    ChannelAccessLevel: {All: 0, Allow: 1, Block: 2, None: 3},
    UserAccessLevel: {All: 0, Allow: 1, Block: 2, None: 3},
}));

jest.mock('@/mm_webapp', () => ({
    AdvancedTextEditor: () => null,
    CreatePost: () => null,
    PostMessagePreview: () => null,
}));

const mockGetAIBots = jest.fn();
jest.mock('@/client', () => ({
    getAIBots: () => mockGetAIBots(),
}));

// eslint-disable-next-line import/first
import {useBotlist} from './bots';
// eslint-disable-next-line import/first
import {
    BotsHandler,
    CustomPromptsHandler,
    DefaultBotIDHandler,
    PinnedPromptIdsHandler,
    SelectedBotIdHandler,
    ShowCustomPromptsModalHandler,
} from './redux';
// eslint-disable-next-line import/first
import {getDefaultBotID} from './selectors';
// eslint-disable-next-line import/first
import manifest from './manifest';

interface PluginSlice {
    bots: any;
    defaultBotID: string;
    selectedBotId: string | null;
    customPrompts: unknown[];
    pinnedPromptIds: string[];
    showCustomPromptsModal: boolean;
    searchEnabled: boolean;
    allowUnsafeLinks: boolean;
}

const initialPluginSlice: PluginSlice = {
    bots: null,
    defaultBotID: '',
    selectedBotId: null,
    customPrompts: [],
    pinnedPromptIds: [],
    showCustomPromptsModal: false,
    searchEnabled: false,
    allowUnsafeLinks: false,
};

const pluginSlice: Reducer<PluginSlice> = (state = initialPluginSlice, action: any) => {
    switch (action.type) {
    case BotsHandler:
        return {...state, bots: action.bots};
    case DefaultBotIDHandler:
        return {...state, defaultBotID: action.defaultBotID ?? ''};
    case SelectedBotIdHandler:
        return {...state, selectedBotId: action.botId};
    case CustomPromptsHandler:
        return {...state, customPrompts: action.customPrompts};
    case PinnedPromptIdsHandler:
        return {...state, pinnedPromptIds: action.pinnedPromptIds};
    case ShowCustomPromptsModalHandler:
        return {...state, showCustomPromptsModal: action.show};
    case 'SET_SEARCH_ENABLED':
        return {...state, searchEnabled: action.searchEnabled};
    case 'SET_ALLOW_UNSAFE_LINKS':
        return {...state, allowUnsafeLinks: action.allowUnsafeLinks};
    default:
        return state;
    }
};

function entitiesReducer(state = {users: {currentUserId: 'user-1'}}) {
    return state;
}

function createTestStore() {
    return createStore(
        combineReducers({
            [`plugins-${manifest.id}`]: pluginSlice,
            entities: entitiesReducer as any,
        } as any),
        applyMiddleware(() => (next: any) => (action: any) => next(action)),
    );
}

const Harness = () => {
    useBotlist();
    return null;
};

beforeEach(() => {
    mockGetAIBots.mockReset();
});

describe('useBotlist defaultBotID dispatch (MM-68856)', () => {
    test('dispatches DefaultBotIDHandler from the /ai_bots response', async () => {
        mockGetAIBots.mockResolvedValue({
            bots: [{id: 'matty-id', username: 'matty', displayName: 'Matty'}],
            defaultBotID: 'matty-id',
            searchEnabled: false,
            allowUnsafeLinks: false,
        });

        const store = createTestStore();

        await act(async () => {
            render(
                <Provider store={store}>
                    <Harness/>
                </Provider>,
            );
        });

        expect(getDefaultBotID(store.getState() as any)).toBe('matty-id');
    });

    test('handles older servers that omit defaultBotID by defaulting to empty', async () => {
        // Older versions of the server don't send defaultBotID; the hook
        // must default it to '' so the selector keeps the empty-string
        // contract instead of returning undefined.
        mockGetAIBots.mockResolvedValue({
            bots: [{id: 'aira-id', username: 'aira', displayName: 'Aira'}],
            searchEnabled: false,
            allowUnsafeLinks: false,
        });

        const store = createTestStore();

        await act(async () => {
            render(
                <Provider store={store}>
                    <Harness/>
                </Provider>,
            );
        });

        expect(getDefaultBotID(store.getState() as any)).toBe('');
    });
});
