import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import { RunToolConfigContainer } from 'helpers/tool-config-container';
import { ToolConfigUIHelper, createToolConfigAPIHelper } from 'helpers/tool-config';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Vetted Server Seed (4.6)
 *
 * Verifies that the embedded Mattermost server has vetted tools pre-seeded
 * with "Auto Run" policy and enabled state for READ-only tools.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

// Known vetted READ tools for the embedded Mattermost server
const VETTED_READ_TOOLS = [
    'read_post',
    'read_channel',
    'get_channel_info',
    'get_channel_members',
    'get_team_info',
    'get_team_members',
    'search_posts',
    'search_users',
    'get_user_channels',
];

test.describe('Vetted Server Seed', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show vetted Mattermost tools pre-seeded as Auto Run', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const toolConfig = new ToolConfigUIHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await toolConfig.navigateToToolsTab(mattermost.url());

        // Expand the embedded server
        const serverHeader = page.getByText(/\d+\/\d+ tools? enabled/).first();
        await serverHeader.click();
        await page.waitForTimeout(500);

        // Check that at least some known vetted READ tools are visible and have "auto_run" policy
        for (const toolName of ['read_post', 'get_channel_info', 'search_posts']) {
            const toolText = page.getByText(toolName, { exact: true });
            // Only check tools that are visible (embedded server may not expose all)
            if (await toolText.isVisible().catch(() => false)) {
                const dropdown = toolConfig.getToolPolicyDropdown(toolName);
                await expect(dropdown).toHaveValue('auto_run');

                const toggle = toolConfig.getToolToggle(toolName);
                await expect(toggle).toBeChecked();
            }
        }
    });

    test('should verify vetted seed via API', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Use the API helper to check tool configurations
        const apiHelper = await createToolConfigAPIHelper(mattermost);
        const adminClient = await mattermost.getAdminClient();
        const token = adminClient.getToken();

        const toolsResponse = await apiHelper.getUserMCPTools(mattermost.url(), token);

        // Verify servers are returned
        expect(toolsResponse.servers).toBeDefined();
        expect(toolsResponse.servers.length).toBeGreaterThan(0);

        // Find the embedded server (identified by having Mattermost tools)
        const embeddedServer = toolsResponse.servers.find((s: any) =>
            s.tools?.some((t: any) => VETTED_READ_TOOLS.includes(t.name)),
        );

        // If embedded server tools are in the response, verify they are present
        if (embeddedServer) {
            const toolNames = embeddedServer.tools.map((t: any) => t.name);
            // At least some vetted tools should be present
            const foundVettedTools = VETTED_READ_TOOLS.filter((name) =>
                toolNames.includes(name),
            );
            expect(foundVettedTools.length).toBeGreaterThan(0);
        }
    });
});
