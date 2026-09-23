import { test, expect, type Page, type Locator } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildToolCallResponse,
    buildTextResponse,
    titleGenerationMockRule,
    turnMocksWithTitleSiphon,
} from 'helpers/openai-mock';
import { RunToolConfigContainerWithPolicies } from 'helpers/tool-config-container';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: AskUserQuestion with arguments that miss the declared schema
 *
 * Models sometimes stringify the option list instead of emitting it inline.
 * Such a question used to render as a generic Accept/Reject approval card
 * whose Accept failed server-side with "failed to parse question arguments".
 * The server now rejects the call before the user sees it, and the model asks
 * again with valid arguments.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const questionText = 'Which channel should I post the release notes in?';
const firstOption = 'Town Square';
const secondOption = 'Off-Topic';
const followUpText = 'Posting the release notes in Town Square now.';

const options = [
    { label: firstOption, description: 'Everyone sees it' },
    { label: secondOption },
];

// The option list arrives as a JSON-encoded string rather than an array.
const malformedQuestionArgs = JSON.stringify({
    question: questionText,
    options: JSON.stringify(options),
});

const validQuestionArgs = JSON.stringify({ question: questionText, options });

// Only the retry request carries the rejected call's error result.
const rejectionResultMatch = 'invalid arguments for tool AskUserQuestion';

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

    test('rejects the malformed call and shows the model\'s valid retry as a question card', async ({ page }) => {
        test.setTimeout(120000);

        const userMessage = 'Publish the release notes ' + Date.now();

        // Later rules take priority: the retry rule matches only once the
        // rejection has been sent back to the model.
        await openAIMock.addMocks([
            buildChatCompletionMockRule(
                buildToolCallResponse('call_malformed_question', 'AskUserQuestion', malformedQuestionArgs),
            ),
            titleGenerationMockRule(),
            buildChatCompletionMockRule(
                buildToolCallResponse('call_valid_question', 'AskUserQuestion', validQuestionArgs),
                { bodyContains: rejectionResultMatch },
            ),
        ]);

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
        // and both option labels are rendered, and the footer offers Skip
        // rather than Reject.
        await expect(botPost.getByText(questionText, { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(botPost.getByText(firstOption, { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(botPost.getByText(secondOption, { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(botPost.getByRole('button', { name: /^skip$/i })).toBeVisible({ timeout: 30000 });
        await expect(botPost.getByRole('button', { name: /^reject$/i })).toHaveCount(0);

        // The follow-up completion the answer triggers.
        await openAIMock.addMocks(turnMocksWithTitleSiphon(buildTextResponse(followUpText)));

        await botPost.getByText(firstOption, { exact: true }).click();
        await botPost.getByRole('button', { name: /^accept$/i }).click();

        await expect(botPost.getByText('Answered', { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(rhs.getByText(followUpText, { exact: false })).toBeVisible({ timeout: 30000 });
    });
});
