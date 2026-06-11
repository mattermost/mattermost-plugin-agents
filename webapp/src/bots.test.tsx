// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {renderHook, act} from '@testing-library/react';

import {
    resolveActiveBot,
    getSelectedAgentId,
    useBotlist,
    useBotlistForChannel,
    LLMBot,
} from './bots';
import {ChannelAccessLevel, UserAccessLevel} from './components/system_console/bot';
import manifest from './manifest';

// mm_webapp reads window.Components/ProductApi at module load, which are absent
// in jsdom. Stub it so importing bots.tsx's redux chain doesn't throw.
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

// @/client is the real HTTP boundary; stub it so persistence is observable and
// the bot-fetch effect never makes a network call.
const mockGetAIBots = jest.fn();
const mockSavePreferences = jest.fn();
jest.mock('@/client', () => ({
    getAIBots: (...args: unknown[]) => mockGetAIBots(...args),
    savePreferences: (...args: unknown[]) => mockSavePreferences(...args),
}));

function makeBot(id: string, overrides: Partial<LLMBot> = {}): LLMBot {
    return {
        id,
        displayName: id,
        username: id,
        lastIconUpdate: 0,
        dmChannelID: '',
        channelAccessLevel: ChannelAccessLevel.All,
        channelIDs: null,
        userAccessLevel: UserAccessLevel.All,
        userIDs: null,
        enabledMCPTools: null,
        autoEnableNewMCPTools: false,
        ...overrides,
    };
}

describe('resolveActiveBot precedence', () => {
    const first = makeBot('first');
    const def = makeBot('def', {isDefault: true});
    const other = makeBot('other');
    const bots = [first, def, other];

    test('returns null when no bots', () => {
        expect(resolveActiveBot([], 'def')).toBeNull();
        expect(resolveActiveBot(null, 'def')).toBeNull();
    });

    test('prefers the saved preference when its id is still available', () => {
        expect(resolveActiveBot(bots, 'other')).toBe(other);
    });

    test('falls back to the default bot when the preference id is missing', () => {
        expect(resolveActiveBot(bots, 'gone')).toBe(def);
        expect(resolveActiveBot(bots, '')).toBe(def);
    });

    test('falls back to first bot when no preference and no default', () => {
        const noDefault = [first, other];
        expect(resolveActiveBot(noDefault, '')).toBe(first);
    });
});

describe('getSelectedAgentId', () => {
    test('reads the agents/selected_agent preference value', () => {
        const state = {
            entities: {
                preferences: {
                    myPreferences: {
                        'agents--selected_agent': {value: 'bot-123'},
                    },
                },
            },
        };
        expect(getSelectedAgentId(state)).toBe('bot-123');
    });

    test('returns empty string when unset', () => {
        expect(getSelectedAgentId({entities: {preferences: {myPreferences: {}}}})).toBe('');
        expect(getSelectedAgentId({})).toBe('');
    });
});

// Minimal realistic store: a real reducer handling the same actions the hook
// dispatches (RECEIVED_PREFERENCES), so selection flows back through
// getSelectedAgentId/resolveActiveBot exactly as in production.
const botsKey = `plugins-${manifest.id}`;

function makeStore(bots: LLMBot[], selectedAgentId = '', currentUserId = 'me') {
    const myPreferences: Record<string, {value: string}> = {};
    if (selectedAgentId) {
        myPreferences['agents--selected_agent'] = {value: selectedAgentId};
    }
    const initial = {
        [botsKey]: {bots},
        entities: {
            users: {currentUserId},
            preferences: {myPreferences},
        },
    };
    return createStore((state: any = initial, action: any) => {
        if (action.type === 'RECEIVED_PREFERENCES') {
            const next = {...state.entities.preferences.myPreferences};
            for (const p of action.data) {
                next[`${p.category}--${p.name}`] = {value: p.value};
            }
            return {
                ...state,
                entities: {
                    ...state.entities,
                    preferences: {...state.entities.preferences, myPreferences: next},
                },
            };
        }
        return state;
    });
}

function wrapperFor(store: ReturnType<typeof createStore>) {
    return ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>{children}</Provider>
    );
}

beforeEach(() => {
    mockGetAIBots.mockReset().mockResolvedValue(null);
    mockSavePreferences.mockReset().mockResolvedValue({});
});

describe('useBotlist setActiveBot persistence', () => {
    const botA = makeBot('a');
    const botB = makeBot('b', {isDefault: true});

    test('persists an explicit selection and updates the active bot', () => {
        const store = makeStore([botA, botB]);
        const {result} = renderHook(() => useBotlist(), {wrapper: wrapperFor(store)});

        // Defaults to the configured default before any selection.
        expect(result.current.activeBot).toBe(botB);

        act(() => result.current.setActiveBot(botA));

        expect(mockSavePreferences).toHaveBeenCalledWith('me', [{
            user_id: 'me',
            category: 'agents',
            name: 'selected_agent',
            value: 'a',
        }]);
        expect(result.current.activeBot?.id).toBe('a');
    });

    test('does not persist when called with no bot', () => {
        const store = makeStore([botA, botB]);
        const {result} = renderHook(() => useBotlist(), {wrapper: wrapperFor(store)});

        act(() => result.current.setActiveBot(null));

        expect(mockSavePreferences).not.toHaveBeenCalled();
    });
});

describe('useBotlistForChannel filtering and non-persisted fallback', () => {
    const open = makeBot('open', {isDefault: true, channelAccessLevel: ChannelAccessLevel.All});
    const allowed = makeBot('allowed', {channelAccessLevel: ChannelAccessLevel.Allow, channelIDs: ['chan']});
    const blocked = makeBot('blocked', {channelAccessLevel: ChannelAccessLevel.Block, channelIDs: ['chan']});
    const all = [open, allowed, blocked];

    test('filters bots disallowed in the channel and flags wasFiltered', () => {
        const store = makeStore(all);
        const {result} = renderHook(() => useBotlistForChannel('chan'), {wrapper: wrapperFor(store)});

        expect(result.current.bots.map((b) => b.id)).toEqual(['open', 'allowed']);
        expect(result.current.wasFiltered).toBe(true);
    });

    test('falls back to an allowed bot when the saved selection is filtered out, without persisting', () => {
        const store = makeStore(all, 'blocked');
        const {result} = renderHook(() => useBotlistForChannel('chan'), {wrapper: wrapperFor(store)});

        // 'blocked' is the saved preference but disallowed here, so it resolves
        // to the default within the filtered list and must not be persisted.
        expect(result.current.activeBot).toBe(open);
        expect(mockSavePreferences).not.toHaveBeenCalled();
    });
});
