// spec: system-console-additional-scenarios.plan.md - Debug Panel
// seed: e2e/tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Debug Panel
 *
 * Tests configuration options in the Debug panel of the system console.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe.serial('Debug Panel', () => {
    test('should toggle Enable LLM Trace', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with enableLLMTrace set to false
        mattermost = await RunSystemConsoleContainer({
            enableLLMTrace: false,
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
                }
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
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as sysadmin user
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console AI plugin configuration page
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Scroll down to locate the Debug panel
        const debugPanel = systemConsole.getDebugPanel();
        await debugPanel.scrollIntoViewIfNeeded();

        // Verify the Debug panel title is visible
        await expect(debugPanel).toBeVisible();

        // Locate the 'Enable LLM Trace' radio buttons by index
        // Radios on page: 0-1=plugin enable, 2-3=render links, 4-5=llm trace, 6-7=token logging, 8-11=mcp
        const llmTraceTrue = page.getByRole('radio').nth(4);
        const llmTraceFalse = page.getByRole('radio').nth(5);

        // Verify the toggle is currently OFF (false radio is checked)
        await expect(llmTraceFalse).toBeChecked();

        // Click the "true" radio to enable it
        await llmTraceTrue.click();

        // Verify the toggle changes to ON state
        await expect(llmTraceTrue).toBeChecked();

        // Click Save button at bottom of page
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // Reload the page
        await page.reload();

        // Scroll to Debug panel
        const reloadedDebugPanel = systemConsole.getDebugPanel();
        await reloadedDebugPanel.scrollIntoViewIfNeeded();

        // Verify 'Enable LLM Trace' toggle is still ON (true radio is checked)
        const reloadedLlmTraceTrue = page.getByRole('radio').nth(4);
        const reloadedLlmTraceFalse = page.getByRole('radio').nth(5);
        await expect(reloadedLlmTraceTrue).toBeChecked();

        // Click false radio to disable
        await reloadedLlmTraceFalse.click();

        // Verify the toggle changes to OFF state
        await expect(reloadedLlmTraceFalse).toBeChecked();

        // Click Save
        await saveButton.click();

        // Reload page
        await page.reload();

        // Verify toggle is OFF (false radio is checked)
        const finalLlmTraceFalse = page.getByRole('radio').nth(5);
        await expect(finalLlmTraceFalse).toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should toggle Enable Token Usage Logging', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with enableTokenUsageLogging set to false
        mattermost = await RunSystemConsoleContainer({
            enableTokenUsageLogging: false,
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
                }
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
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as sysadmin user
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console AI plugin configuration page
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Scroll to the Debug panel
        const debugPanel = systemConsole.getDebugPanel();
        await debugPanel.scrollIntoViewIfNeeded();

        // Locate the 'Enable Token Usage Logging' radio buttons by index
        // Radios: 0-1=plugin enable, 2-3=render links, 4-5=llm trace, 6-7=token logging, 8-11=mcp
        const tokenLoggingTrue = page.getByRole('radio').nth(6);
        const tokenLoggingFalse = page.getByRole('radio').nth(7);

        // Verify the toggle is currently OFF
        await expect(tokenLoggingFalse).toBeChecked();

        // Click the "true" radio to enable it
        await tokenLoggingTrue.click();

        // Verify the toggle changes to ON state
        await expect(tokenLoggingTrue).toBeChecked();

        // Click Save button
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // Reload the page
        await page.reload();

        // Verify 'Enable Token Usage Logging' toggle is ON after reload
        const reloadedTokenLoggingTrue = page.getByRole('radio').nth(6);
        const reloadedTokenLoggingFalse = page.getByRole('radio').nth(7);
        await expect(reloadedTokenLoggingTrue).toBeChecked();

        // Toggle it OFF
        await reloadedTokenLoggingFalse.click();

        // Verify the toggle changes to OFF state
        await expect(reloadedTokenLoggingFalse).toBeChecked();

        // Save the change
        await saveButton.click();

        // Reload and verify it's OFF
        await page.reload();

        const finalTokenLoggingFalse = page.getByRole('radio').nth(7);
        await expect(finalTokenLoggingFalse).toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should configure both debug toggles independently', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with both enableLLMTrace and enableTokenUsageLogging set to false
        mattermost = await RunSystemConsoleContainer({
            enableLLMTrace: false,
            enableTokenUsageLogging: false,
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
                }
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
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Scroll to Debug panel
        const debugPanel = systemConsole.getDebugPanel();
        await debugPanel.scrollIntoViewIfNeeded();

        // Verify both toggles are OFF
        // Radios: 0-1=plugin enable, 2-3=render links, 4-5=llm trace, 6-7=token logging, 8-11=mcp
        const llmTraceTrue = page.getByRole('radio').nth(4);
        const llmTraceFalse = page.getByRole('radio').nth(5);
        const tokenLoggingTrue = page.getByRole('radio').nth(6);
        const tokenLoggingFalse = page.getByRole('radio').nth(7);

        await expect(llmTraceFalse).toBeChecked();
        await expect(tokenLoggingFalse).toBeChecked();

        // Enable only 'Enable LLM Trace', leave 'Enable Token Usage Logging' OFF
        await llmTraceTrue.click();
        await expect(llmTraceTrue).toBeChecked();
        await expect(tokenLoggingFalse).toBeChecked();

        // Click Save
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();
        await page.waitForTimeout(1000);

        // Reload page
        await page.reload();

        // Verify 'Enable LLM Trace' is ON and 'Enable Token Usage Logging' is OFF
        const reloadedLlmTraceTrue = page.getByRole('radio').nth(4);
        const reloadedLlmTraceFalse = page.getByRole('radio').nth(5);
        const reloadedTokenLoggingTrue = page.getByRole('radio').nth(6);
        const reloadedTokenLoggingFalse = page.getByRole('radio').nth(7);

        await expect(reloadedLlmTraceTrue).toBeChecked();
        await expect(reloadedTokenLoggingFalse).toBeChecked();

        // Now enable 'Enable Token Usage Logging' while keeping 'Enable LLM Trace' ON
        await reloadedTokenLoggingTrue.click();
        await expect(reloadedLlmTraceTrue).toBeChecked();
        await expect(reloadedTokenLoggingTrue).toBeChecked();

        // Click Save
        await saveButton.click();
        await page.waitForTimeout(1000);

        // Reload page
        await page.reload();

        // Verify both toggles are ON
        const bothOnLlmTraceTrue = page.getByRole('radio').nth(4);
        const bothOnLlmTraceFalse = page.getByRole('radio').nth(5);
        const bothOnTokenLoggingTrue = page.getByRole('radio').nth(6);

        await expect(bothOnLlmTraceTrue).toBeChecked();
        await expect(bothOnTokenLoggingTrue).toBeChecked();

        // Disable 'Enable LLM Trace', keep 'Enable Token Usage Logging' ON
        await bothOnLlmTraceFalse.click();
        await expect(bothOnLlmTraceFalse).toBeChecked();
        await expect(bothOnTokenLoggingTrue).toBeChecked();

        // Click Save
        await saveButton.click();
        await page.waitForTimeout(1000);

        // Reload page
        await page.reload();

        // Verify 'Enable LLM Trace' is OFF and 'Enable Token Usage Logging' is ON
        const finalLlmTraceFalse = page.getByRole('radio').nth(5);
        const finalTokenLoggingTrue = page.getByRole('radio').nth(6);

        await expect(finalLlmTraceFalse).toBeChecked();
        await expect(finalTokenLoggingTrue).toBeChecked();

        await openAIMock.stop();
        await mattermost.stop();
    });
});
