// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getAIBots, getPluginConfig, savePluginConfig} from '@/client';

import Config from './config';

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

jest.mock('@/client', () => ({
    getPluginConfig: jest.fn(),
    getAIBots: jest.fn(),
    savePluginConfig: jest.fn(),
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

jest.mock('./mcp_servers', () => ({
    __esModule: true,
    default: ({mcpConfig}: {mcpConfig: {servers: Array<{id?: string; name: string}> | null}}) => (
        <div>
            {(mcpConfig.servers ?? []).map((s, i) => (
                <span key={i}>{`mcp:${s.name}:${s.id || 'unsaved'}`}</span>
            ))}
        </div>
    ),
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
});
