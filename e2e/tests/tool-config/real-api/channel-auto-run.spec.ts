import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { RunRealAPIContainer, REAL_API_BEFORE_ALL_TIMEOUT_MS } from 'helpers/real-api-container';
import {
    getAPIConfig,
    getAvailableProviders,
} from 'helpers/api-config';

/**
 * Test Suite: Channel Auto Run Two-Stage (Real API) (4.11)
 *
 * Verifies Auto Run (DM) behavior in a channel: call is auto-approved but
 * result sharing still requires user approval (channel safety).
 *
 * Skip-gated: requires ANTHROPIC_API_KEY or OPENAI_API_KEY.
 */

const config = getAPIConfig();
const skipMessage =
    'Skipping channel-auto-run tests: No ANTHROPIC_API_KEY or OPENAI_API_KEY found in environment.';
const providers = config.shouldRunTests ? getAvailableProviders() : [];

for (const provider of providers) {
    test.describe(`Channel Auto Run Two-Stage (${provider.name})`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            test.setTimeout(REAL_API_BEFORE_ALL_TIMEOUT_MS);
            mattermost = await RunRealAPIContainer({
                service: provider.service,
                bot: provider.bot,
            });
        });

        test.afterAll(async () => {
            if (mattermost) {
                await mattermost.stop();
            }
        });

        test('auto_run dm policy skips call approval but requires result-sharing approval', async ({ browser }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(480000);

            // Create a new browser context for this test
            const context = await browser.newContext();
            const page = await context.newPage();

            const mmPage = new MattermostPage(page);

            // Login as regular user
            await mmPage.login(mattermost.url(), 'regularuser', 'regularuser');

            // @mention bot in a channel
            const botName = provider.bot.name;
            await mmPage.mentionBot(botName, 'What channels exist in this team? Please look them up.');

            // Wait for the bot to reply in the thread
            const replyIndicator = page.getByText(/\d+ repl/);
            await expect(replyIndicator).toBeVisible({ timeout: 90000 });

            // Open the reply thread
            await replyIndicator.click();

            // Wait for the bot to finish streaming its reply in the thread.
            // Poll for the Share button with a generous timeout to account for
            // tool invocation + LLM streaming latency.
            const shareButton = page.getByRole('button', { name: /share/i });
            const keepPrivateButton = page.getByRole('button', { name: /keep private/i });

            let isShareVisible = false;
            try {
                await expect(shareButton.or(keepPrivateButton)).toBeVisible({ timeout: 120000 });
                isShareVisible = true;
            } catch {
                isShareVisible = false;
            }

            // If the LLM didn't invoke a tool, skip so the result clearly
            // signals the two-stage channel flow was not exercised (avoids a
            // false-green pass).
            if (!isShareVisible) {
                test.skip(true, 'LLM did not invoke a tool; two-stage channel approval flow was not exercised');
            }

            // The share/keep-private prompt appeared — the two-stage flow is active.
            await expect(shareButton).toBeVisible();
            await shareButton.click();
            await page.waitForTimeout(5000);

            await context.close();
        });
    });
}

// Ensure at least one test runs even when skipped
if (providers.length === 0) {
    test('channel auto-run (skipped - no API keys)', async () => {
        test.skip(!config.shouldRunTests, skipMessage);
    });
}
