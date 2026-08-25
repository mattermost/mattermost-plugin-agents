// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {renderHook, act} from '@testing-library/react';
import {PreferenceType} from '@mattermost/types/preferences';

import {
    resolveActiveBot,
    getSelectedAgentId,
    useBotlist,
    useBotlistForChannel,
    fetchAndStoreBots,
    filterBotsByChannelAccess,
    LLMBot,
} from './bots';
import {ChannelAccessLevel, UserAccessLevel} from './components/system_console/bot';
import {BotsHandler} from './redux';
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

type TestEntities = {
    users: {currentUserId: string};
    preferences: {myPreferences: Record<string, {value: string}>};
};

type TestState = {
    entities: TestEntities;
} & Record<string, TestEntities | {bots: LLMBot[]}>;

type ReceivedPreferencesAction = {
    type: 'RECEIVED_PREFERENCES';
    data: PreferenceType[];
};

type TestAction = ReceivedPreferencesAction | {type: string};

function isReceivedPreferencesAction(action: TestAction): action is ReceivedPreferencesAction {
    return action.type === 'RECEIVED_PREFERENCES';
}

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
    return createStore((state: TestState = initial, action: TestAction): TestState => {
        if (isReceivedPreferencesAction(action)) {
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

describe('filterBotsByChannelAccess', () => {
    const all = makeBot('all', {channelAccessLevel: ChannelAccessLevel.All});
    const allowIn = makeBot('allow-in', {channelAccessLevel: ChannelAccessLevel.Allow, channelIDs: ['chan']});
    const allowOut = makeBot('allow-out', {channelAccessLevel: ChannelAccessLevel.Allow, channelIDs: ['other']});
    const blockIn = makeBot('block-in', {channelAccessLevel: ChannelAccessLevel.Block, channelIDs: ['chan']});
    const blockOut = makeBot('block-out', {channelAccessLevel: ChannelAccessLevel.Block, channelIDs: ['other']});
    const none = makeBot('none', {channelAccessLevel: ChannelAccessLevel.None});

    test('keeps All-access bots regardless of channel membership', () => {
        expect(filterBotsByChannelAccess([all], 'chan').map((b) => b.id)).toEqual(['all']);
        expect(filterBotsByChannelAccess([all], 'anything-else').map((b) => b.id)).toEqual(['all']);
    });

    test('Allow requires the channel to be listed in channelIDs', () => {
        expect(filterBotsByChannelAccess([allowIn, allowOut], 'chan').map((b) => b.id)).toEqual(['allow-in']);
    });

    test('Block excludes the channels listed in channelIDs', () => {
        expect(filterBotsByChannelAccess([blockIn, blockOut], 'chan').map((b) => b.id)).toEqual(['block-out']);
    });

    test('None-access bots are always filtered out', () => {
        expect(filterBotsByChannelAccess([none], 'chan')).toEqual([]);
    });

    test('treats null channelIDs as an empty list', () => {
        const allowNull = makeBot('allow-null', {channelAccessLevel: ChannelAccessLevel.Allow, channelIDs: null});
        const blockNull = makeBot('block-null', {channelAccessLevel: ChannelAccessLevel.Block, channelIDs: null});

        expect(filterBotsByChannelAccess([allowNull, blockNull], 'chan').map((b) => b.id)).toEqual(['block-null']);
    });

    test('returns an empty array for an empty bot list', () => {
        expect(filterBotsByChannelAccess([], 'chan')).toEqual([]);
    });
});

describe('fetchAndStoreBots', () => {
    test('dispatches the bots and feature flags, and returns the bots', async () => {
        const bots = [makeBot('a')];
        mockGetAIBots.mockResolvedValue({bots, searchEnabled: true, allowUnsafeLinks: 1});
        const dispatch = jest.fn();

        await expect(fetchAndStoreBots(dispatch)).resolves.toBe(bots);

        expect(dispatch).toHaveBeenCalledWith({type: BotsHandler, bots});
        expect(dispatch).toHaveBeenCalledWith({type: 'SET_SEARCH_ENABLED', searchEnabled: true});
        expect(dispatch).toHaveBeenCalledWith({type: 'SET_ALLOW_UNSAFE_LINKS', allowUnsafeLinks: true});
    });

    test('returns null and dispatches nothing when the response is falsy', async () => {
        mockGetAIBots.mockResolvedValue(null);
        const dispatch = jest.fn();

        await expect(fetchAndStoreBots(dispatch)).resolves.toBeNull();

        expect(dispatch).not.toHaveBeenCalled();
    });

    test('propagates fetch failures to the caller', async () => {
        const error = new Error('network');
        mockGetAIBots.mockRejectedValue(error);

        await expect(fetchAndStoreBots(jest.fn())).rejects.toBe(error);
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
