import { expect, Page } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';

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

test.beforeEach(async () => {
    // Reset mocks before each test to prevent cross-contamination
    await openAIMock.resetMocks();
});

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const url = mattermost.url();

    await mmPage.login(url, username, password);

    return { mmPage, aiPlugin };
}

test.describe('Bot Selector in Search', () => {
    test('Bot selector is visible in search after selecting Agents', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        // Wait for plugin to be fully initialized
        await aiPlugin.openRHS();
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
        await aiPlugin.closeRHS();

        // Open the search bar
        await page.getByRole('button', { name: 'Search' }).click();

        // Select the Agents search type
        const agentsRadio = page.getByRole('radio', { name: /Agents/i });
        await expect(agentsRadio).toBeVisible({ timeout: 10000 });
        await agentsRadio.click();

        // Verify bot selector button is visible with the default bot name
        const botSelector = page.getByRole('button', { name: 'Mock Bot' });
        await expect(botSelector).toBeVisible({ timeout: 10000 });
    });

    test('Selecting an agent from the dropdown keeps the search box open', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);
        const selector = await openAgentsSearch(page, aiPlugin);
        const target = (await selector.textContent())?.includes('Second Bot') ? 'Mock Bot' : 'Second Bot';

        const selectorBox = await selector.boundingBox();
        await selector.click();
        const menu = page.getByTestId('dropdownmenu');
        const menuBox = await menu.boundingBox();
        expect(menuBox!.y).toBeGreaterThanOrEqual(selectorBox!.y + selectorBox!.height);

        await menu.getByRole('button', { name: target }).click();

        await expect(page.getByRole('button', { name: target, exact: true })).toBeVisible();
        await expect(page.getByRole('radio', { name: /Agents/i })).toBeVisible();
    });

    test('Clicking Manage in the dropdown opens the agents page', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);
        const selector = await openAgentsSearch(page, aiPlugin);

        await selector.click();
        await page.getByRole('button', { name: 'Manage' }).click();

        await expect(page).toHaveURL(/\/plug\/mattermost-ai\/agents/);
    });
});

async function openAgentsSearch(page: Page, aiPlugin: AIPlugin) {
    await aiPlugin.openRHS();
    await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
    await aiPlugin.closeRHS();

    await page.getByRole('button', { name: 'Search' }).click();
    await page.getByRole('radio', { name: /Agents/i }).click();
    const selector = page.getByRole('button', { name: /^(Mock Bot|Second Bot)$/ });
    await expect(selector).toBeVisible({ timeout: 10000 });
    return selector;
}
