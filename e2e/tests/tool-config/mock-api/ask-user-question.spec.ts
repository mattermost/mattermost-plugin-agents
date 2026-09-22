import { test, expect, type Page, type Locator } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildToolCallResponse,
    buildTextResponse,
    turnMocksWithTitleSiphon,
} from 'helpers/openai-mock';
import { RunToolConfigContainerWithPolicies } from 'helpers/tool-config-container';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: AskUserQuestion with arguments that miss the declared schema
 *
 * Models routinely stringify the option list instead of emitting it inline.
 * Such a question used to render as a generic Accept/Reject approval card
 * whose Accept failed server-side with "failed to parse question arguments",
 * leaving the question unanswerable. Both sides now repair the shape, so the
 * question card renders and the answer round-trips.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const questionText = 'Which channel should I post the release notes in?';
const firstOption = 'Town Square';
const secondOption = 'Off-Topic';
const followUpText = 'Posting the release notes in Town Square now.';

// The option list arrives as a JSON-encoded string rather than an array.
const malformedQuestionArgs = JSON.stringify({
    question: questionText,
    options: JSON.stringify([
        { label: firstOption, description: 'Everyone sees it' },
        { label: secondOption },
    ]),
});

async function waitForSentPost(page: Page, message: string, timeout = 30000): Promise<Locator> {
    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, { exact: true }),
    }).last();
    await expect(post).toBeVisible({ timeout });
    return post;
}

test.describe('AskUserQuestion with malformed arguments (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('renders an answerable question card and round-trips the answer', async ({ page }) => {
        test.setTimeout(120000);

        const userMessage = 'Publish the release notes ' + Date.now();

        await openAIMock.addMocks(turnMocksWithTitleSiphon(
            buildToolCallResponse('call_ask_user_question', 'AskUserQuestion', malformedQuestionArgs),
        ));

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost,
            adminUsername,
            adminPassword,
            'toolbot',
        );

        await mmPage.mentionBot('toolbot', userMessage);
        const post = await waitForSentPost(page, `@toolbot ${userMessage}`);
        await expect(post.getByText(/\d+ repl/i)).toBeVisible({ timeout: 30000 });
        await post.getByText(/\d+ repl/i).click();

        const rhs = page.locator('#rhsContainer');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();

        // The question card, not the generic approval card: the question text
        // and both repaired option labels are rendered, and the footer offers
        // Skip rather than Reject.
        await expect(botPost.getByText(questionText, { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(botPost.getByText(firstOption, { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(botPost.getByText(secondOption, { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(botPost.getByRole('button', { name: /^skip$/i })).toBeVisible({ timeout: 30000 });
        await expect(botPost.getByRole('button', { name: /^reject$/i })).toHaveCount(0);

        // The follow-up completion the answer triggers.
        await openAIMock.addMocks(turnMocksWithTitleSiphon(buildTextResponse(followUpText)));

        await botPost.getByText(firstOption, { exact: true }).click();
        await botPost.getByRole('button', { name: /^accept$/i }).click();

        // Accepting resolves the question instead of failing the request.
        await expect(botPost.getByText('Answered', { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(rhs.getByText(followUpText, { exact: false })).toBeVisible({ timeout: 30000 });
    });
});
