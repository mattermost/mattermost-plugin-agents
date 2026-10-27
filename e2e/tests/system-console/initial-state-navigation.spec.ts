import { test, expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: System Console - Initial State and Navigation
 *
 * Tests the initial state display and navigation of the system console configuration UI.
 */

const noServicesTest = containerTest(async (own) => {
    // Start container with NO pre-configured services or bots
    const mattermost = own(await RunSystemConsoleContainer({
        services: [],
        bots: [],
    }));
    own(await RunOpenAIMocks(mattermost.network));
    return { mattermost };
});

const serviceOnlyTest = containerTest(async (own) => {
    // Start container with one service but no bots
    const mattermost = own(await RunSystemConsoleContainer({
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
            useResponsesAPI: false,
        }],
        bots: [],
    }));
    own(await RunOpenAIMocks(mattermost.network));
    return { mattermost };
});

const fullConfigTest = containerTest(async (own) => {
    // Start container with services and bots
    const mattermost = own(await RunSystemConsoleContainer({
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
    }));
    own(await RunOpenAIMocks(mattermost.network));
    return { mattermost };
});

test.describe('Initial State and Navigation', () => {
    noServicesTest('should display no services page when no services configured', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(60000);

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
    });

    serviceOnlyTest('should show AI Bots moved notice when services exist but no legacy config bots', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(60000);

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

        await expect(page.getByText(/AI bot configuration has moved/i)).toBeVisible({ timeout: 10000 });
        await expect(systemConsole.getAddBotButton()).not.toBeVisible();
    });

    fullConfigTest('should display full configuration when both services and bots exist', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(60000);

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

        await expect(page.getByText(/AI bot configuration has moved/i)).toBeVisible();

        const defaultBotDropdown = page.getByText('Default bot').locator('..').getByRole('combobox');
        await defaultBotDropdown.scrollIntoViewIfNeeded();
        await expect(defaultBotDropdown).toContainText('Test Bot');
    });
});
