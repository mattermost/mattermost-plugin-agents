import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import { RunToolConfigContainer } from 'helpers/tool-config-container';
import { ToolConfigUIHelper } from 'helpers/tool-config';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Tools Tab Display (4.3)
 *
 * Verifies that the Tools tab correctly displays MCP servers and their tools,
 * including tool names, descriptions, policy dropdowns, and enable toggles.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Tools Tab Display', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should display embedded server with tools in Tools tab', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToToolsTab(mattermost.url());

        // Verify the MCP Tools Configuration header is visible
        await expect(page.getByText('MCP Tools Configuration')).toBeVisible();

        // Verify there is a tool count summary (e.g. "X tools from Y servers")
        await expect(page.getByText(/\d+ tools from \d+ servers/)).toBeVisible();

        // Verify a server row is visible with a tools enabled count
        await expect(page.getByText(/\d+\/\d+ tools? enabled/)).toBeVisible();

        // Expand the server to see individual tools
        // Click the server row header to expand
        const serverHeader = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader.click();
        await page.waitForTimeout(500);

        // Verify individual tool rows are now visible
        // Known embedded server tools include read_post, get_channel_info, etc.
        await expect(page.getByText('read_post')).toBeVisible({ timeout: 5000 });

        // Verify each tool row has a policy dropdown (select element)
        const policyDropdowns = page.locator('select');
        const dropdownCount = await policyDropdowns.count();
        expect(dropdownCount).toBeGreaterThan(0);

        // Verify each tool row has a toggle (checkbox)
        const toggles = page.locator('input[type="checkbox"]');
        const toggleCount = await toggles.count();
        expect(toggleCount).toBeGreaterThan(0);
    });

    test('should show correct policy values for vetted tools', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToToolsTab(mattermost.url());

        // Expand the embedded server
        const serverHeader = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader.click();
        await page.waitForTimeout(500);

        // Verify read_post (a vetted READ tool) shows the DM-scoped auto-run policy
        await expect(page.getByText('read_post')).toBeVisible({ timeout: 5000 });
        const readPostPolicy = toolConfig.getToolPolicyDropdown('read_post');
        await expect(readPostPolicy).toHaveValue('auto_run_in_dm');

        // Verify the toggle is checked (enabled) for read_post
        const readPostToggle = toolConfig.getToolToggle('read_post');
        await expect(readPostToggle).toBeChecked();
    });
});
