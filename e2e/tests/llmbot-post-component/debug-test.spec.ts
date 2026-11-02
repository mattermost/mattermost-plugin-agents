import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

const test = containerTest(async (own) => {
    const mattermost = own(await RunContainer());
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async ({ containers }) => {
    ({ mattermost, openAIMock } = containers);
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
