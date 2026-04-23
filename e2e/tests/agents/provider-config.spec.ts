// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test, expect, Page} from '@playwright/test';

import {AgentPageHelper} from 'helpers/agent-page';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {OpenAIMockContainer, RunOpenAIMocks} from 'helpers/openai-mock';
import RunSystemConsoleContainer, {adminUsername, adminPassword} from 'helpers/system-console-container';

const providerConfigTestTimeoutMs = 180000;

type ProviderFixtureConfig = Parameters<typeof RunSystemConsoleContainer>[0];

function createCompatibleService(overrides: Record<string, unknown> = {}) {
    return {
        id: 'compatible-service',
        name: 'Compatible Service',
        type: 'openaicompatible',
        apiKey: 'mock-compatible-key',
        apiURL: 'http://openai:8080',
        defaultModel: 'gpt-mock',
        tokenLimit: 16384,
        outputTokenLimit: 4096,
        streamingTimeoutSeconds: 30,
        useResponsesAPI: false,
        ...overrides,
    };
}

function createOpenAIService(overrides: Record<string, unknown> = {}) {
    return {
        id: 'openai-service',
        name: 'OpenAI Service',
        type: 'openai',
        apiKey: 'mock-openai-key',
        apiURL: 'http://openai:8080',
        defaultModel: 'gpt-mock',
        tokenLimit: 16384,
        outputTokenLimit: 4096,
        streamingTimeoutSeconds: 30,
        useResponsesAPI: false,
        ...overrides,
    };
}

function createAnthropicService(overrides: Record<string, unknown> = {}) {
    return {
        id: 'anthropic-service',
        name: 'Anthropic Service',
        type: 'anthropic',
        apiKey: 'mock-anthropic-key',
        apiURL: 'http://openai:8080',
        defaultModel: 'claude-3-7-sonnet',
        tokenLimit: 16384,
        outputTokenLimit: 8192,
        ...overrides,
    };
}

async function stubAgentModelFetch(page: Page): Promise<void> {
    await page.context().route(/\/plugins\/mattermost-ai\/agents\/models\/fetch(\?.*)?$/, async (route) => {
        if (route.request().method() !== 'POST') {
            await route.continue();
            return;
        }
        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: '[]',
        });
    });
}

test.describe('Agent provider configuration', () => {
    let mattermost: MattermostContainer | undefined;
    let openAIMock: OpenAIMockContainer | undefined;

    const startFixture = async (config: ProviderFixtureConfig): Promise<MattermostContainer> => {
        mattermost = await RunSystemConsoleContainer(config);
        openAIMock = await RunOpenAIMocks(mattermost.network);
        return mattermost;
    };

    test.afterEach(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
        openAIMock = undefined;
        mattermost = undefined;
    });

    test('shows validation errors for required fields and invalid usernames in the agent builder', async ({page}) => {
        test.setTimeout(providerConfigTestTimeoutMs);

        const mm = await startFixture({
            services: [
                createCompatibleService({
                    id: 'validation-service',
                    name: 'Validation Service',
                }),
            ],
            bots: [],
        });

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await stubAgentModelFetch(page);
        await mmPage.login(mm.url(), adminUsername, adminPassword);
        await agentPage.navigateToAgents(mm.url());
        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        await agentPage.getModalSaveButton().click();

        await expect(page.getByText('Display name is required')).toBeVisible({timeout: 10000});
        await expect(page.getByText('Username is required')).toBeVisible({timeout: 10000});
        await expect(page.getByText('AI Service is required')).toBeVisible({timeout: 10000});

        await agentPage.getDisplayNameInput().fill('Validation Agent');
        await agentPage.getUsernameInput().fill('Invalid Name');
        await agentPage.getAIServiceSelect().selectOption({label: 'Validation Service'});
        await agentPage.getModalSaveButton().click();

        await expect(page.getByText('Username must start with a letter and contain only lowercase letters, numbers, periods, hyphens, and underscores')).toBeVisible({
            timeout: 10000,
        });
    });

    test('creates direct OpenAI agents with native tools and structured output off by default', async ({page}) => {
        test.setTimeout(providerConfigTestTimeoutMs);

        const mm = await startFixture({
            services: [
                createOpenAIService({
                    id: 'openai-direct-service',
                    name: 'OpenAI Direct Service',
                    useResponsesAPI: false,
                }),
            ],
            bots: [],
        });

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await stubAgentModelFetch(page);
        await mmPage.login(mm.url(), adminUsername, adminPassword);
        await agentPage.navigateToAgents(mm.url());
        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        await agentPage.getDisplayNameInput().fill('Direct OpenAI Agent');
        await agentPage.getUsernameInput().fill('directopenaibot');
        await agentPage.getAIServiceSelect().selectOption({label: 'OpenAI Direct Service'});

        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();
        await expect(agentPage.getAgentRowByName('Direct OpenAI Agent')).toBeVisible({timeout: 15000});

        await agentPage.openAgentActions('Direct OpenAI Agent');
        await agentPage.clickEditAction('Direct OpenAI Agent');
        await agentPage.waitForModal();

        await expect(agentPage.getNativeToolsSection('Native OpenAI Tools')).toBeVisible({timeout: 10000});
        await expect(agentPage.getNativeToolCheckbox('Native OpenAI Tools')).toBeChecked();
        await expect(agentPage.getReasoningEffortSelect()).toBeVisible({timeout: 10000});
        await expect(agentPage.getBooleanFieldRadios('Structured Output').nth(1)).toBeChecked();
    });

    test('edits migrated OpenAI-compatible settings from the agent builder', async ({page}) => {
        test.setTimeout(providerConfigTestTimeoutMs);

        const mm = await startFixture({
            services: [
                createCompatibleService({
                    id: 'responses-service',
                    name: 'Responses Compatible Service',
                    useResponsesAPI: true,
                }),
                createCompatibleService({
                    id: 'plain-service',
                    name: 'Plain Compatible Service',
                    useResponsesAPI: false,
                }),
            ],
            bots: [
                {
                    id: 'legacy-openai-bot',
                    name: 'legacyresponsesbot',
                    displayName: 'Legacy Responses Agent',
                    serviceID: 'responses-service',
                    customInstructions: 'You are a helpful migration test agent.',
                    enableVision: true,
                    disableTools: false,
                    enabledNativeTools: ['web_search'],
                    reasoningEnabled: true,
                    reasoningEffort: 'high',
                },
            ],
        });

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await stubAgentModelFetch(page);
        await mmPage.login(mm.url(), adminUsername, adminPassword);
        await agentPage.navigateToAgents(mm.url());
        await expect(agentPage.getAgentRowByName('Legacy Responses Agent')).toBeVisible({timeout: 15000});

        await agentPage.openAgentActions('Legacy Responses Agent');
        await agentPage.clickEditAction('Legacy Responses Agent');
        await agentPage.waitForModal();

        await expect(agentPage.getNativeToolsSection('Native OpenAI Tools')).toBeVisible({timeout: 10000});
        await expect(agentPage.getNativeToolCheckbox('Native OpenAI Tools')).toBeChecked();
        await expect(agentPage.getReasoningEffortSelect()).toHaveValue('high');

        await agentPage.getAIServiceSelect().selectOption({label: 'Plain Compatible Service'});
        await expect(agentPage.getNativeToolsSection('Native OpenAI Tools')).toHaveCount(0);
        await expect(agentPage.getReasoningEffortSelect()).toHaveCount(0);

        await agentPage.getAIServiceSelect().selectOption({label: 'Responses Compatible Service'});
        await expect(agentPage.getNativeToolsSection('Native OpenAI Tools')).toBeVisible({timeout: 10000});
        await expect(agentPage.getReasoningEffortSelect()).toHaveValue('high');

        await agentPage.getNativeToolCheckbox('Native OpenAI Tools').click();
        await expect(agentPage.getNativeToolCheckbox('Native OpenAI Tools')).not.toBeChecked();
        await agentPage.getReasoningEffortSelect().selectOption('minimal');

        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        await agentPage.openAgentActions('Legacy Responses Agent');
        await agentPage.clickEditAction('Legacy Responses Agent');
        await agentPage.waitForModal();

        await expect(agentPage.getAIServiceSelect()).toHaveValue('responses-service');
        await expect(agentPage.getNativeToolsSection('Native OpenAI Tools')).toBeVisible({timeout: 10000});
        await expect(agentPage.getNativeToolCheckbox('Native OpenAI Tools')).not.toBeChecked();
        await expect(agentPage.getReasoningEffortSelect()).toHaveValue('minimal');
    });

    test('edits migrated Anthropic settings from the agent builder', async ({page}) => {
        test.setTimeout(providerConfigTestTimeoutMs);

        const mm = await startFixture({
            services: [
                createAnthropicService(),
            ],
            bots: [
                {
                    id: 'legacy-anthropic-bot',
                    name: 'legacyanthropicbot',
                    displayName: 'Legacy Anthropic Agent',
                    serviceID: 'anthropic-service',
                    customInstructions: 'You are a thoughtful migration test agent.',
                    enableVision: true,
                    disableTools: false,
                    enabledNativeTools: ['web_search'],
                    reasoningEnabled: true,
                    thinkingBudget: 2048,
                    structuredOutputEnabled: false,
                },
            ],
        });

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await stubAgentModelFetch(page);
        await mmPage.login(mm.url(), adminUsername, adminPassword);
        await agentPage.navigateToAgents(mm.url());
        await expect(agentPage.getAgentRowByName('Legacy Anthropic Agent')).toBeVisible({timeout: 15000});

        await agentPage.openAgentActions('Legacy Anthropic Agent');
        await agentPage.clickEditAction('Legacy Anthropic Agent');
        await agentPage.waitForModal();

        await expect(agentPage.getNativeToolsSection('Native Claude Tools')).toBeVisible({timeout: 10000});
        await expect(agentPage.getNativeToolCheckbox('Native Claude Tools')).toBeChecked();
        await expect(agentPage.getReasoningEnableCheckbox('Extended Thinking')).toBeChecked();
        await expect(agentPage.getThinkingBudgetInput()).toHaveValue('2048');

        await agentPage.getThinkingBudgetInput().fill('512');
        await page.keyboard.press('Tab');
        await expect(page.getByText('Thinking budget must be at least 1024 tokens.')).toBeVisible({timeout: 10000});

        await agentPage.getThinkingBudgetInput().fill('4096');
        await page.keyboard.press('Tab');
        await expect(page.getByText('Thinking budget must be at least 1024 tokens.')).not.toBeVisible();

        await agentPage.getNativeToolCheckbox('Native Claude Tools').click();
        await expect(agentPage.getNativeToolCheckbox('Native Claude Tools')).not.toBeChecked();

        await agentPage.setBooleanField('Structured Output', true);
        await expect(agentPage.getBooleanFieldRadios('Structured Output').nth(0)).toBeChecked();
        await expect(agentPage.getStructuredOutputNote()).toBeVisible({timeout: 10000});
        await expect(agentPage.getReasoningEnableCheckbox('Extended Thinking')).not.toBeChecked();
        await expect(agentPage.getThinkingBudgetInput()).toHaveCount(0);

        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        await agentPage.openAgentActions('Legacy Anthropic Agent');
        await agentPage.clickEditAction('Legacy Anthropic Agent');
        await agentPage.waitForModal();

        await expect(agentPage.getNativeToolsSection('Native Claude Tools')).toBeVisible({timeout: 10000});
        await expect(agentPage.getNativeToolCheckbox('Native Claude Tools')).not.toBeChecked();
        await expect(agentPage.getBooleanFieldRadios('Structured Output').nth(0)).toBeChecked();
        await expect(agentPage.getStructuredOutputNote()).toBeVisible({timeout: 10000});
        await expect(agentPage.getReasoningEnableCheckbox('Extended Thinking')).not.toBeChecked();
        await expect(agentPage.getThinkingBudgetInput()).toHaveCount(0);

        await agentPage.setBooleanField('Structured Output', false);
        await expect(agentPage.getBooleanFieldRadios('Structured Output').nth(1)).toBeChecked();
        await expect(agentPage.getStructuredOutputNote()).toHaveCount(0);
        await expect(agentPage.getReasoningEnableCheckbox('Extended Thinking')).toBeChecked();
        await expect(agentPage.getThinkingBudgetInput()).toHaveValue('4096');
    });
});
