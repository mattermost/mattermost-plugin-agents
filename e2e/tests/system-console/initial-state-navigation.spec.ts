import { test, expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: System Console - Initial State and Navigation
 *
 * Tests the initial state display and navigation of the Agents settings.
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

        await systemConsole.navigateToAgentsSettings(mattermost.url());

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

    serviceOnlyTest('should show the configured service and the general settings once a service exists', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToAgentsSettings(mattermost.url(), 'services');

        await expect(systemConsole.getServicesPanel()).toBeVisible({ timeout: 10000 });
        await expect(page.getByText('Test OpenAI Service').first()).toBeVisible();

        // Agent version history moved from the old AI Bots panel to General.
        await systemConsole.openSection('general');
        await expect(systemConsole.getFunctionsPanel()).toBeVisible();
        await expect(page.getByText('Limit agent version history')).toBeVisible();
    });

    fullConfigTest('should display every settings section when both services and bots exist', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToAgentsSettings(mattermost.url(), 'debug');

        // Settings is reachable from the Agents sidebar and opens on General.
        await page.getByRole('navigation', { name: 'Agents' }).getByRole('button', { name: 'Settings' }).click();
        await expect(page).toHaveURL(/\/agents\/settings$/);

        await expect(systemConsole.getFunctionsPanel()).toBeVisible();
        const defaultBotDropdown = page.getByRole('combobox', { name: 'Default bot', exact: true });
        await expect(defaultBotDropdown).toContainText('Test Bot');

        await systemConsole.openSection('services');
        await expect(systemConsole.getServicesPanel()).toBeVisible();
        await expect(page.getByText('Test Service').first()).toBeVisible();

        await systemConsole.openSection('debug');
        await expect(systemConsole.getDebugPanel()).toBeVisible();

        // Sections are links: a reload keeps the section.
        await expect(page).toHaveURL(/\/agents\/settings\/debug$/);
        await page.reload();
        await expect(systemConsole.getDebugPanel()).toBeVisible();
    });
});
