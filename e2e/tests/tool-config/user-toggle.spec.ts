import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';
import { RunToolConfigContainer } from 'helpers/tool-config-container';
import { createToolConfigAPIHelper } from 'helpers/tool-config';
import { AIPlugin } from 'helpers/ai-plugin';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: User Provider Toggle (4.12)
 *
 * Verifies that users can toggle MCP providers on/off in the Copilot RHS
 * via the tool provider popover, and that the preference persists.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('User Provider Toggle', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await openAIMock.addCompletionMock(responseTest);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should persist user provider preference via API', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Use the API helper to manage user preferences
        const apiHelper = await createToolConfigAPIHelper(mattermost);
        const adminClient = await mattermost.getAdminClient();
        const token = adminClient.getToken();
        const baseUrl = mattermost.url();

        // Get initial preferences
        const prefsBefore = await apiHelper.getUserPreferences(baseUrl, token);

        // Set a disabled server preference
        const updatedPrefs = await apiHelper.setUserPreferences(baseUrl, token, {
            disabled_servers: ['test-server-to-disable'],
        });

        // Verify the write response reflects what we sent
        expect(updatedPrefs).toBeDefined();
        expect(updatedPrefs.disabled_servers).toContain('test-server-to-disable');

        // Verify the preference was saved by reading it back
        const prefsAfter = await apiHelper.getUserPreferences(baseUrl, token);
        expect(prefsAfter).toBeDefined();
        expect(prefsAfter.disabled_servers).toBeDefined();
        expect(prefsAfter.disabled_servers).toContain('test-server-to-disable');

        // Clean up by restoring empty preferences
        await apiHelper.setUserPreferences(baseUrl, token, {
            disabled_servers: [],
        });
    });

    test('should keep disabled provider visible in tool list so user can re-enable it', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        const apiHelper = await createToolConfigAPIHelper(mattermost);
        const adminClient = await mattermost.getAdminClient();
        const token = adminClient.getToken();
        const baseUrl = mattermost.url();

        // Get the full tool list with all providers enabled
        const toolsBefore = await apiHelper.getUserMCPTools(baseUrl, token);
        expect(toolsBefore.servers).toBeDefined();
        expect(toolsBefore.servers.length).toBeGreaterThan(0);

        const firstServer = toolsBefore.servers[0];
        expect(firstServer.name).toBeDefined();
        expect(firstServer.tools).toBeDefined();

        // Disable the first provider via user preferences
        await apiHelper.setUserPreferences(baseUrl, token, {
            disabled_servers: [firstServer.name],
        });

        // The tool list should still include the disabled server so the user
        // can see it and re-enable it from the UI.
        const toolsAfter = await apiHelper.getUserMCPTools(baseUrl, token);
        expect(toolsAfter.servers).toBeDefined();
        expect(toolsAfter.servers.length).toBe(toolsBefore.servers.length);
        const serverStillPresent = toolsAfter.servers.find(
            (s: any) => s.name === firstServer.name,
        );
        expect(serverStillPresent).toBeDefined();

        // Verify the preference itself was persisted correctly
        const prefs = await apiHelper.getUserPreferences(baseUrl, token);
        expect(prefs.disabled_servers).toContain(firstServer.name);

        // Restore: re-enable all providers
        await apiHelper.setUserPreferences(baseUrl, token, {
            disabled_servers: [],
        });
    });
});
