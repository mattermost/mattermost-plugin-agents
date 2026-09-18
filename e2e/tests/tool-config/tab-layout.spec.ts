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
 * Verifies the Agents console shows Services / MCPs / Settings, and that
 * the MCPs tab contains Configuration and Tools. No "Approved Servers" tab.
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

    test('should show Services, MCPs, and Settings tabs', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToPluginConfig(mattermost.url());

        const tabs = toolConfig.getTabButtons();
        await expect(tabs).toHaveCount(3);

        await expect(toolConfig.getTab('Services')).toBeVisible();
        await expect(toolConfig.getTab('MCPs')).toBeVisible();
        await expect(toolConfig.getTab('Settings')).toBeVisible();

        const approvedServersTab = page.getByRole('button', { name: 'Approved Servers', exact: true });
        await expect(approvedServersTab).not.toBeVisible();
    });

    test('should switch between console and MCP tabs', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToPluginConfig(mattermost.url());

        await expect(page.getByText('AI Services').first()).toBeVisible();

        await toolConfig.getTab('MCPs').click();
        await expect(page.getByText('Enable Mattermost MCP Server (HTTP)')).toBeVisible();
        await expect(toolConfig.getTab('Configuration')).toBeVisible();
        await expect(toolConfig.getTab('Tools')).toBeVisible();

        await toolConfig.getTab('Tools').click();
        await expect(page.getByText('MCP Tools Configuration')).toBeVisible();

        await toolConfig.getTab('Configuration').click();
        await expect(page.getByText('Enable Mattermost MCP Server (HTTP)')).toBeVisible();

        await toolConfig.getTab('Settings').click();
        await expect(page.getByText('AI Functions').first()).toBeVisible();
    });
});
