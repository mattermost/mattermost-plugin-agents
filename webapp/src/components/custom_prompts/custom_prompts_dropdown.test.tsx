// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {Provider} from 'react-redux';
import {applyMiddleware, combineReducers, createStore, Reducer} from 'redux';

// Stub heavy modules pulled in transitively. `system_console/bot` imports
// `avatar.tsx`, which imports a PNG asset that jest can't transform.
jest.mock('@/components/system_console/bot', () => ({
    ChannelAccessLevel: {All: 0, Allow: 1, Block: 2, None: 3},
    UserAccessLevel: {All: 0, Allow: 1, Block: 2, None: 3},
}));

// eslint-disable-next-line import/first
import CustomPromptsDropdown from './custom_prompts_dropdown';
// eslint-disable-next-line import/first
import {LLMBot} from '@/bots';
// eslint-disable-next-line import/first
import {
    BotsHandler,
    CustomPromptsHandler,
    DefaultBotIDHandler,
    PinnedPromptIdsHandler,
    SelectedBotIdHandler,
    ShowCustomPromptsModalHandler,
} from '@/redux';
// eslint-disable-next-line import/first
import {getDefaultBotID, getSelectedBotId} from '@/selectors';
// eslint-disable-next-line import/first
import manifest from '@/manifest';

const ChannelAccessLevelAll = 0;
const UserAccessLevelAll = 0;

// Stub the bot selector so we can assert on the bot it received without
// pulling the entire agents UI graph (which transitively imports PNG assets
// and the host web app) into this unit test.
jest.mock('@/components/bot_selector', () => ({
    __esModule: true,
    DropdownBotSelector: ({activeBot}: {activeBot: {displayName: string} | null}) => (
        <div data-testid='active-bot'>{activeBot ? activeBot.displayName : 'none'}</div>
    ),
}));

jest.mock('@/mm_webapp', () => ({
    AdvancedTextEditor: () => null,
    CreatePost: () => null,
    PostMessagePreview: () => null,
}));

jest.mock('react-intl', () => ({
    FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => <>{defaultMessage}</>,
    IntlProvider: ({children}: {children: React.ReactNode}) => <>{children}</>,
    useIntl: () => ({formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage}),
}));

jest.mock('@/client', () => ({
    renderCustomPrompt: jest.fn(),
    getProfilePictureUrl: () => 'http://localhost/picture.png',
    getCustomPrompts: jest.fn().mockResolvedValue([]),
    getCustomPromptPins: jest.fn().mockResolvedValue([]),
    fetchModels: jest.fn(),
}));

function makeBot(overrides: Partial<LLMBot>): LLMBot {
    return {
        id: 'bot-id',
        displayName: 'Bot',
        username: 'bot',
        lastIconUpdate: 0,
        dmChannelID: '',
        channelAccessLevel: ChannelAccessLevelAll as LLMBot['channelAccessLevel'],
        channelIDs: [],
        userAccessLevel: UserAccessLevelAll as LLMBot['userAccessLevel'],
        userIDs: [],
        teamIDs: [],
        enabledMCPTools: [],
        autoEnableNewMCPTools: false,
        ...overrides,
    };
}

interface PluginSlice {
    bots: LLMBot[] | null;
    defaultBotID: string;
    selectedBotId: string | null;
    customPrompts: unknown[];
    pinnedPromptIds: string[];
    showCustomPromptsModal: boolean;
}

const initialPluginSlice: PluginSlice = {
    bots: null,
    defaultBotID: '',
    selectedBotId: null,
    customPrompts: [],
    pinnedPromptIds: [],
    showCustomPromptsModal: false,
};

// pluginSlice mirrors the production reducer shape for the keys this
// component reads/writes via Redux. Keeping the same action types means the
// real selector (`getDefaultBotID`) and the component's own dispatches go
// through the real wiring rather than ad-hoc mocks.
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
    default:
        return state;
    }
};

// Track which actions the component dispatches via a passthrough middleware
// so tests can both observe dispatches AND let the real reducer apply them.
// thunkMiddleware mirrors redux-thunk's behavior so the component's
// `dispatch(fetchCustomPrompts() as any)` call (which returns a function)
// resolves without exploding the store.
const dispatches: any[] = [];
const thunkMiddleware = (api: any) => (next: any) => (action: any) => {
    if (typeof action === 'function') {
        return action(api.dispatch, api.getState);
    }
    dispatches.push(action);
    return next(action);
};

function createTestStore(initial: Partial<PluginSlice>) {
    const rootReducer = combineReducers({
        [`plugins-${manifest.id}`]: pluginSlice,
    });
    const store = createStore(rootReducer, applyMiddleware(thunkMiddleware));
    if (initial.bots !== undefined) {
        store.dispatch({type: BotsHandler, bots: initial.bots});
    }
    if (initial.defaultBotID !== undefined) {
        store.dispatch({type: DefaultBotIDHandler, defaultBotID: initial.defaultBotID});
    }
    if (initial.selectedBotId !== undefined) {
        store.dispatch({type: SelectedBotIdHandler, botId: initial.selectedBotId});
    }
    // Drop the seeding dispatches so tests only inspect what the component
    // produced.
    dispatches.length = 0;
    return store;
}

function renderDropdown(initial: Partial<PluginSlice>) {
    const store = createTestStore(initial);
    return {
        store,
        ...render(
            <Provider store={store}>
                <CustomPromptsDropdown
                    draft={{}}
                    getSelectedText={() => ({start: 0, end: 0})}
                    updateText={jest.fn()}
                    channelId='channel-1'
                    isRHS={false}
                />
            </Provider>,
        ),
    };
}

beforeEach(() => {
    dispatches.length = 0;
});

describe('CustomPromptsDropdown bot selection (MM-68856)', () => {
    const aira = makeBot({id: 'aira-id', username: 'aira', displayName: 'Aira'});
    const matty = makeBot({id: 'matty-id', username: 'matty', displayName: 'Matty'});
    const zorro = makeBot({id: 'zorro-id', username: 'zorro', displayName: 'Zorro'});

    test('pre-selects the system-wide default agent when one is configured', () => {
        // The order intentionally puts a non-default bot first to exercise
        // the bug: previously the dropdown showed bots[0] ("Aira") even when
        // the admin had configured Matty as the system default.
        renderDropdown({
            bots: [aira, matty, zorro],
            defaultBotID: matty.id,
        });

        expect(screen.getByTestId('active-bot').textContent).toBe('Matty');
    });

    test('falls back to the first bot when no default is configured', () => {
        renderDropdown({
            bots: [aira, matty, zorro],
            defaultBotID: '',
        });

        expect(screen.getByTestId('active-bot').textContent).toBe('Aira');
    });

    test('falls back to the first bot when defaultBotID does not match any visible bot', () => {
        // E.g. the configured default is restricted from this user.
        renderDropdown({
            bots: [aira, zorro],
            defaultBotID: 'missing-default-id',
        });

        expect(screen.getByTestId('active-bot').textContent).toBe('Aira');
    });

    test('honors the user-selected bot over the system default', () => {
        renderDropdown({
            bots: [aira, matty, zorro],
            defaultBotID: matty.id,
            selectedBotId: zorro.id,
        });

        expect(screen.getByTestId('active-bot').textContent).toBe('Zorro');
    });

    test('dispatches the default bot id to the store when no selection exists yet', () => {
        const {store} = renderDropdown({
            bots: [aira, matty, zorro],
            defaultBotID: matty.id,
        });

        // The component should have dispatched SelectedBotIdHandler with the
        // default bot, and the real reducer should have applied it.
        expect(dispatches).toContainEqual({
            type: SelectedBotIdHandler,
            botId: matty.id,
        });
        expect(getSelectedBotId(store.getState() as any)).toBe(matty.id);
    });

    test('getDefaultBotID selector reads the reducer state set by DefaultBotIDHandler', () => {
        // Locks down the redux wiring: dispatching DefaultBotIDHandler must
        // be reflected by getDefaultBotID. Guards against typos in either
        // the reducer key or the selector.
        const {store} = renderDropdown({
            bots: [aira, matty],
            defaultBotID: matty.id,
        });
        expect(getDefaultBotID(store.getState() as any)).toBe(matty.id);
    });
});
