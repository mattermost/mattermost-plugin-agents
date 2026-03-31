import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import { RunToolConfigContainer } from 'helpers/tool-config-container';
import { ToolConfigUIHelper } from 'helpers/tool-config';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Tab Layout Verification (4.7)
 *
 * Verifies that the system console plugin page shows exactly 2 tabs:
 * "Configuration" and "Tools". No "Approved Servers" tab should exist.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Tab Layout', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show exactly 2 tabs: Configuration and Tools', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToPluginConfig(mattermost.url());

        // Get all tab buttons
        const tabs = toolConfig.getTabButtons();

        // Verify exactly 2 tabs exist
        await expect(tabs).toHaveCount(2);

        // Verify tab names
        await expect(toolConfig.getTab('Configuration')).toBeVisible();
        await expect(toolConfig.getTab('Tools')).toBeVisible();

        // Verify NO "Approved Servers" tab exists
        const approvedServersTab = page.getByRole('button', { name: 'Approved Servers', exact: true });
        await expect(approvedServersTab).not.toBeVisible();
    });

    test('should switch between tabs', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToPluginConfig(mattermost.url());

        // Verify Configuration tab content is visible by default
        await expect(page.getByText('Enable MCP Client')).toBeVisible();

        // Click Tools tab
        await toolConfig.getTab('Tools').click();
        await expect(page.getByText('MCP Tools Configuration')).toBeVisible();

        // Click back to Configuration tab
        await toolConfig.getTab('Configuration').click();
        await expect(page.getByText('Enable MCP Client')).toBeVisible();
    });
});
