// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {BotsHandler} from './redux';
import {LLMBot} from './bots';
import {ChannelAccessLevel, UserAccessLevel} from './components/system_console/bot';

// mm_webapp reads window.Components/ProductApi at module load, which are absent
// in jsdom. Stub it so importing the plugin entrypoint doesn't throw.
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

// The production build runs babel-plugin-formatjs, which injects the message
// ids that `formatMessage({defaultMessage})` needs; jest transforms with ts-jest
// only, so resolve plain-string labels to their default message instead.
jest.mock('react-intl', () => ({
    ...jest.requireActual('react-intl'),
    createIntl: () => ({
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    }),
}));

// react-bootstrap and react-router-dom are webpack externals supplied by the
// host webapp, so they are not installed here; the entrypoint's component tree
// imports them at module load.
jest.mock('react-bootstrap', () => ({OverlayTrigger: () => null, Tooltip: () => null}), {virtual: true});
jest.mock('react-router-dom', () => ({
    Link: () => null,
    Redirect: () => null,
    Route: () => null,
    Switch: () => null,
    useHistory: () => ({push: jest.fn()}),
    useLocation: () => ({pathname: '/'}),
    useParams: () => ({}),
    useRouteMatch: () => ({url: '/'}),
}), {virtual: true});

// @/client is the real HTTP boundary; stub the calls initialize() makes.
const mockGetAIBots = jest.fn();
jest.mock('@/client', () => ({
    getAIBots: (...args: unknown[]) => mockGetAIBots(...args),
    setSiteURL: jest.fn(),
    getAIDirectChannel: jest.fn(),
    doReaction: jest.fn(),
    doRunSearch: jest.fn(),
    doThreadAnalysis: jest.fn(),
}));

function makeBot(id: string): LLMBot {
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
    };
}

type WebSocketHandlers = Map<string, (msg: unknown) => void>;

// Only the registry members initialize() calls unconditionally, plus the
// channel-settings tab (its presence is what makes the bots cache load-bearing
// for a synchronous shouldRender).
function makeRegistry(websocketHandlers: WebSocketHandlers) {
    return {
        registerReducer: jest.fn(),
        registerDesktopNotificationHook: jest.fn(),
        registerTranslations: jest.fn(),
        registerPostTypeComponent: jest.fn(),
        registerPostActionComponent: jest.fn(),
        registerAdminConsoleCustomSetting: jest.fn(),
        registerChannelSettingsTab: jest.fn(),
        registerWebSocketEventHandler: (event: string, handler: (msg: unknown) => void) => {
            websocketHandlers.set(event, handler);
        },
    };
}

function makeStore(dispatch: jest.Mock) {
    const state = {
        entities: {
            general: {config: {SiteURL: 'http://localhost:8065'}},
            users: {currentUserId: ''},
            i18n: {locale: 'en'},
        },
    };
    return {
        getState: () => state,
        dispatch,
        subscribe: jest.fn(),
    };
}

async function initializePlugin() {
    const websocketHandlers: WebSocketHandlers = new Map();
    const dispatch = jest.fn();

    // The entrypoint self-registers on import, so the host hook must exist first.
    window.registerPlugin = jest.fn();

    // eslint-disable-next-line global-require
    const Plugin = require('./index').default;
    await new Plugin().initialize(makeRegistry(websocketHandlers), makeStore(dispatch));

    return {websocketHandlers, dispatch};
}

// Lets the floating fetch promises inside initialize()/the handlers settle.
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

function fireWebSocketEvent(websocketHandlers: WebSocketHandlers, event: string) {
    const handler = websocketHandlers.get(event);
    if (!handler) {
        throw new Error(`no websocket handler registered for ${event}`);
    }
    handler({});
}

describe('bots cache websocket handlers', () => {
    beforeEach(() => {
        jest.resetModules();
        mockGetAIBots.mockReset();
    });

    test.each([
        ['config_changed'],
        ['custom_mattermost-ai_bots_invalidate'],
    ])('%s replaces the bots cache without a null window', async (event) => {
        const stale = [makeBot('stale')];
        const fresh = [makeBot('fresh')];
        mockGetAIBots.mockResolvedValue({bots: stale, searchEnabled: false, allowUnsafeLinks: false});

        const {websocketHandlers, dispatch} = await initializePlugin();
        await flush();
        dispatch.mockClear();

        mockGetAIBots.mockResolvedValue({bots: fresh, searchEnabled: false, allowUnsafeLinks: false});
        fireWebSocketEvent(websocketHandlers, event);
        await flush();

        // A `bots: null` dispatch would hide the channel-settings Agents tab
        // until some other surface refetched.
        expect(dispatch).not.toHaveBeenCalledWith(expect.objectContaining({type: BotsHandler, bots: null}));
        expect(dispatch).toHaveBeenCalledWith({type: BotsHandler, bots: fresh});
    });

    test('a failed refetch leaves the previous bots in place', async () => {
        mockGetAIBots.mockResolvedValue({bots: [makeBot('stale')], searchEnabled: false, allowUnsafeLinks: false});

        const {websocketHandlers, dispatch} = await initializePlugin();
        await flush();
        dispatch.mockClear();

        mockGetAIBots.mockRejectedValue(new Error('network'));
        fireWebSocketEvent(websocketHandlers, 'config_changed');
        await flush();

        expect(dispatch).not.toHaveBeenCalledWith(expect.objectContaining({type: BotsHandler}));
    });
});
