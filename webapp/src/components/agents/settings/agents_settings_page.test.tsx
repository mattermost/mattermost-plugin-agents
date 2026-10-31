// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getAIBots, getMySettingsAccess, getPluginConfig, savePluginConfig} from '@/client';
import {PluginConfig} from '@/components/system_console/plugin_config_types';
import {useSettingsReadOnly} from '@/components/system_console/settings_read_only';

import AgentsSettingsPage from './agents_settings_page';
import {SettingsSection} from './sections';
import {effectiveDefaultBotID} from './settings_sections';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');

    // A stable intl instance: the page keys effects on [intl], and a fresh
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

jest.mock('@/client', () => ({
    getMySettingsAccess: jest.fn(),
    getPluginConfig: jest.fn(),
    getAIBots: jest.fn(),
    savePluginConfig: jest.fn(),
}));

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

type MockService = {id: string; name: string};
const mockServices: {onChange?: (services: MockService[]) => void} = {};

// The heavy editors are not under test; expose the identity each entry is
// rendered with so ID propagation from the save response is observable, and
// whether they render read-only.
const ReadOnlyMarker = () => (useSettingsReadOnly() ? <span>{'read-only editors'}</span> : null);

jest.mock('@/components/system_console/services', () => ({
    __esModule: true,
    default: ({services, onChange}: {services: MockService[]; onChange: (services: MockService[]) => void}) => {
        mockServices.onChange = onChange;
        return (
            <div>
                <ReadOnlyMarker/>
                {services.map((s, i) => (
                    <span key={i}>{`service:${s.name}:${s.id || 'unsaved'}`}</span>
                ))}
            </div>
        );
    },
    firstNewService: {id: '', name: 'OpenAI Service'},
}));

type MockMCPConfig = {servers: Array<{id?: string; name: string}> | null};
const mockMCPServers: {onChange?: (update: MockMCPConfig | ((current: MockMCPConfig) => MockMCPConfig)) => void} = {};

jest.mock('@/components/system_console/mcp_servers', () => ({
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

jest.mock('@/components/system_console/embedding_search/embedding_search_panel', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('@/components/system_console/web_search/web_search_panel', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('@/components/system_console/no_services_page', () => ({
    __esModule: true,
    default: ({onAddServicePressed}: {onAddServicePressed?: () => void}) => (
        <div>
            {'no-services'}
            {onAddServicePressed && <button onClick={onAddServicePressed}>{'Add an AI Service'}</button>}
        </div>
    ),
}));

const loadedConfig = {
    services: [{id: '', name: 'My Service', type: 'openai'}],
    mcp: {
        enabled: true,
        enablePluginServer: false,
        servers: [{name: 'Jira', enabled: true, baseURL: 'https://jira.example.com', headers: {}}],
        embeddedServer: {enabled: true},
    },
};

function renderPage(section: SettingsSection = 'general') {
    const onSelectSection = jest.fn();
    const result = render(
        <IntlProvider locale='en'>
            <AgentsSettingsPage
                section={section}
                onSelectSection={onSelectSection}
            />
        </IntlProvider>,
    );
    return {...result, onSelectSection};
}

async function clickSave(): Promise<PluginConfig> {
    await act(async () => {
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));
    });
    return (savePluginConfig as jest.Mock).mock.calls.at(-1)?.[0] as PluginConfig;
}

function radioFor(label: string, value: 'true' | 'false'): HTMLInputElement {
    const group = screen.getByRole('radiogroup', {name: label});
    return group.querySelector(`input[value="${value}"]`) as HTMLInputElement;
}

beforeEach(() => {
    jest.clearAllMocks();
    (getMySettingsAccess as jest.Mock).mockResolvedValue('write');
    (getPluginConfig as jest.Mock).mockResolvedValue(loadedConfig);
    (getAIBots as jest.Mock).mockResolvedValue({bots: []});
    (savePluginConfig as jest.Mock).mockImplementation(async (cfg) => cfg);
});

describe('AgentsSettingsPage access', () => {
    it('tells a user without access so and does not load the configuration', async () => {
        (getMySettingsAccess as jest.Mock).mockResolvedValue('none');
        renderPage();

        await screen.findByText("You don't have access to the Agents settings");
        expect(getPluginConfig).not.toHaveBeenCalled();
    });

    it('shows read-only users the settings with every editor read-only and nothing to save', async () => {
        (getMySettingsAccess as jest.Mock).mockResolvedValue('read');
        renderPage('services');

        await screen.findByText('service:My Service:unsaved');
        expect(screen.getByText('You have read-only access')).toBeTruthy();
        expect(screen.getByText('read-only editors')).toBeTruthy();

        act(() => {
            mockServices.onChange?.([{id: '', name: 'Renamed'}]);
        });
        expect(screen.queryByRole('button', {name: 'Save'})).toBeNull();
    });

    it('disables the general settings for read-only users', async () => {
        (getMySettingsAccess as jest.Mock).mockResolvedValue('read');
        renderPage('general');

        await screen.findByText('Render AI-generated links');
        expect(radioFor('Render AI-generated links', 'true').disabled).toBe(true);
        expect(radioFor('Render AI-generated links', 'false').disabled).toBe(true);
    });

    it('offers to add the first service only to users who can save it', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, services: []});
        (getMySettingsAccess as jest.Mock).mockResolvedValue('read');
        const {unmount} = renderPage();
        await screen.findByText('no-services');
        expect(screen.queryByRole('button', {name: 'Add an AI Service'})).toBeNull();
        unmount();

        (getMySettingsAccess as jest.Mock).mockResolvedValue('write');
        const {onSelectSection} = renderPage();
        fireEvent.click(await screen.findByRole('button', {name: 'Add an AI Service'}));
        expect(onSelectSection).toHaveBeenCalledWith('services');
    });
});

describe('AgentsSettingsPage save flow', () => {
    it('saves only after a change, then adopts server-minted IDs from the response', async () => {
        renderPage('services');
        await screen.findByText('service:My Service:unsaved');
        expect(screen.queryByRole('button', {name: 'Save'})).toBeNull();

        act(() => {
            mockServices.onChange?.([{id: '', name: 'My Service'}, {id: '', name: 'Second'}]);
        });
        (savePluginConfig as jest.Mock).mockImplementation(async (cfg: PluginConfig) => ({
            ...cfg,
            services: (cfg.services ?? []).map((s, i) => ({...s, id: `serviceid${i}aaaaaaaaaaaaaaaa`})),
        }));
        await clickSave();

        expect(savePluginConfig).toHaveBeenCalledTimes(1);
        await screen.findByText('service:My Service:serviceid0aaaaaaaaaaaaaaaa');
        expect(screen.getByText('service:Second:serviceid1aaaaaaaaaaaaaaaa')).toBeTruthy();
        expect(screen.queryByRole('button', {name: 'Save'})).toBeNull();
    });

    it('reports a save error and keeps local state when the save is rejected', async () => {
        renderPage('services');
        await screen.findByText('service:My Service:unsaved');
        act(() => {
            mockServices.onChange?.([{id: '', name: 'Renamed'}]);
        });

        (savePluginConfig as jest.Mock).mockRejectedValue(new Error('409'));
        await clickSave();

        expect(screen.getByText('Failed to save configuration.')).toBeTruthy();
        expect(screen.getByText('service:Renamed:unsaved')).toBeTruthy();
        expect(screen.getByRole('button', {name: 'Save'})).toBeTruthy();
    });

    it('shows an inline error when the save is rejected for invalid allowed link domains, until the field is edited', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, allowUnsafeLinks: true, allowedLinkDomains: 'not a host'});
        renderPage('general');
        const domains = await screen.findByLabelText('Allowed Link Domains (csv)');
        fireEvent.change(domains, {target: {value: 'still not a host'}});

        (savePluginConfig as jest.Mock).mockRejectedValue(Object.assign(new Error(''), {server_error_id: 'invalid_allowed_link_domains'}));
        await clickSave();

        expect(screen.getByText('Failed to save configuration.')).toBeTruthy();
        expect(await screen.findByText('One or more entries are not valid domains.')).toBeTruthy();

        fireEvent.change(domains, {target: {value: 'example.com'}});
        expect(screen.queryByText('One or more entries are not valid domains.')).toBeNull();
    });

    it('keeps the editors read-only while a save is pending, so no edit is lost to its response', async () => {
        renderPage('services');
        await screen.findByText('service:My Service:unsaved');
        act(() => {
            mockServices.onChange?.([{id: '', name: 'Renamed'}]);
        });
        expect(screen.queryByText('read-only editors')).toBeNull();

        let finish: (cfg: PluginConfig) => void = () => {/* set below */};
        (savePluginConfig as jest.Mock).mockImplementation((cfg: PluginConfig) => new Promise((resolve) => {
            finish = () => resolve(cfg);
        }));
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));
        expect(await screen.findByText('read-only editors')).toBeTruthy();

        await act(async () => finish({} as PluginConfig));
        expect(screen.queryByText('read-only editors')).toBeNull();
    });

    it('asks before leaving the settings with unsaved changes, but not when switching sections', async () => {
        const unblock = jest.fn();
        const block = jest.fn().mockReturnValue(unblock);
        window.WebappUtils = {browserHistory: {push: jest.fn(), block}} as any;
        try {
            renderPage('services');
            await screen.findByText('service:My Service:unsaved');
            expect(block).not.toHaveBeenCalled();

            act(() => {
                mockServices.onChange?.([{id: '', name: 'Renamed'}]);
            });
            expect(block).toHaveBeenCalledTimes(1);
            const prompt = block.mock.calls[0][0] as (location: {pathname: string}) => string | undefined;
            expect(prompt({pathname: '/plug/mattermost-ai/agents/all'})).toBe('You have unsaved changes to the Agents settings. Leave without saving?');
            expect(prompt({pathname: '/plug/mattermost-ai/agents/settings/mcp'})).toBeUndefined();

            await clickSave();
            expect(unblock).toHaveBeenCalledTimes(1);
        } finally {
            delete window.WebappUtils;
        }
    });

    it('discards changes back to the saved configuration', async () => {
        renderPage('services');
        await screen.findByText('service:My Service:unsaved');
        act(() => {
            mockServices.onChange?.([{id: '', name: 'Renamed'}]);
        });

        fireEvent.click(screen.getByRole('button', {name: 'Discard'}));
        expect(screen.getByText('service:My Service:unsaved')).toBeTruthy();
        expect(screen.queryByRole('button', {name: 'Save'})).toBeNull();
    });

    it('keeps unsaved changes when switching sections', async () => {
        const {rerender} = renderPage('services');
        await screen.findByText('service:My Service:unsaved');
        act(() => {
            mockServices.onChange?.([{id: '', name: 'Renamed'}]);
        });

        const sectionPage = (section: SettingsSection) => (
            <IntlProvider locale='en'>
                <AgentsSettingsPage
                    section={section}
                    onSelectSection={jest.fn()}
                />
            </IntlProvider>
        );
        rerender(sectionPage('debug'));
        rerender(sectionPage('services'));
        expect(screen.getByText('service:Renamed:unsaved')).toBeTruthy();
    });

    it('applies an MCP update function to the configuration as it is when the update lands', async () => {
        renderPage('mcp');
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

describe('AgentsSettingsPage license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    afterEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    it('keeps token logging and native web search settings visible below their minimum and allows turning them off', async () => {
        useIsLicensedFor.mockReturnValue(false);
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, enableTokenUsageLogging: true, allowNativeWebSearchInChannels: true});

        const {unmount} = renderPage('debug');
        await screen.findByText('Enable Token Usage Logging');
        expect(radioFor('Enable Token Usage Logging', 'true').disabled).toBe(true);
        expect(radioFor('Enable Token Usage Logging', 'false').disabled).toBe(false);
        unmount();

        renderPage('general');
        await screen.findByText('Allow native web search in channels');
        expect(radioFor('Allow native web search in channels', 'true').disabled).toBe(true);
        expect(radioFor('Allow native web search in channels', 'false').disabled).toBe(false);
    });

    it('allows enabling token logging and native web search settings when licensed', async () => {
        const {unmount} = renderPage('debug');
        await screen.findByText('Enable Token Usage Logging');
        expect(radioFor('Enable Token Usage Logging', 'true').disabled).toBe(false);
        unmount();

        renderPage('general');
        await screen.findByText('Allow native web search in channels');
        expect(radioFor('Allow native web search in channels', 'true').disabled).toBe(false);
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

describe('AgentsSettingsPage default agent selector', () => {
    beforeEach(() => {
        (getAIBots as jest.Mock).mockResolvedValue({bots: runtimeBots});
    });

    // An unrelated change, so the Save bar appears without touching the default agent.
    const changeSomethingElse = () => fireEvent.click(radioFor('Render AI-generated links', 'true'));

    it('shows the agent matching a legacy default name and saves it unchanged for the server to convert', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, defaultBotName: 'second', defaultBotUserID: ''});
        renderPage();

        await screen.findByText('Second');
        expect(screen.queryByText('First')).toBeNull();

        changeSomethingElse();
        const saved = await clickSave();
        expect(saved.defaultBotUserID).toBe('');
        expect(saved.defaultBotName).toBe('second');
    });

    it('saves a selection by ID and clears the legacy default name', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, defaultBotName: 'second', transcriptBackend: 'scribe'});
        renderPage();
        await screen.findByText('Second');

        fireEvent.click(screen.getByRole('combobox', {name: 'Default bot'}));
        fireEvent.pointerUp(await screen.findByRole('option', {name: 'First'}));

        const saved = await clickSave();
        expect(saved.defaultBotUserID).toBe(BOT_FIRST);
        expect(saved.defaultBotName).toBe('');
        expect(saved.transcriptBotUserID).toBe('');
        expect(saved.transcriptBackend).toBe('scribe');
    });

    it('flags a configured default agent that is not available', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, defaultBotUserID: 'gone0000000000000000000000'});
        renderPage();

        await screen.findByText(/The configured default agent is not available/);
        expect(screen.queryByText('First')).toBeNull();
    });

    it('sends empty ID and legacy fields when nothing is configured', async () => {
        renderPage();
        await screen.findByText('First');

        changeSomethingElse();
        const saved = await clickSave();
        expect(saved).toMatchObject({defaultBotUserID: '', defaultBotName: '', transcriptBotUserID: '', transcriptBackend: ''});
    });
});

describe('AgentsSettingsPage agent version retention', () => {
    function limitRadio(name: 'True' | 'False') {
        const group = screen.getByRole('radiogroup', {name: 'Limit agent version history'});
        return Array.from(group.querySelectorAll('label')).find((l) => l.textContent === name)!.querySelector('input')!;
    }

    it('keeps every version until a limit is turned on, then saves the number to keep', async () => {
        renderPage();
        await screen.findByText('Limit agent version history');

        expect(limitRadio('False').checked).toBe(true);
        expect(screen.queryByLabelText('Versions to keep per agent')).toBeNull();

        fireEvent.click(limitRadio('True'));
        const count = screen.getByLabelText('Versions to keep per agent') as HTMLInputElement;
        expect(count.value).toBe('50');
        fireEvent.change(count, {target: {value: '10'}});
        expect((await clickSave()).agentVersionsToKeep).toBe(10);

        fireEvent.change(count, {target: {value: '0'}});
        expect((await clickSave()).agentVersionsToKeep).toBe(1);

        fireEvent.click(limitRadio('False'));
        expect(screen.queryByLabelText('Versions to keep per agent')).toBeNull();
        expect((await clickSave()).agentVersionsToKeep).toBe(0);
    });

    it('shows a saved limit', async () => {
        (getPluginConfig as jest.Mock).mockResolvedValue({...loadedConfig, agentVersionsToKeep: 25});
        renderPage();
        await waitFor(() => expect(limitRadio('True').checked).toBe(true));
        expect((screen.getByLabelText('Versions to keep per agent') as HTMLInputElement).value).toBe('25');
    });
});
