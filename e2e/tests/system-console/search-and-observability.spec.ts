// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createHash, randomUUID} from 'crypto';

import {expect, test} from '@playwright/test';
import type {Client4} from '@mattermost/client';

import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {mattermostAIAdminConfigApiFromClient, PluginAdminConfigApi} from 'helpers/plugin-http';
import {SystemConsoleHelper} from 'helpers/system-console';
import RunSystemConsoleContainer, {
    adminPassword,
    adminUsername,
    SystemConsoleEmbeddingSearchConfig,
    SystemConsolePluginConfig,
    SystemConsoleWebSearchConfig,
} from 'helpers/system-console-container';

type PersistedSystemConsoleConfig = SystemConsolePluginConfig & {
    allowNativeWebSearchInChannels: boolean;
    embeddingSearchConfig: SystemConsoleEmbeddingSearchConfig;
    enableChannelMentionToolCalling: boolean;
    openTelemetryEndpoint: string;
    telemetryOutput: 'off' | 'logs' | 'otlp' | '';
    webSearch: SystemConsoleWebSearchConfig;
};

const disabledEmbeddingSearch: SystemConsoleEmbeddingSearchConfig = {
    type: '',
    vectorStore: {
        type: '',
        parameters: {},
    },
    embeddingProvider: {
        type: '',
        parameters: {},
    },
    parameters: {},
    dimensions: 0,
    chunkingOptions: {
        chunkSize: 1000,
        chunkOverlap: 200,
        chunkingStrategy: 'sentences',
    },
};

const disabledWebSearch: SystemConsoleWebSearchConfig = {
    enabled: false,
    provider: 'google',
    google: {
        apiKey: '',
        searchEngineId: '',
        resultLimit: 5,
        apiURL: '',
    },
    brave: {
        apiKey: '',
        resultLimit: 5,
        apiURL: '',
    },
    domainDenylist: [],
};

let mattermost: MattermostContainer;
let adminClient: Client4;
let configAPI: PluginAdminConfigApi;
let baselineConfig: Record<string, unknown>;

function persistedConfig(config: Record<string, unknown>): PersistedSystemConsoleConfig {
    return config as unknown as PersistedSystemConsoleConfig;
}

function secretFingerprint(value: unknown): string {
    return createHash('sha256').update(String(value)).digest('hex');
}

function embeddingConfigFingerprint(config: Record<string, unknown>): Record<string, unknown> {
    const embeddingConfig = persistedConfig(config).embeddingSearchConfig;
    const providerParameters = embeddingConfig.embeddingProvider.parameters ?? {};

    return {
        ...embeddingConfig,
        embeddingProvider: {
            ...embeddingConfig.embeddingProvider,
            parameters: {
                ...providerParameters,
                apiKey: secretFingerprint(providerParameters.apiKey),
            },
        },
    };
}

async function getReindexJobStatus(): Promise<string> {
    const response = await fetch(`${mattermost.url()}/plugins/mattermost-ai/admin/reindex/status`, {
        headers: {
            Authorization: `Bearer ${adminClient.getToken()}`,
        },
    });
    if (!response.ok && response.status !== 404) {
        throw new Error(`Failed to get reindex status: HTTP ${response.status}`);
    }

    const body = await response.json() as {status?: string};
    if (!body.status) {
        throw new Error('Reindex status response did not contain a status');
    }
    return body.status;
}

test.use({trace: 'off'});

test.describe('System Console search and observability persistence', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);

        mattermost = await RunSystemConsoleContainer({
            services: [{
                id: 'system-console-service',
                name: 'System Console Service',
                type: 'openai',
                apiKey: 'unused-system-console-service-key',
                apiURL: 'https://api.openai.invalid/v1',
                defaultModel: 'unused-model',
                tokenLimit: 8192,
                outputTokenLimit: 2048,
                streamingTimeoutSeconds: 30,
                useResponsesAPI: false,
            }],
            bots: [],
            embeddingSearchConfig: disabledEmbeddingSearch,
            webSearch: disabledWebSearch,
            enableChannelMentionToolCalling: false,
            allowNativeWebSearchInChannels: false,
            telemetryOutput: 'off',
            openTelemetryEndpoint: '',
        });

        adminClient = await mattermost.getClient(adminUsername, adminPassword);
        configAPI = mattermostAIAdminConfigApiFromClient(adminClient, mattermost.url());
        baselineConfig = await configAPI.get();
    });

    test.afterAll(async () => {
        await mattermost.stop();
    });

    test.beforeEach(async ({page}, testInfo) => {
        testInfo.setTimeout(120000);

        await configAPI.put(baselineConfig, {settleMs: 0});

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        const systemConsole = new SystemConsoleHelper(page);
        await systemConsole.navigateToPluginConfig(mattermost.url());
    });

    test('persists Brave web search and provider switching through UI and config API', async ({page}) => {
        const marker = randomUUID();
        const braveAPIKey = `e2e-brave-key-${marker}`;
        const braveAPIURL = `https://brave-${marker}.invalid/search`;
        const denylist = [`blocked-${marker}.invalid`, `ignored-${marker}.test`];
        const resultLimit = 17;
        const googleAPIKey = `e2e-google-key-${marker}`;
        const googleEngineID = `engine-${marker}`;
        const googleResultLimit = 23;
        const googleAPIURL = `https://google-${marker}.invalid/customsearch`;
        const systemConsole = new SystemConsoleHelper(page);

        await systemConsole.getBooleanRadio('Web Search', 'Enable Web Search', true).click();
        await systemConsole.getSelect('Web Search', 'Provider').selectOption('brave');
        await systemConsole.getInput('Web Search', 'Brave API Key').fill(braveAPIKey);
        await systemConsole.getInput('Web Search', 'Brave Result Limit').fill(resultLimit.toString());
        await systemConsole.getInput('Web Search', 'Brave API URL (optional)').fill(braveAPIURL);
        await systemConsole.getInput('Web Search', 'Domain Denylist (optional)').fill(denylist.join(', '));

        await systemConsole.clickSave();
        await systemConsole.reloadPluginConfig();

        await expect(systemConsole.getBooleanRadio('Web Search', 'Enable Web Search', true)).toBeChecked();
        await expect(systemConsole.getSelect('Web Search', 'Provider')).toHaveValue('brave');
        expect((await systemConsole.getInput('Web Search', 'Brave API Key').inputValue()) === braveAPIKey).toBe(true);
        await expect(systemConsole.getInput('Web Search', 'Brave Result Limit')).toHaveValue(resultLimit.toString());
        await expect(systemConsole.getInput('Web Search', 'Brave API URL (optional)')).toHaveValue(braveAPIURL);
        await expect(systemConsole.getInput('Web Search', 'Domain Denylist (optional)')).toHaveValue(denylist.join(', '));

        let apiConfig = persistedConfig(await configAPI.get());
        expect(apiConfig.webSearch.enabled).toBe(true);
        expect(apiConfig.webSearch.provider).toBe('brave');
        expect(apiConfig.webSearch.brave.apiKey === braveAPIKey).toBe(true);
        expect(apiConfig.webSearch.brave.resultLimit).toBe(resultLimit);
        expect(apiConfig.webSearch.brave.apiURL).toBe(braveAPIURL);
        expect(apiConfig.webSearch.domainDenylist).toEqual(denylist);

        await systemConsole.getSelect('Web Search', 'Provider').selectOption('google');
        await expect(systemConsole.getInput('Web Search', 'Google API Key')).toBeVisible();
        await expect(systemConsole.getInput('Web Search', 'Search Engine ID')).toBeVisible();
        await expect(systemConsole.getInput('Web Search', 'Brave API Key')).not.toBeVisible();
        await expect(systemConsole.getInput('Web Search', 'Brave Result Limit')).not.toBeVisible();
        await expect(systemConsole.getBooleanRadio('Web Search', 'Enable Web Search', true)).toBeChecked();
        await expect(systemConsole.getInput('Web Search', 'Domain Denylist (optional)')).toHaveValue(denylist.join(', '));

        await systemConsole.getInput('Web Search', 'Google API Key').fill(googleAPIKey);
        await systemConsole.getInput('Web Search', 'Search Engine ID').fill(googleEngineID);
        await systemConsole.getInput('Web Search', 'Result Limit').fill(googleResultLimit.toString());
        await systemConsole.getInput('Web Search', 'API URL (optional)').fill(googleAPIURL);
        await systemConsole.clickSave();
        await systemConsole.reloadPluginConfig();

        await expect(systemConsole.getSelect('Web Search', 'Provider')).toHaveValue('google');
        expect((await systemConsole.getInput('Web Search', 'Google API Key').inputValue()) === googleAPIKey).toBe(true);
        await expect(systemConsole.getInput('Web Search', 'Search Engine ID')).toHaveValue(googleEngineID);
        await expect(systemConsole.getInput('Web Search', 'Result Limit')).toHaveValue(googleResultLimit.toString());
        await expect(systemConsole.getInput('Web Search', 'API URL (optional)')).toHaveValue(googleAPIURL);
        await expect(systemConsole.getInput('Web Search', 'Brave API Key')).not.toBeVisible();
        await expect(systemConsole.getBooleanRadio('Web Search', 'Enable Web Search', true)).toBeChecked();
        await expect(systemConsole.getInput('Web Search', 'Domain Denylist (optional)')).toHaveValue(denylist.join(', '));

        apiConfig = persistedConfig(await configAPI.get());
        expect(apiConfig.webSearch.enabled).toBe(true);
        expect(apiConfig.webSearch.provider).toBe('google');
        expect(apiConfig.webSearch.domainDenylist).toEqual(denylist);
        expect(apiConfig.webSearch.google.apiKey === googleAPIKey).toBe(true);
        expect(apiConfig.webSearch.google.searchEngineId).toBe(googleEngineID);
        expect(apiConfig.webSearch.google.resultLimit).toBe(googleResultLimit);
        expect(apiConfig.webSearch.google.apiURL).toBe(googleAPIURL);
        expect(apiConfig.webSearch.brave.apiKey === braveAPIKey).toBe(true);
        expect(apiConfig.webSearch.brave.resultLimit).toBe(resultLimit);
        expect(apiConfig.webSearch.brave.apiURL).toBe(braveAPIURL);

        await systemConsole.getBooleanRadio('Web Search', 'Enable Web Search', false).click();
        await systemConsole.clickSave();
        await systemConsole.reloadPluginConfig();

        await expect(systemConsole.getBooleanRadio('Web Search', 'Enable Web Search', false)).toBeChecked();
        await expect(systemConsole.getSelect('Web Search', 'Provider')).toHaveValue('google');
        await expect(systemConsole.getSelect('Web Search', 'Provider')).toBeDisabled();
        expect((await systemConsole.getInput('Web Search', 'Google API Key').inputValue()) === googleAPIKey).toBe(true);
        await expect(systemConsole.getInput('Web Search', 'Search Engine ID')).toHaveValue(googleEngineID);
        await expect(systemConsole.getInput('Web Search', 'Result Limit')).toHaveValue(googleResultLimit.toString());
        await expect(systemConsole.getInput('Web Search', 'API URL (optional)')).toHaveValue(googleAPIURL);
        await expect(systemConsole.getInput('Web Search', 'Domain Denylist (optional)')).toHaveValue(denylist.join(', '));

        apiConfig = persistedConfig(await configAPI.get());
        expect(apiConfig.webSearch.enabled).toBe(false);
        expect(apiConfig.webSearch.google.apiKey === googleAPIKey).toBe(true);
        expect(apiConfig.webSearch.brave.apiKey === braveAPIKey).toBe(true);

        await systemConsole.getBooleanRadio('Web Search', 'Enable Web Search', true).click();
        await systemConsole.clickSave();
        await systemConsole.reloadPluginConfig();

        await expect(systemConsole.getBooleanRadio('Web Search', 'Enable Web Search', true)).toBeChecked();
        await expect(systemConsole.getSelect('Web Search', 'Provider')).toHaveValue('google');
        expect((await systemConsole.getInput('Web Search', 'Google API Key').inputValue()) === googleAPIKey).toBe(true);
        await expect(systemConsole.getInput('Web Search', 'Search Engine ID')).toHaveValue(googleEngineID);
        await expect(systemConsole.getInput('Web Search', 'Result Limit')).toHaveValue(googleResultLimit.toString());
        await expect(systemConsole.getInput('Web Search', 'API URL (optional)')).toHaveValue(googleAPIURL);
        await expect(systemConsole.getInput('Web Search', 'Domain Denylist (optional)')).toHaveValue(denylist.join(', '));

        apiConfig = persistedConfig(await configAPI.get());
        expect(apiConfig.webSearch.enabled).toBe(true);
        expect(apiConfig.webSearch.google.apiKey === googleAPIKey).toBe(true);
        expect(apiConfig.webSearch.brave.apiKey === braveAPIKey).toBe(true);
    });

    test('persists embedding search and dismisses full-reindex confirmation without starting a job', async ({page}) => {
        const marker = randomUUID();
        const embeddingAPIKey = `e2e-embedding-key-${marker}`;
        const embeddingModel = `embedding-model-${marker}`;
        const embeddingAPIURL = `http://embedding-${marker}.invalid/v1`;
        const dimensions = 1024;
        const chunkSize = 640;
        const chunkOverlap = 64;
        const systemConsole = new SystemConsoleHelper(page);

        await systemConsole.getBooleanRadio('Embedding Search', 'Enable Embedding Search', true).click();
        await systemConsole.getSelect('Embedding Search', 'Embedding Provider Type').selectOption('openai-compatible');
        await systemConsole.getInput('Embedding Search', 'API Key').fill(embeddingAPIKey);
        await systemConsole.getInput('Embedding Search', 'Model').fill(embeddingModel);
        await systemConsole.getInput('Embedding Search', 'API URL').fill(embeddingAPIURL);
        await systemConsole.getInput('Embedding Search', 'Dimensions').fill(dimensions.toString());
        await systemConsole.getSelect('Embedding Search', 'Chunking Strategy').selectOption('paragraphs');
        await systemConsole.getInput('Embedding Search', 'Chunk Size').fill(chunkSize.toString());
        await systemConsole.getInput('Embedding Search', 'Chunk Overlap').fill(chunkOverlap.toString());

        await systemConsole.clickSave();
        await systemConsole.reloadPluginConfig();

        await expect(systemConsole.getBooleanRadio('Embedding Search', 'Enable Embedding Search', true)).toBeChecked();
        await expect(systemConsole.getSelect('Embedding Search', 'Embedding Provider Type')).toHaveValue('openai-compatible');
        expect((await systemConsole.getInput('Embedding Search', 'API Key').inputValue()) === embeddingAPIKey).toBe(true);
        await expect(systemConsole.getInput('Embedding Search', 'Model')).toHaveValue(embeddingModel);
        await expect(systemConsole.getInput('Embedding Search', 'API URL')).toHaveValue(embeddingAPIURL);
        await expect(systemConsole.getInput('Embedding Search', 'Dimensions')).toHaveValue(dimensions.toString());
        await expect(systemConsole.getSelect('Embedding Search', 'Chunking Strategy')).toHaveValue('paragraphs');
        await expect(systemConsole.getInput('Embedding Search', 'Chunk Size')).toHaveValue(chunkSize.toString());
        await expect(systemConsole.getInput('Embedding Search', 'Chunk Overlap')).toHaveValue(chunkOverlap.toString());

        const configured = persistedConfig(await configAPI.get()).embeddingSearchConfig;
        const providerParameters = configured.embeddingProvider.parameters ?? {};
        expect(configured.type).toBe('composite');
        expect(configured.vectorStore.type).toBe('pgvector');
        expect(configured.embeddingProvider.type).toBe('openai-compatible');
        expect(providerParameters.apiKey === embeddingAPIKey).toBe(true);
        expect(providerParameters.embeddingModel).toBe(embeddingModel);
        expect(providerParameters.apiURL).toBe(embeddingAPIURL);
        expect(configured.dimensions).toBe(dimensions);
        expect(configured.chunkingOptions).toEqual({
            chunkSize,
            chunkOverlap,
            chunkingStrategy: 'paragraphs',
        });

        const configBeforeCancel = await configAPI.get();
        expect(await getReindexJobStatus()).toBe('no_job');

        await systemConsole.getPanel('Embedding Search').getByRole('button', {name: 'Full Reindex'}).click();
        const confirmation = page.getByRole('dialog', {name: 'Confirm Reindexing'});
        await expect(confirmation).toBeVisible();
        await expect(confirmation.getByText(/Incur API costs from your embedding provider/i)).toBeVisible();
        await confirmation.getByRole('button', {name: 'Cancel'}).click();
        await expect(confirmation).not.toBeVisible();

        expect(await getReindexJobStatus()).toBe('no_job');
        const configAfterCancel = await configAPI.get();
        expect(embeddingConfigFingerprint(configAfterCancel)).toEqual(embeddingConfigFingerprint(configBeforeCancel));
    });

    test('persists tracing configuration visibility and channel flags', async ({page}) => {
        const otlpEndpoint = 'localhost:4317';
        const systemConsole = new SystemConsoleHelper(page);

        // This verifies configuration and conditional visibility, not operational exporter emission.
        await systemConsole.getSelect('Debug', 'Trace Output').selectOption('otlp');
        await systemConsole.getInput('Debug', 'OpenTelemetry Endpoint').fill(otlpEndpoint);
        await systemConsole.getBooleanRadio('AI Functions', 'Enable Channel Mention Tool Calling', true).click();
        await systemConsole.getBooleanRadio('AI Functions', 'Allow native web search in channels', true).click();

        await systemConsole.clickSave();
        await systemConsole.reloadPluginConfig();

        await expect(systemConsole.getSelect('Debug', 'Trace Output')).toHaveValue('otlp');
        await expect(systemConsole.getInput('Debug', 'OpenTelemetry Endpoint')).toHaveValue(otlpEndpoint);
        await expect(systemConsole.getBooleanRadio('AI Functions', 'Enable Channel Mention Tool Calling', true)).toBeChecked();
        await expect(systemConsole.getBooleanRadio('AI Functions', 'Allow native web search in channels', true)).toBeChecked();

        let apiConfig = persistedConfig(await configAPI.get());
        expect(apiConfig.telemetryOutput).toBe('otlp');
        expect(apiConfig.openTelemetryEndpoint).toBe(otlpEndpoint);
        expect(apiConfig.enableChannelMentionToolCalling).toBe(true);
        expect(apiConfig.allowNativeWebSearchInChannels).toBe(true);

        await systemConsole.getSelect('Debug', 'Trace Output').selectOption('logs');
        await expect(systemConsole.getInput('Debug', 'OpenTelemetry Endpoint')).not.toBeVisible();
        await systemConsole.clickSave();
        await systemConsole.reloadPluginConfig();

        await expect(systemConsole.getSelect('Debug', 'Trace Output')).toHaveValue('logs');
        await expect(systemConsole.getInput('Debug', 'OpenTelemetry Endpoint')).not.toBeVisible();
        await expect(systemConsole.getBooleanRadio('AI Functions', 'Enable Channel Mention Tool Calling', true)).toBeChecked();
        await expect(systemConsole.getBooleanRadio('AI Functions', 'Allow native web search in channels', true)).toBeChecked();

        apiConfig = persistedConfig(await configAPI.get());
        expect(apiConfig.telemetryOutput).toBe('logs');
        expect(apiConfig.openTelemetryEndpoint).toBe(otlpEndpoint);
        expect(apiConfig.enableChannelMentionToolCalling).toBe(true);
        expect(apiConfig.allowNativeWebSearchInChannels).toBe(true);

        await systemConsole.getSelect('Debug', 'Trace Output').selectOption('otlp');
        await expect(systemConsole.getInput('Debug', 'OpenTelemetry Endpoint')).toBeVisible();
        await expect(systemConsole.getInput('Debug', 'OpenTelemetry Endpoint')).toHaveValue(otlpEndpoint);
        await systemConsole.getBooleanRadio('AI Functions', 'Enable Channel Mention Tool Calling', false).click();
        await systemConsole.getBooleanRadio('AI Functions', 'Allow native web search in channels', false).click();
        await systemConsole.clickSave();
        await systemConsole.reloadPluginConfig();

        await expect(systemConsole.getSelect('Debug', 'Trace Output')).toHaveValue('otlp');
        await expect(systemConsole.getInput('Debug', 'OpenTelemetry Endpoint')).toHaveValue(otlpEndpoint);
        await expect(systemConsole.getBooleanRadio('AI Functions', 'Enable Channel Mention Tool Calling', false)).toBeChecked();
        await expect(systemConsole.getBooleanRadio('AI Functions', 'Allow native web search in channels', false)).toBeChecked();

        apiConfig = persistedConfig(await configAPI.get());
        expect(apiConfig.telemetryOutput).toBe('otlp');
        expect(apiConfig.openTelemetryEndpoint).toBe(otlpEndpoint);
        expect(apiConfig.enableChannelMentionToolCalling).toBe(false);
        expect(apiConfig.allowNativeWebSearchInChannels).toBe(false);
    });
});
