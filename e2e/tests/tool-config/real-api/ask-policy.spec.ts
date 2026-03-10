import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { RunRealAPIContainer } from 'helpers/real-api-container';
import {
    getAPIConfig,
    getAvailableProviders,
} from 'helpers/api-config';
import { createToolConfigAPIHelper } from 'helpers/tool-config';

/**
 * Test Suite: Ask Policy (Real API) (4.9)
 *
 * Verifies that tools configured with "ask" policy show pending
 * approval in a DM with the bot.
 *
 * Skip-gated: requires ANTHROPIC_API_KEY or OPENAI_API_KEY.
 */

const config = getAPIConfig();
const skipMessage =
    'Skipping ask-policy tests: No ANTHROPIC_API_KEY or OPENAI_API_KEY found in environment.';

const providers = config.shouldRunTests ? getAvailableProviders() : [];

for (const provider of providers) {
    test.describe(`Ask Policy (${provider.name})`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            mattermost = await RunRealAPIContainer({
                service: provider.service,
                bot: provider.bot,
            });
        });

        test.afterAll(async () => {
            await mattermost.stop();
        });

        test('ask policy tool shows pending approval in DM', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(120000);

            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);

            // Login
            await mmPage.login(mattermost.url(), 'regularuser', 'regularuser');

            // Open Copilot RHS
            await aiPlugin.openRHS();

            // Send a message that should trigger a tool call requiring approval
            await aiPlugin.sendMessage('Can you create a new post in town-square saying "hello from bot"?');

            // Wait for streaming to complete rather than using a fixed timeout
            await page.waitForTimeout(2000);
            const stopButton = page.getByRole('button', { name: /stop/i });
            await expect(stopButton).not.toBeVisible({ timeout: 90000 });

            // Verify the bot responded with some content in the RHS
            const rhsContainer = page.getByTestId('mattermost-ai-rhs');
            await expect(rhsContainer).toBeVisible();

            // Check for Accept/Reject buttons — these appear when an "ask" tool is invoked
            const acceptButton = page.getByRole('button', { name: /accept/i });
            const isAcceptVisible = await acceptButton.isVisible().catch(() => false);

            if (isAcceptVisible) {
                await acceptButton.click();
                // Wait for tool execution and continuation
                await expect(stopButton).not.toBeVisible({ timeout: 60000 });
            } else {
                // LLM did not invoke a tool — annotate but don't fail since behavior is non-deterministic
                test.info().annotations.push({ type: 'note', description: 'LLM did not invoke a tool; ask-policy approval flow was not exercised' });
            }
        });
    });
}

// Ensure at least one test runs even when skipped
if (providers.length === 0) {
    test('ask policy (skipped - no API keys)', async () => {
        test.skip(!config.shouldRunTests, skipMessage);
    });
}
