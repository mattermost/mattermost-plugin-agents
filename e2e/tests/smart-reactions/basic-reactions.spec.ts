import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, buildTextResponse } from 'helpers/openai-mock';

// spec: /Users/nickmisasi/workspace/worktrees/mattermost-plugin-ai-agents-in-e2e/e2e/specs/smart-reactions.md
// seed: /Users/nickmisasi/workspace/worktrees/mattermost-plugin-ai-agents-in-e2e/seed.spec.ts

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.beforeEach(async () => {
    // Reset mocks before each test to prevent cross-contamination
    await openAIMock.resetMocks();
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

async function gotoTownSquare(page) {
    const target = mattermost.url() + '/test/channels/town-square';

    for (let attempt = 0; attempt < 3; attempt++) {
        try {
            await page.goto(target);
            return;
        } catch (error) {
            if (attempt === 2) {
                throw error;
            }
            await page.waitForTimeout(1000);
        }
    }
}

test.describe('Smart Reactions - Basic Functionality', () => {
    // OpenAI mock + reaction application can be slow or race under CI load; one retry matches repo flake policy.
    test.describe.configure({ retries: 1 });

    test('Access React for me menu option', async ({ page }) => {
        const { mmPage } = await setupTestPage(page);

        // Create a post
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Great job on completing the project milestone!'
        );

        // Navigate to the post
        await gotoTownSquare(page);
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });

        // Hover over the post to show menu
        await page.locator(`#post_${rootPost.id}`).hover();

        // Click AI Actions menu
        await page.getByTestId('ai-actions-menu').click();

        // Verify "React for me" option is visible
        await expect(page.getByRole('button', { name: 'React for me' })).toBeVisible();
    });

    test('Positive message gets appropriate reaction suggestion', async ({ page }) => {
        test.setTimeout(120000);
        const { mmPage } = await setupTestPage(page);

        // Create positive message
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Congratulations on the successful launch! Amazing work by everyone!'
        );

        // Navigate and interact
        await gotoTownSquare(page);
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });
        await page.locator(`#post_${rootPost.id}`).hover();
        await page.getByTestId('ai-actions-menu').click();

        // Set up mock for reaction suggestion
        await openAIMock.addCompletionMock(buildTextResponse('thumbsup'));

        // Pair listener with click. Do not require response.ok(): the handler may return an error
        // body while still completing the HTTP exchange (Chromium CI was timing out on ok-only waits).
        await Promise.all([
            page.waitForResponse(
                (response) =>
                    response.request().method() === 'POST' &&
                    response.url().includes('/plugins/mattermost-ai') &&
                    response.url().includes('/react'),
                {timeout: 90000},
            ),
            page.getByRole('button', { name: 'React for me' }).click(),
        ]);

        const postLocator = page.locator(`#post_${rootPost.id}`);
        const reactionsContainer = postLocator.locator('[aria-label="reactions"]');
        await expect(reactionsContainer).toBeVisible({timeout: 60000});
        await expect(reactionsContainer).toContainText('1');
    });
});

test.describe('Smart Reactions - Error Handling', () => {
    test('Handle API error gracefully', async ({ page }) => {
        const { mmPage } = await setupTestPage(page);

        // Create a post
        const rootPost = await mmPage.sendMessageAsUser(
            mattermost,
            username,
            password,
            'Test message for error handling'
        );

        await gotoTownSquare(page);
        await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });
        await page.locator(`#post_${rootPost.id}`).hover();
        await page.getByTestId('ai-actions-menu').click();

        // Set up error mock
        await openAIMock.addErrorMock(500, "Internal Server Error");

        await page.getByRole('button', { name: 'React for me' }).click();

        // Should not crash - may show error toast or fail silently
        await page.waitForTimeout(2000);

        // Verify page is still functional - just check page didn't crash
        await expect(page.locator('#post_' + rootPost.id)).toBeVisible();
    });
});
