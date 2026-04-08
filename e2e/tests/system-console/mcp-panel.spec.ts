// spec: system-console-mcp-panel.plan.md - MCP Panel
// seed: e2e/tests/seed.spec.ts

import {test, expect} from '@playwright/test';

import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {SystemConsoleHelper} from 'helpers/system-console';
import {OpenAIMockContainer, RunOpenAIMocks} from 'helpers/openai-mock';
import RunSystemConsoleContainer, {adminUsername, adminPassword} from 'helpers/system-console-container';

let mattermost: MattermostContainer | undefined;
let openAIMock: OpenAIMockContainer | undefined;

test.describe.serial('MCP Panel', () => {
    test('should keep Connection Idle Timeout empty when cleared', async ({page}) => {
        test.setTimeout(60000);

        try {
            mattermost = await RunSystemConsoleContainer({
                mcp: {
                    enabled: true,
                    enablePluginServer: false,
                    idleTimeoutMinutes: 30,
                },
                services: [
                    {
                        id: 'test-service',
                        name: 'Test Service',
                        type: 'openai',
                        apiKey: 'test-key',
                        orgId: '',
                        defaultModel: 'gpt-4',
                        tokenLimit: 16384,
                        streamingTimeoutSeconds: 30,
                        sendUserId: false,
                        outputTokenLimit: 4096,
                        useResponsesAPI: false,
                    },
                ],
                bots: [
                    {
                        id: 'bot-1',
                        name: 'testbot',
                        displayName: 'Test Bot',
                        serviceID: 'test-service',
                        customInstructions: 'You are a helpful assistant',
                        enableVision: false,
                        enableTools: false,
                    },
                ],
                defaultBotName: 'testbot',
            });
            openAIMock = await RunOpenAIMocks(mattermost.network);

            const mmPage = new MattermostPage(page);
            const systemConsole = new SystemConsoleHelper(page);

            await mmPage.login(mattermost.url(), adminUsername, adminPassword);
            await systemConsole.navigateToPluginConfig(mattermost.url());

            const timeoutField = page.getByLabel(/Connection Idle Timeout \(minutes\)/i).or(
                page.locator('text=Connection Idle Timeout (minutes)').locator('..').getByRole('spinbutton'),
            );
            await timeoutField.scrollIntoViewIfNeeded();
            await expect(timeoutField).toHaveValue('30');

            await timeoutField.fill('');
            await expect(timeoutField).toHaveValue('');

            await systemConsole.clickSave();
            await page.reload();

            const reloadedTimeoutField = page.getByLabel(/Connection Idle Timeout \(minutes\)/i).or(
                page.locator('text=Connection Idle Timeout (minutes)').locator('..').getByRole('spinbutton'),
            );
            await expect(reloadedTimeoutField).toHaveValue('');
        } finally {
            if (openAIMock) {
                await openAIMock.stop();
            }
            if (mattermost) {
                await mattermost.stop();
            }
        }
    });

    test('should persist service account fallback headers on a remote MCP server', async ({page}) => {
        test.setTimeout(120000);

        try {
            mattermost = await RunSystemConsoleContainer({
                mcp: {
                    enabled: true,
                    enablePluginServer: false,
                    idleTimeoutMinutes: 30,
                    servers: [
                        {
                            name: 'E2E MCP Server',
                            enabled: true,
                            baseURL: 'https://mcp.e2e.test/v1',
                        },
                    ],
                },
                services: [
                    {
                        id: 'test-service',
                        name: 'Test Service',
                        type: 'openai',
                        apiKey: 'test-key',
                        orgId: '',
                        defaultModel: 'gpt-4',
                        tokenLimit: 16384,
                        streamingTimeoutSeconds: 30,
                        sendUserId: false,
                        outputTokenLimit: 4096,
                        useResponsesAPI: false,
                    },
                ],
                bots: [
                    {
                        id: 'bot-1',
                        name: 'testbot',
                        displayName: 'Test Bot',
                        serviceID: 'test-service',
                        customInstructions: 'You are a helpful assistant',
                        enableVision: false,
                        enableTools: false,
                    },
                ],
                defaultBotName: 'testbot',
            });
            openAIMock = await RunOpenAIMocks(mattermost.network);

            const mmPage = new MattermostPage(page);
            const systemConsole = new SystemConsoleHelper(page);

            await mmPage.login(mattermost.url(), adminUsername, adminPassword);
            await systemConsole.navigateToPluginConfig(mattermost.url());

            await expect(page.getByText('E2E MCP Server')).toBeVisible({timeout: 15000});

            const serviceAccountToggle = page.getByRole('button', {name: /Service account authentication/i});
            await serviceAccountToggle.scrollIntoViewIfNeeded();
            await serviceAccountToggle.click();

            const fallbackSection = page.locator('#service-account-section-content-0');
            await expect(fallbackSection).toBeVisible();

            await fallbackSection.getByRole('button', {name: /Add fallback header/i}).click();

            const headerNameInput = fallbackSection.getByPlaceholder('Header name').first();
            const headerValueInput = fallbackSection.getByPlaceholder('Value').first();
            await headerNameInput.fill('Authorization');
            await headerValueInput.fill('Bearer e2e-fallback-token');

            await systemConsole.clickSave();
            await systemConsole.navigateToPluginConfig(mattermost.url());
            await page.waitForSelector('text=To report a bug or to provide feedback', {timeout: 15000});

            await expect(page.getByText('E2E MCP Server')).toBeVisible({timeout: 15000});

            const serviceAccountHeaderReloaded = page.getByRole('button', {name: /Service account authentication/i});
            await serviceAccountHeaderReloaded.scrollIntoViewIfNeeded();
            const expanded = await serviceAccountHeaderReloaded.getAttribute('aria-expanded');
            if (expanded !== 'true') {
                await serviceAccountHeaderReloaded.click();
            }

            const reloadedSection = page.locator('#service-account-section-content-0');
            await expect(reloadedSection).toBeVisible({timeout: 15000});
            await expect(reloadedSection.getByPlaceholder('Header name').first()).toHaveValue('Authorization');
            await expect(reloadedSection.getByPlaceholder('Value').first()).toHaveValue('Bearer e2e-fallback-token');
        } finally {
            if (openAIMock) {
                await openAIMock.stop();
            }
            if (mattermost) {
                await mattermost.stop();
            }
        }
    });
});
