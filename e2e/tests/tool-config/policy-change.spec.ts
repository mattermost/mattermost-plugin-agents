import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import { RunToolConfigContainer } from 'helpers/tool-config-container';
import { ToolConfigUIHelper } from 'helpers/tool-config';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Per-Tool Policy Change (4.4)
 *
 * Verifies that admin can change a tool's execution policy between
 * "Auto Run (DM)", "Auto Run (Everywhere)", and "Ask Every Time",
 * and that the changes persist across reload.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe.serial('Per-Tool Policy Change', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should change tool policy from Auto Run (DM) to Ask Every Time and persist', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToToolsTab(mattermost.url());

        // Expand the embedded server
        const serverHeader = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader.click();
        await page.waitForTimeout(500);

        // Find read_post tool (should be "auto_run" from vetted seed)
        await expect(page.getByText('read_post')).toBeVisible({ timeout: 5000 });
        const readPostPolicy = toolConfig.getToolPolicyDropdown('read_post');
        await expect(readPostPolicy).toHaveValue('auto_run');

        // Change to "Ask Every Time"
        await toolConfig.setToolPolicy('read_post', 'Ask Every Time');
        await expect(readPostPolicy).toHaveValue('ask');

        // Save
        await toolConfig.clickSave();

        // Reload page
        await toolConfig.navigateToToolsTab(mattermost.url());

        // Expand server again
        const serverHeader2 = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader2.click();
        await page.waitForTimeout(500);

        // Verify the tool now shows "ask"
        await expect(page.getByText('read_post')).toBeVisible({ timeout: 5000 });
        const readPostPolicyAfter = toolConfig.getToolPolicyDropdown('read_post');
        await expect(readPostPolicyAfter).toHaveValue('ask');
    });

    test('should change tool policy from Ask Every Time to Auto Run (DM) and persist', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToToolsTab(mattermost.url());

        // Expand the embedded server
        const serverHeader = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader.click();
        await page.waitForTimeout(500);

        // read_post should be "ask" from the previous test changing it
        // (tests in the same describe block share the container)
        await expect(page.getByText('read_post')).toBeVisible({ timeout: 5000 });

        // Change to "Auto Run (DM)"
        await toolConfig.setToolPolicy('read_post', 'Auto Run (DM)');
        const readPostPolicy = toolConfig.getToolPolicyDropdown('read_post');
        await expect(readPostPolicy).toHaveValue('auto_run');

        // Save
        await toolConfig.clickSave();

        // Reload page
        await toolConfig.navigateToToolsTab(mattermost.url());

        // Expand server again
        const serverHeader2 = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader2.click();
        await page.waitForTimeout(500);

        // Verify the tool now shows "auto_run"
        await expect(page.getByText('read_post')).toBeVisible({ timeout: 5000 });
        const readPostPolicyAfter = toolConfig.getToolPolicyDropdown('read_post');
        await expect(readPostPolicyAfter).toHaveValue('auto_run');
    });

    test('should change tool policy to Auto Run (Everywhere) and persist', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToToolsTab(mattermost.url());

        const serverHeader = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader.click();
        await page.waitForTimeout(500);

        await expect(page.getByText('read_post')).toBeVisible({ timeout: 5000 });

        await toolConfig.setToolPolicy('read_post', 'Auto Run (Everywhere)');
        const readPostPolicy = toolConfig.getToolPolicyDropdown('read_post');
        await expect(readPostPolicy).toHaveValue('auto_run_everywhere');

        await toolConfig.clickSave();

        await toolConfig.navigateToToolsTab(mattermost.url());

        const serverHeader2 = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader2.click();
        await page.waitForTimeout(500);

        await expect(page.getByText('read_post')).toBeVisible({ timeout: 5000 });
        const readPostPolicyAfter = toolConfig.getToolPolicyDropdown('read_post');
        await expect(readPostPolicyAfter).toHaveValue('auto_run_everywhere');
    });
});
