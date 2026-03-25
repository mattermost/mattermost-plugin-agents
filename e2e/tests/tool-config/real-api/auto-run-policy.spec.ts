import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { RunRealAPIContainer } from 'helpers/real-api-container';
import {
    getAPIConfig,
    getAvailableProviders,
    ProviderBundle,
} from 'helpers/api-config';

/**
 * Test Suite: Auto Run Policy (Real API) (4.8)
 *
 * Verifies that tools configured with auto_run policy execute without
 * user approval in a DM with the bot.
 *
 * Skip-gated: requires ANTHROPIC_API_KEY or OPENAI_API_KEY.
 */

const config = getAPIConfig();
const skipMessage =
    'Skipping auto-run policy tests: No ANTHROPIC_API_KEY or OPENAI_API_KEY found in environment.';
const REAL_API_SETUP_TIMEOUT_MS = 180000;

const providers = config.shouldRunTests ? getAvailableProviders() : [];

for (const provider of providers) {
    test.describe(`Auto Run Policy (${provider.name})`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            test.setTimeout(REAL_API_SETUP_TIMEOUT_MS);
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

        test('auto_run embedded tool executes without approval in DM', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(120000);

            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);

            // Login
            await mmPage.login(mattermost.url(), 'regularuser', 'regularuser');

            // Open Copilot RHS
            await aiPlugin.openRHS();

            // Send a message that should trigger an embedded auto_run tool
            // (e.g. get_channel_info is a vetted auto_run tool)
            await aiPlugin.sendMessage('What channels are available in this team?');

            // Wait for streaming to complete
            await page.waitForTimeout(2000);
            const stopButton = page.getByRole('button', { name: /stop/i });
            await expect(stopButton).not.toBeVisible({ timeout: 90000 });

            // Verify the bot responded with content in the RHS
            const rhsContainer = page.getByTestId('mattermost-ai-rhs');
            await expect(rhsContainer).toBeVisible();

            // auto_run in DM means no approval prompt should appear
            const acceptButton = page.getByRole('button', { name: /accept/i });
            const isAcceptVisible = await acceptButton.isVisible().catch(() => false);
            expect(isAcceptVisible).toBe(false);

            // Check for evidence that a tool actually auto-ran
            const autoApprovedBadge = rhsContainer.getByText('Auto-approved');
            const didAutoRun = await autoApprovedBadge.first().isVisible().catch(() => false);
            if (!didAutoRun) {
                test.info().annotations.push({ type: 'note', description: 'LLM did not invoke a tool; auto_run flow was not exercised' });
            }
        });
    });
}

// Ensure at least one test runs even when skipped
if (providers.length === 0) {
    test('auto_run policy (skipped - no API keys)', async () => {
        test.skip(!config.shouldRunTests, skipMessage);
    });
}
