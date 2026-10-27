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
const mockSetSiteURL = jest.fn();
jest.mock('@/client', () => ({
    getAIBots: (...args: unknown[]) => mockGetAIBots(...args),
    setSiteURL: (...args: unknown[]) => mockSetSiteURL(...args),
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
type DesktopNotificationHook = (post: unknown, msgProps: unknown, channel: unknown, teamId: string, args: Record<string, unknown>) => Promise<{args?: Record<string, unknown>}>;

function makeRegistry(websocketHandlers: WebSocketHandlers, options: {channelSettingsTab?: boolean; onDesktopHook?: (hook: DesktopNotificationHook) => void} = {}) {
    return {
        registerReducer: jest.fn(),
        registerDesktopNotificationHook: (hook: DesktopNotificationHook) => options.onDesktopHook?.(hook),
        registerTranslations: jest.fn(),
        registerPostTypeComponent: jest.fn(),
        registerPostActionComponent: jest.fn(),
        registerAdminConsoleCustomSetting: jest.fn(),
        ...(options.channelSettingsTab === false ? {} : {registerChannelSettingsTab: jest.fn()}),
        registerWebSocketEventHandler: (event: string, handler: (msg: unknown) => void) => {
            websocketHandlers.set(event, handler);
        },
    };
}

function makeStore(dispatch: jest.Mock, siteURL = 'http://localhost:8065', pluginState: Record<string, unknown> = {}) {
    const state = {
        'plugins-mattermost-ai': pluginState,
        entities: {
            general: {config: {SiteURL: siteURL}},
            users: {currentUserId: ''},
            i18n: {locale: 'en'},
            posts: {posts: {}},
        },
    };
    return {
        getState: () => state,
        dispatch,
        subscribe: jest.fn(),
    };
}

async function initializePlugin(siteURL?: string, options: {channelSettingsTab?: boolean; pluginState?: Record<string, unknown>} = {}) {
    const websocketHandlers: WebSocketHandlers = new Map();
    const dispatch = jest.fn();
    let desktopHook: DesktopNotificationHook | null = null;

    // The entrypoint self-registers on import, so the host hook must exist first.
    window.registerPlugin = jest.fn();

    // eslint-disable-next-line global-require
    const Plugin = require('./index').default;
    const registry = makeRegistry(websocketHandlers, {
        channelSettingsTab: options.channelSettingsTab,
        onDesktopHook: (hook) => {
            desktopHook = hook;
        },
    });
    await new Plugin().initialize(registry, makeStore(dispatch, siteURL, options.pluginState));

    return {websocketHandlers, dispatch, desktopHook: desktopHook as DesktopNotificationHook | null};
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

describe('deferred agent post notifications', () => {
    beforeEach(() => {
        jest.resetModules();
        mockGetAIBots.mockReset();
    });

    test('the startup bots fetch records server support even without the channel-settings tab', async () => {
        mockGetAIBots.mockResolvedValue({bots: [], deferredNotifications: true});

        const {dispatch} = await initializePlugin('http://localhost:8065', {channelSettingsTab: false});
        await flush();

        expect(mockGetAIBots).toHaveBeenCalledTimes(1);
        expect(dispatch).toHaveBeenCalledWith({type: 'SET_DEFERRED_NOTIFICATIONS', deferredNotifications: true});
    });

    test.each([
        {name: 'a server that defers notifications lets an off-screen agent response notify', deferredNotifications: true, channel: {}, notify: true},
        {name: 'an older server keeps agent responses from notifying', deferredNotifications: false, channel: {}, notify: false},
        {name: 'a server that has not answered yet keeps agent responses from notifying', deferredNotifications: null, channel: {}, notify: false},
        {name: 'a shared channel keeps agent responses from notifying, as the server does not defer them', deferredNotifications: true, channel: {shared: true}, notify: false},
    ])('$name', async ({deferredNotifications, channel, notify}) => {
        mockGetAIBots.mockResolvedValue({bots: []});

        const {desktopHook} = await initializePlugin('http://localhost:8065', {pluginState: {deferredNotifications}});
        expect(desktopHook).not.toBeNull();

        const args = {title: 'Agents', body: 'answer', notify: true};
        const result = await desktopHook!({id: 'agent-post', user_id: 'agent-bot', type: 'custom_llmbot'}, {}, channel, 'team', args);

        expect(result.args?.notify).toBe(notify);
    });

    test.each([
        {name: 'a delivered post', post: JSON.stringify({id: 'agent-post', root_id: 'root', update_at: 5}), received: [{id: 'agent-post', root_id: 'root', update_at: 5}]},
        {name: 'a malformed post', post: '{', received: []},
        {name: 'a post without an id', post: JSON.stringify({root_id: 'root'}), received: []},
        {name: 'a missing post', post: null, received: []},
    ])('core posted events reach subscribers: $name', async ({post, received}) => {
        mockGetAIBots.mockResolvedValue({bots: []});
        const {websocketHandlers} = await initializePlugin();

        // eslint-disable-next-line global-require
        const {subscribeToPosted} = require('./posted_events');
        const seen: unknown[] = [];
        const unsubscribe = subscribeToPosted((p: unknown) => seen.push(p));
        websocketHandlers.get('posted')!({data: {post}});
        unsubscribe();

        expect(seen).toEqual(received);
    });
});
