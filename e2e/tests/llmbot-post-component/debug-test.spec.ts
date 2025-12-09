import { test, expect } from '@playwright/test';
import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

async function setupTestPage(page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const url = mattermost.url();

    await mmPage.login(url, username, password);

    return { mmPage, aiPlugin };
}

test.describe('Debug', () => {
    test('Basic response works (from working test)', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);
        await aiPlugin.openRHS();

        // Use the exact mock from basic.spec.ts that we know works
        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.sendMessage('Hello!');
        await aiPlugin.waitForBotResponse(responseTestText);

        // If we get here, basic mocking works
        await expect(page.getByText(responseTestText)).toBeVisible();
    });
});
