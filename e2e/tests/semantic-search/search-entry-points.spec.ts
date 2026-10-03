import { test, expect, Page } from '@playwright/test';

import { RunAIMockContainer } from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { mattermostAIPluginRoutes } from 'helpers/plugin-http';

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;

// RunAIMockContainer configures no embedding search, so semantic search is
// unavailable while the license still allows Agents search.
test.beforeAll(async () => {
    mattermost = await RunAIMockContainer();
});

test.afterAll(async () => {
    await mattermost.stop();
});

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    await mmPage.login(mattermost.url(), username, password);
    return { mmPage, aiPlugin };
}

test.describe('Search Entry Points', () => {
    test('Agents search option is visible without embedding search', async ({ page }) => {
        const userClient = await mattermost.getClient(username, password);
        const aiBots = await mattermostAIPluginRoutes(mattermost.url()).getJson('ai_bots', userClient.getToken()) as { searchEnabled: boolean };
        expect(aiBots.searchEnabled).toBe(false);

        const { aiPlugin } = await setupTestPage(page);

        // Wait for plugin to be fully initialized
        await aiPlugin.openRHS();
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
        await aiPlugin.closeRHS();

        await aiPlugin.expectAgentsSearchVisible();
    });
});
