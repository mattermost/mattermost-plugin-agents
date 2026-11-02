// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getAIBots, getPluginConfig, savePluginConfig} from '@/client';

import Config, {effectiveDefaultBotID} from './config';
import {PluginConfig} from './plugin_config_types';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // A stable intl instance: config.tsx keys effects on [intl], and a fresh
    // object per render would re-run the config load effect forever.
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/client', () => ({
    getPluginConfig: jest.fn(),
    getAIBots: jest.fn(),
    savePluginConfig: jest.fn(),
}));

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

// The heavy editors are not under test; expose the identity each entry is
// rendered with so ID propagation from the save response is observable.
jest.mock('./services', () => ({
    __esModule: true,
    default: ({services}: {services: Array<{id: string; name: string}>}) => (
        <div>
            {services.map((s, i) => (
                <span key={i}>{`service:${s.name}:${s.id || 'unsaved'}`}</span>
            ))}
        </div>
    ),
    firstNewService: {id: '', name: 'OpenAI Service'},
}));

type MockMCPConfig = {servers: Array<{id?: string; name: string}> | null};
const mockMCPServers: {onChange?: (update: MockMCPConfig | ((current: MockMCPConfig) => MockMCPConfig)) => void} = {};

jest.mock('./mcp_servers', () => ({
    __esModule: true,
    default: ({mcpConfig, onChange}: {mcpConfig: MockMCPConfig; onChange: typeof mockMCPServers.onChange}) => {
        mockMCPServers.onChange = onChange;
        return (
            <div>
                {(mcpConfig.servers ?? []).map((s, i) => (
                    <span key={i}>{`mcp:${s.name}:${s.id || 'unsaved'}`}</span>
                ))}
            </div>
        );
    },
}));

jest.mock('./embedding_search/embedding_search_panel', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('./web_search/web_search_panel', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('./bots_moved_notice', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('./no_services_page', () => ({
    __esModule: true,
    default: () => <div>{'no-services'}</div>,
}));

type SaveAction = () => Promise<{error?: {message?: string}}>;

const loadedConfig = {
    services: [{id: '', name: 'My Service', type: 'openai'}],
    mcp: {
        enabled: true,
        enablePluginServer: false,
        servers: [{name: 'Jira', enabled: true, baseURL: 'https://jira.example.com', headers: {}}],
        embeddedServer: {enabled: true},
    },
};

function renderConfig() {
    const registerSaveAction = jest.fn();
    const props = {
        id: 'Config',
        label: '',
        helpText: null,
        value: {} as never,
        disabled: false,
        config: {},
        currentState: {},
        license: {},
        setByEnv: false,
        onChange: jest.fn(),
        setSaveNeeded: jest.fn(),
        registerSaveAction,
        unRegisterSaveAction: jest.fn(),
    };
    render(
        <IntlProvider locale='en'>
            <Config {...props}/>
        </IntlProvider>,
    );
    return {registerSaveAction};
}

describe('Config save flow', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        (getPluginConfig as jest.Mock).mockResolvedValue(loadedConfig);
        (getAIBots as jest.Mock).mockResolvedValue({bots: []});
    });

    it('adopts server-minted IDs from the save response so ID-gated UI appears without a reload', async () => {
        const {registerSaveAction} = renderConfig();

        // Loaded config renders without IDs.
        await screen.findByText('service:My Service:unsaved');
        expect(screen.getByText('mcp:Jira:unsaved')).toBeTruthy();

        const savedConfig = {
            ...loadedConfig,
            services: [{id: 'serviceidaaaaaaaaaaaaaaaaa', name: 'My Service', type: 'openai'}],
            mcp: {
                ...loadedConfig.mcp,
                servers: [{...loadedConfig.mcp.servers[0], id: 'mcpserveridbbbbbbbbbbbbbbb'}],
            },
        };
        (savePluginConfig as jest.Mock).mockResolvedValue(savedConfig);

        const save = registerSaveAction.mock.calls.at(-1)?.[0] as SaveAction;
        let result: {error?: {message?: string}} = {};
        await act(async () => {
            result = await save();
        });

        expect(result).toEqual({});
        expect(savePluginConfig).toHaveBeenCalledTimes(1);

        // The normalized response replaces local state: minted IDs are live.
        await screen.findByText('service:My Service:serviceidaaaaaaaaaaaaaaaaa');
        expect(screen.getByText('mcp:Jira:mcpserveridbbbbbbbbbbbbbbb')).toBeTruthy();
    });

    it('reports a save error and keeps local state when the save is rejected', async () => {
        const {registerSaveAction} = renderConfig();
        await screen.findByText('service:My Service:unsaved');

        (savePluginConfig as jest.Mock).mockRejectedValue(new Error('409'));

        const save = registerSaveAction.mock.calls.at(-1)?.[0] as SaveAction;
        let result: {error?: {message?: string}} = {};
        await act(async () => {
            result = await save();
        });

        expect(result.error?.message).toBe('Failed to save configuration.');
        expect(screen.getByText('service:My Service:unsaved')).toBeTruthy();
        expect(screen.getByText('mcp:Jira:unsaved')).toBeTruthy();
    });

    it('shows an inline error when the save is rejected for invalid allowed link domains, until the field is edited', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, allowUnsafeLinks: true, allowedLinkDomains: 'not a host'});
        const {registerSaveAction} = renderConfig();
        await screen.findByText('service:My Service:unsaved');

        (savePluginConfig as jest.Mock).mockRejectedValue(Object.assign(new Error(''), {server_error_id: 'invalid_allowed_link_domains'}));

        const save = registerSaveAction.mock.calls.at(-1)?.[0] as SaveAction;
        let result: {error?: {message?: string}} = {};
        await act(async () => {
            result = await save();
        });

        expect(result.error?.message).toBe('Failed to save configuration.');
        expect(await screen.findByText('One or more entries are not valid domains.')).toBeTruthy();

        fireEvent.change(screen.getByLabelText('Allowed Link Domains (csv)'), {target: {value: 'example.com'}});
        expect(screen.queryByText('One or more entries are not valid domains.')).toBeNull();
    });
});

describe('Config MCP updates', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        (getPluginConfig as jest.Mock).mockResolvedValue(loadedConfig);
        (getAIBots as jest.Mock).mockResolvedValue({bots: []});
    });

    it('applies an update function to the configuration as it is when the update lands', async () => {
        renderConfig();
        await screen.findByText('mcp:Jira:unsaved');

        act(() => {
            mockMCPServers.onChange?.({servers: [...loadedConfig.mcp.servers, {name: 'GitHub'}]});
        });
        act(() => {
            mockMCPServers.onChange?.((current) => ({
                ...current,
                servers: (current.servers ?? []).map((s) => (s.name === 'Jira' ? {...s, name: 'Jira Cloud'} : s)),
            }));
        });

        expect(screen.getByText('mcp:Jira Cloud:unsaved')).toBeTruthy();
        expect(screen.getByText('mcp:GitHub:unsaved')).toBeTruthy();
    });
});

describe('Config license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        jest.clearAllMocks();
        useIsLicensedFor.mockReturnValue(true);
        (getPluginConfig as jest.Mock).mockResolvedValue({
            ...loadedConfig,
            enableTokenUsageLogging: true,
            allowNativeWebSearchInChannels: true,
        });
        (getAIBots as jest.Mock).mockResolvedValue({bots: [{username: 'matty', displayName: 'Matty'}]});
    });

    function rowFor(label: string): HTMLElement {
        const el = screen.getByText(label);
        const row = el.tagName === 'LABEL' ? el.parentElement : el.closest('div');
        if (!row) {
            throw new Error(`No row for ${label}`);
        }
        return row;
    }

    function trueRadioFor(label: string): HTMLInputElement {
        return rowFor(label).querySelector('input[value="true"]') as HTMLInputElement;
    }

    function falseRadioFor(label: string): HTMLInputElement {
        return rowFor(label).querySelector('input[value="false"]') as HTMLInputElement;
    }

    it('keeps token logging and native web search settings visible below their minimum and allows turning them off', async () => {
        useIsLicensedFor.mockReturnValue(false);
        renderConfig();
        await screen.findByText('Enable Token Usage Logging');

        expect(trueRadioFor('Enable Token Usage Logging').disabled).toBe(true);
        expect(falseRadioFor('Enable Token Usage Logging').disabled).toBe(false);
        expect(trueRadioFor('Allow native web search in channels').disabled).toBe(true);
        expect(falseRadioFor('Allow native web search in channels').disabled).toBe(false);
    });

    it('allows enabling token logging and native web search settings when licensed', async () => {
        useIsLicensedFor.mockReturnValue(true);
        (getPluginConfig as jest.Mock).mockResolvedValue({
            ...loadedConfig,
            enableTokenUsageLogging: false,
            allowNativeWebSearchInChannels: false,
        });
        renderConfig();
        await screen.findByText('Enable Token Usage Logging');

        expect(trueRadioFor('Enable Token Usage Logging').disabled).toBe(false);
        expect(trueRadioFor('Allow native web search in channels').disabled).toBe(false);
    });
});

const BOT_FIRST = 'f1rstbotidaaaaaaaaaaaaaaa';
const BOT_SECOND = 's3condbotidbbbbbbbbbbbbbb';
const BOT_TRANSCRIPT = 't4anscriptbotidcccccccccc';

const runtimeBots = [
    {id: BOT_FIRST, username: 'first', displayName: 'First'},
    {id: BOT_SECOND, username: 'second', displayName: 'Second'},
    {id: BOT_TRANSCRIPT, username: 'scribe', displayName: 'Scribe'},
];

describe('effectiveDefaultBotID', () => {
    it.each([
        {name: 'a configured ID', id: BOT_SECOND, legacy: '', expected: BOT_SECOND},
        {name: 'a configured ID that wins over a stale legacy name', id: BOT_SECOND, legacy: 'first', expected: BOT_SECOND},
        {name: 'a legacy name only', id: '', legacy: 'second', expected: BOT_SECOND},
        {name: 'a renamed agent still matched by ID', id: BOT_FIRST, legacy: 'old-name', expected: BOT_FIRST},
        {name: 'an empty default', id: '', legacy: '', expected: BOT_FIRST},
        {name: 'a removed agent ID', id: 'gone0000000000000000000000', legacy: '', expected: ''},
        {name: 'an unknown legacy name', id: '', legacy: 'gone', expected: ''},
    ])('resolves $name the way the server does', ({id, legacy, expected}) => {
        expect(effectiveDefaultBotID(id, legacy, runtimeBots)).toBe(expected);
    });

    it('returns an empty value when there are no agents', () => {
        expect(effectiveDefaultBotID('', '', [])).toBe('');
    });
});

describe('Config default agent selector', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        (getAIBots as jest.Mock).mockResolvedValue({bots: runtimeBots});
        (savePluginConfig as jest.Mock).mockImplementation((cfg) => Promise.resolve(cfg));
    });

    async function saveLastRegistered(registerSaveAction: jest.Mock) {
        const save = registerSaveAction.mock.calls.at(-1)?.[0] as SaveAction;
        await act(async () => {
            await save();
        });
        return (savePluginConfig as jest.Mock).mock.calls.at(-1)?.[0] as PluginConfig;
    }

    it('shows the agent matching a legacy default name and saves it unchanged for the server to convert', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, defaultBotName: 'second', defaultBotUserID: ''});
        const {registerSaveAction} = renderConfig();

        await screen.findByText('Second');
        expect(screen.queryByText('First')).toBeNull();

        const saved = await saveLastRegistered(registerSaveAction);
        expect(saved.defaultBotUserID).toBe('');
        expect(saved.defaultBotName).toBe('second');
    });

    it('saves a selection by ID and clears the legacy default name', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, defaultBotName: 'second', transcriptBackend: 'scribe'});
        const {registerSaveAction} = renderConfig();
        await screen.findByText('Second');

        fireEvent.click(screen.getByRole('combobox', {name: 'Default bot'}));
        fireEvent.pointerUp(await screen.findByRole('option', {name: 'First'}));

        const saved = await saveLastRegistered(registerSaveAction);
        expect(saved.defaultBotUserID).toBe(BOT_FIRST);
        expect(saved.defaultBotName).toBe('');
        expect(saved.transcriptBotUserID).toBe('');
        expect(saved.transcriptBackend).toBe('scribe');
    });

    it('flags a configured default agent that is not available', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, defaultBotUserID: 'gone0000000000000000000000'});
        renderConfig();

        await screen.findByText(/The configured default agent is not available/);
        expect(screen.queryByText('First')).toBeNull();
    });

    it('sends empty ID and legacy fields when nothing is configured', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue(loadedConfig);
        const {registerSaveAction} = renderConfig();
        await screen.findByText('First');

        const saved = await saveLastRegistered(registerSaveAction);
        expect(saved).toMatchObject({defaultBotUserID: '', defaultBotName: '', transcriptBotUserID: '', transcriptBackend: ''});
    });
});

describe('Config agent version retention', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        (getAIBots as jest.Mock).mockResolvedValue({bots: []});
        (savePluginConfig as jest.Mock).mockImplementation(async (cfg) => cfg);
    });

    async function saveAndGetConfig(registerSaveAction: jest.Mock): Promise<PluginConfig> {
        const save = registerSaveAction.mock.calls.at(-1)?.[0] as SaveAction;
        await act(async () => {
            await save();
        });
        return (savePluginConfig as jest.Mock).mock.calls.at(-1)[0];
    }

    function limitRadio(name: 'True' | 'False') {
        const group = screen.getByRole('radiogroup', {name: 'Limit agent version history'});
        return Array.from(group.querySelectorAll('label')).find((l) => l.textContent === name)!.querySelector('input')!;
    }

    it('keeps every version until a limit is turned on, then saves the number to keep', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue(loadedConfig);
        const {registerSaveAction} = renderConfig();
        await screen.findByText('service:My Service:unsaved');

        expect(limitRadio('False').checked).toBe(true);
        expect(screen.queryByLabelText('Versions to keep per agent')).toBeNull();

        fireEvent.click(limitRadio('True'));
        const count = screen.getByLabelText('Versions to keep per agent') as HTMLInputElement;
        expect(count.value).toBe('50');
        fireEvent.change(count, {target: {value: '10'}});
        expect((await saveAndGetConfig(registerSaveAction)).agentVersionsToKeep).toBe(10);

        fireEvent.change(count, {target: {value: '0'}});
        expect((await saveAndGetConfig(registerSaveAction)).agentVersionsToKeep).toBe(1);

        fireEvent.click(limitRadio('False'));
        expect(screen.queryByLabelText('Versions to keep per agent')).toBeNull();
        expect((await saveAndGetConfig(registerSaveAction)).agentVersionsToKeep).toBe(0);
    });

    it('shows a saved limit', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, agentVersionsToKeep: 25});
        renderConfig();
        await screen.findByText('service:My Service:unsaved');

        expect(limitRadio('True').checked).toBe(true);
        expect((screen.getByLabelText('Versions to keep per agent') as HTMLInputElement).value).toBe('25');
    });
});
