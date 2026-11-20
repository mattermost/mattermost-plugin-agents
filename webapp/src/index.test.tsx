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

// @/client is the real HTTP boundary; stub the calls initialize() makes.
const mockGetAIBots = jest.fn();
const mockSetSiteURL = jest.fn();
const mockGetAIThreads = jest.fn();
jest.mock('@/client', () => ({
    getAIBots: (...args: unknown[]) => mockGetAIBots(...args),
    setSiteURL: (...args: unknown[]) => mockSetSiteURL(...args),
    getAIThreads: (...args: unknown[]) => mockGetAIThreads(...args),
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
function makeRegistry(websocketHandlers: WebSocketHandlers, extra: Record<string, unknown> = {}) {
    return {
        ...extra,
        registerReducer: jest.fn(),
        registerDesktopNotificationHook: jest.fn(),
        registerTranslations: jest.fn(),
        registerPostTypeComponent: jest.fn(),
        registerPostActionComponent: jest.fn(),
        registerAdminConsoleCustomSetting: jest.fn(),
        registerChannelSettingsTab: jest.fn(),
        registerGlobalComponent: jest.fn(),
        registerWebSocketEventHandler: (event: string, handler: (msg: unknown) => void) => {
            websocketHandlers.set(event, handler);
        },
    };
}

function makeStore(dispatch: jest.Mock, siteURL = 'http://localhost:8065', state: Record<string, unknown> = {entities: {
    general: {config: {SiteURL: siteURL}},
    users: {currentUserId: ''},
    i18n: {locale: 'en'},
}}) {
    return {
        getState: () => state,
        dispatch,
        subscribe: jest.fn(),
    };
}

async function initializePlugin(siteURL?: string, state?: Record<string, unknown>, registryExtras: Record<string, unknown> = {}) {
    const websocketHandlers: WebSocketHandlers = new Map();
    const dispatch = jest.fn();

    // The entrypoint self-registers on import, so the host hook must exist first.
    window.registerPlugin = jest.fn();

    // eslint-disable-next-line global-require
    const Plugin = require('./index').default;
    await new Plugin().initialize(makeRegistry(websocketHandlers, registryExtras), makeStore(dispatch, siteURL, state));

    return {websocketHandlers, dispatch};
}

// Lets the floating fetch promises inside initialize()/the handlers settle.
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

function fireWebSocketEvent(websocketHandlers: WebSocketHandlers, event: string, msg: unknown = {}) {
    const handler = websocketHandlers.get(event);
    if (!handler) {
        throw new Error(`no websocket handler registered for ${event}`);
    }
    handler(msg);
}

describe('client base URL', () => {
    beforeEach(() => {
        jest.resetModules();
        mockSetSiteURL.mockReset();
        window.basename = '/mattermost';
    });

    afterEach(() => {
        delete window.basename;
    });

    test('uses the loaded origin and basename instead of the configured SiteURL', async () => {
        await initializePlugin('https://configured.example.test/mattermost');

        expect(mockSetSiteURL).toHaveBeenCalledTimes(1);
        expect(mockSetSiteURL).toHaveBeenCalledWith(`${window.location.origin}/mattermost`);
    });
});

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

describe('agent thread list websocket handlers', () => {
    beforeEach(() => {
        jest.resetModules();
        mockGetAIThreads.mockReset();
        mockGetAIThreads.mockResolvedValue([]);
        mockGetAIBots.mockResolvedValue({bots: [], searchEnabled: false, allowUnsafeLinks: false});
    });

    afterEach(() => {
        jest.useRealTimers();
    });

    test('a chunk of a reply streaming into an unlisted chat refetches the list', async () => {
        const state = {
            entities: {
                general: {config: {SiteURL: 'http://localhost:8065'}},
                users: {currentUserId: 'me'},
                i18n: {locale: 'en'},
                posts: {posts: {reply: {id: 'reply', root_id: 'root', channel_id: 'dm', user_id: 'bot', props: {llm_requester_user_id: 'me'}}}},
            },
            'plugins-mattermost-ai': {aiThreads: {threads: [], loading: false, error: false, requestId: 0}, bots: []},
        };
        const {websocketHandlers} = await initializePlugin('http://localhost:8065', state);
        await flush();

        jest.useFakeTimers();
        fireWebSocketEvent(websocketHandlers, 'custom_mattermost-ai_postupdate', {data: {post_id: 'reply', control: 'continue', next: 'x'}});
        await jest.advanceTimersByTimeAsync(1000);

        expect(mockGetAIThreads).toHaveBeenCalledTimes(1);
    });
});

describe('project channels registration', () => {
    beforeEach(() => {
        jest.resetModules();
        mockGetAIBots.mockResolvedValue({bots: [], searchEnabled: false, allowUnsafeLinks: false});
    });

    const stateWith = (projectChannelsEnabled: boolean) => ({
        entities: {
            general: {config: {SiteURL: 'http://localhost:8065'}},
            users: {currentUserId: 'me'},
            teams: {currentTeamId: 'team1'},
            i18n: {locale: 'en'},
        },
        'plugins-mattermost-ai': {projectChannelsEnabled},
    });

    test.each([
        {name: 'offers the Project option while project channels are on', enabled: true},
        {name: 'hides the Project option while project channels are off', enabled: false},
    ])('$name', async ({enabled}) => {
        const registerChannelTypeOption = jest.fn();
        await initializePlugin('http://localhost:8065', stateWith(enabled), {registerChannelTypeOption});

        expect(registerChannelTypeOption).toHaveBeenCalledTimes(1);
        const option = registerChannelTypeOption.mock.calls[0][0];
        expect(option.isAvailable(stateWith(enabled))).toBe(enabled);
        expect(option.onCreate).toEqual(expect.any(Function));
    });

    test('a project update event is stored for the channel', async () => {
        const {websocketHandlers, dispatch} = await initializePlugin('http://localhost:8065', stateWith(true));
        const project = {channel_id: 'chan1', state: 'active', update_at: 5};

        fireWebSocketEvent(websocketHandlers, 'custom_mattermost-ai_project_updated', {data: project});

        expect(dispatch).toHaveBeenCalledWith({type: 'PROJECT_RECEIVED', project});
    });

    test('an event that is not a project is ignored', async () => {
        const {websocketHandlers, dispatch} = await initializePlugin('http://localhost:8065', stateWith(true));
        dispatch.mockClear();

        fireWebSocketEvent(websocketHandlers, 'custom_mattermost-ai_project_updated', {data: {state: 'active'}});

        expect(dispatch).not.toHaveBeenCalledWith(expect.objectContaining({type: 'PROJECT_RECEIVED'}));
    });
});
