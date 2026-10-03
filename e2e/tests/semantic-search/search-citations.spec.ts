import { test, expect, Page } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    buildToolCallResponse,
    titleGenerationMockRule,
} from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

// Alphabetic so keyword search treats it as a single searchable term.
function uniqueTerm(): string {
    const letters = 'abcdefghijklmnopqrstuvwxyz';
    let suffix = '';
    for (let i = 0; i < 8; i++) {
        suffix += letters[Math.floor(Math.random() * letters.length)];
    }
    return `zephyr${suffix}`;
}

async function setupTestPage(page: Page, mattermost: MattermostContainer) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    await mmPage.login(mattermost.url(), username, password);
    return { mmPage, aiPlugin };
}

test.describe('Search bar tool calling and citations', () => {
    let mattermost: MattermostContainer;
    let openAIMock: OpenAIMockContainer;

    test.beforeAll(async () => {
        mattermost = await RunContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.beforeEach(async () => {
        await openAIMock.resetMocks();
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('searches with search_posts and cites the posts it found', async ({ page }) => {
        test.setTimeout(120000);
        const { mmPage, aiPlugin } = await setupTestPage(page, mattermost);

        const term = uniqueTerm();
        const budgetPost = await mmPage.sendMessageAsUser(mattermost, username, password, `The ${term} budget has been approved by leadership`);
        const launchPost = await mmPage.sendMessageAsUser(mattermost, username, password, `The ${term} launch moves to Friday`);

        const siteURL = mattermost.url();
        const answerText = `The ${term} budget was approved`;
        const answer = `${answerText} [permalink](${siteURL}/test/pl/${budgetPost.id}?view=citation) and the launch moves to Friday [permalink](${siteURL}/test/pl/${launchPost.id}?view=citation)`;

        // The answer is only served once the request carries the real
        // search_posts output, so the test fails if the tool did not run.
        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildToolCallResponse('call_search_e2e', 'search_posts', JSON.stringify({ query: term }))),
            buildChatCompletionMockRule(buildTextResponse(answer), { bodyContains: `${term} launch moves to Friday` }),
            titleGenerationMockRule(),
        ]);

        await aiPlugin.openRHS();
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
        await aiPlugin.closeRHS();

        await aiPlugin.triggerEmbeddingSearch(`what happened with the ${term} project?`);
        await aiPlugin.waitForBotResponse(answerText);

        const rhsBotPost = page.getByTestId('mattermost-ai-rhs').locator('[data-testid="llm-bot-post"]').filter({
            hasText: answerText,
        }).last();
        await expect(rhsBotPost).toBeVisible({ timeout: 30000 });
        await expect(rhsBotPost.getByTestId('llm-bot-tool-activity-header')).toContainText('Used 1 tool');

        const citationLinks = rhsBotPost.getByTestId('posttext').locator('a[href*="view=citation"]');
        await expect(citationLinks).toHaveCount(2, { timeout: 30000 });
        expect(await citationLinks.nth(0).getAttribute('href')).toContain(`/test/pl/${budgetPost.id}`);
        expect(await citationLinks.nth(1).getAttribute('href')).toContain(`/test/pl/${launchPost.id}`);
    });
});
