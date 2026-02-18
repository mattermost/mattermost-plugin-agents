import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: System Console - Initial State and Navigation
 *
 * Tests the initial state display and navigation of the system console configuration UI.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe.serial('Initial State and Navigation', () => {
    test('should display no services page when no services configured', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with NO pre-configured services or bots
        mattermost = await RunSystemConsoleContainer({
            services: [],
            bots: [],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as admin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Verify beta message is visible
        const betaMessage = systemConsole.getBetaMessage();
        await expect(betaMessage).toBeVisible();

        // Verify no services message is displayed
        const noServicesMessage = systemConsole.getNoServicesMessage();
        await expect(noServicesMessage).toBeVisible();

        // Verify Add Service button is visible
        const addServiceButton = systemConsole.getAddServiceButton();
        await expect(addServiceButton).toBeVisible();
        await expect(addServiceButton).toBeEnabled();

        // Cleanup
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should display no bots page when services exist but no bots configured', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with one service but no bots
        mattermost = await RunSystemConsoleContainer({
            services: [{
                id: 'test-service-1',
                name: 'Test OpenAI Service',
                type: 'openai',
                apiKey: 'test-key',
                apiURL: 'https://api.openai.com',
                defaultModel: 'gpt-4',
                tokenLimit: 8000,
                outputTokenLimit: 4000,
                streamingTimeoutSeconds: 0,
                sendUserId: false,
                useResponsesAPI: false,
            }],
            bots: [],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as admin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Verify AI Services panel is visible
        const servicesPanel = systemConsole.getServicesPanel();
        await expect(servicesPanel).toBeVisible({ timeout: 10000 });

        // Wait for the bots panel to be fully loaded
        await systemConsole.waitForBotsPanel();

        // Verify No Bots message is displayed
        const noBotsMessage = systemConsole.getNoBotsMessage();
        await expect(noBotsMessage).toBeVisible({ timeout: 10000 });

        // Verify Add Bot button is visible
        const addBotButton = systemConsole.getAddBotButton();
        await expect(addBotButton).toBeVisible({ timeout: 10000 });
        await expect(addBotButton).toBeEnabled();

        // Cleanup
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should display full configuration when both services and bots exist', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with services and bots
        mattermost = await RunSystemConsoleContainer({
            services: [{
                id: 'test-service-1',
                name: 'Test Service',
                type: 'anthropic',
                apiKey: 'test-key',
                apiURL: 'https://api.anthropic.com',
                defaultModel: 'claude-3-opus',
                tokenLimit: 8000,
                outputTokenLimit: 4000,
                streamingTimeoutSeconds: 0,
                sendUserId: false,
                useResponsesAPI: false,
            }],
            bots: [{
                id: 'test-bot-1',
                name: 'testbot',
                displayName: 'Test Bot',
                serviceID: 'test-service-1',
                customInstructions: '',
                enableVision: false,
                disableTools: false,
            }],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as admin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Verify all panels are visible
        await expect(systemConsole.getServicesPanel()).toBeVisible();
        await expect(systemConsole.getBotsPanel()).toBeVisible();
        await expect(systemConsole.getFunctionsPanel()).toBeVisible();
        await expect(systemConsole.getDebugPanel()).toBeVisible();

        // Verify service is listed
        await expect(page.getByText('Test Service').first()).toBeVisible();

        // Verify bot is listed
        await expect(page.getByText('Test Bot').first()).toBeVisible();

        // Cleanup
        await openAIMock.stop();
        await mattermost.stop();
    });
});
