import { test, expect, Page } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

// Build a streaming SSE mock response with permalink citations using real post IDs and site URL.
// Uses team name in the URL (e.g. /test/pl/) so links pass the unsafeLinks permalink filter.
function buildSearchResponseWithCitations(siteURL: string, teamName: string, postId1: string, postId2: string): string {
    const cite1 = `[permalink](${siteURL}/${teamName}/pl/${postId1}?view=citation)`;
    const cite2 = `[permalink](${siteURL}/${teamName}/pl/${postId2}?view=citation)`;

    const chunks = [
        {delta: {role: 'assistant', content: ''}, finish_reason: null},
        {delta: {content: `Based on the discussion ${cite1} the budget has been approved ${cite2}.`}, finish_reason: null},
        {delta: {}, finish_reason: 'stop'},
    ];

    const lines = chunks.map((choice, _i) => {
        const obj = {
            id: 'chatcmpl-citations-1',
            object: 'chat.completion.chunk',
            created: 1708124577,
            model: 'gpt-3.5-turbo-0613',
            system_fingerprint: null,
            choices: [{index: 0, ...choice, logprobs: null}],
        };
        return `data: ${JSON.stringify(obj)}`;
    });

    lines.push('data: [DONE]');
    return lines.join('\n\n') + '\n\n';
}

const searchResponseText = 'Based on the discussion';

async function setupTestPage(page: Page, mattermost: MattermostContainer) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const url = mattermost.url();

    await mmPage.login(url, username, password);

    return { mmPage, aiPlugin };
}

test.describe('Post Citations Display', () => {
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

    test('Permalink citations render as clickable links', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page, mattermost);

        // Create posts and capture their IDs for citation URLs
        const post1 = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'We need to discuss the Q4 budget allocation for the marketing department'
        );

        const post2 = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'The budget for the new project has been approved by leadership'
        );

        // Wait for posts to be indexed by the embedding search
        await page.waitForTimeout(2000);

        // Build the mock response dynamically with real post IDs, team name, and site URL.
        // The team name in the URL ensures links pass the unsafeLinks permalink filter.
        const siteURL = mattermost.url();
        const mockResponse = buildSearchResponseWithCitations(siteURL, 'test', post1.id, post2.id);
        await openAIMock.addCompletionMock(mockResponse);

        // Wait for plugin to be fully initialized (app bar icon indicates plugin is ready)
        await aiPlugin.openRHS();
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
        await aiPlugin.closeRHS();

        // Trigger embedding search via the search bar
        await aiPlugin.triggerEmbeddingSearch('budget discussion');

        // Wait for bot response to appear
        await aiPlugin.waitForBotResponse(searchResponseText);

        // Verify permalink citation links are rendered inside the bot response post.
        // Scope to the RHS bot post rather than the last PostText in the thread to avoid
        // racing against the user query/root post order in CI.
        const rhsBotPost = page.getByTestId('mattermost-ai-rhs').locator('[data-testid="llm-bot-post"]').filter({
            hasText: searchResponseText,
        }).last();
        await expect(rhsBotPost).toBeVisible({timeout: 30000});

        const botPostText = rhsBotPost.getByTestId('posttext');
        await expect(botPostText).toContainText(searchResponseText, {timeout: 30000});

        // The [permalink](URL?view=citation) markdown should render as <a> tags
        // containing the post IDs in their href attribute
        const citationLinks = botPostText.locator(`a[href*="view=citation"]`);
        await expect(citationLinks).toHaveCount(2, {timeout: 30000});

        // Verify links point to the correct posts via /team/pl/
        const firstHref = await citationLinks.nth(0).getAttribute('href');
        const secondHref = await citationLinks.nth(1).getAttribute('href');
        expect(firstHref).toContain(`/test/pl/${post1.id}`);
        expect(secondHref).toContain(`/test/pl/${post2.id}`);
    });
});
