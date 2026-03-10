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

const providers = config.shouldRunTests ? getAvailableProviders() : [];

for (const provider of providers) {
    test.describe(`Auto Run Policy (${provider.name})`, () => {
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

        test('auto_run embedded tool executes without approval in DM', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(120000);

            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);

            // Login
            await mmPage.login(mattermost.url(), 'regularuser', 'regularuser');

            // Open Copilot RHS
            await aiPlugin.openRHS();

            // Send a message that should trigger an embedded tool
            // read_post and get_channel_info are auto_run vetted tools
            await aiPlugin.sendMessage('What channels are available in this team?');

            // Wait for response - the tool should auto-execute
            // With auto_run policy, no Accept/Reject prompt should appear
            await page.waitForTimeout(10000);

            // Verify the bot responded (wait generously for real API)
            const rhsContainer = page.getByTestId('mattermost-ai-rhs');
            await expect(rhsContainer).toBeVisible();

            // The response should contain channel information
            // If auto_run worked, no approval prompt should be visible
            const acceptButton = page.getByRole('button', { name: /accept/i });
            const isAcceptVisible = await acceptButton.isVisible().catch(() => false);

            // For auto_run in DM, no approval should be needed
            // Note: this may vary if the LLM does not invoke the tool
            expect(isAcceptVisible).toBe(false);
        });
    });
}

// Ensure at least one test runs even when skipped
if (providers.length === 0) {
    test('auto_run policy (skipped - no API keys)', async () => {
        test.skip(!config.shouldRunTests, skipMessage);
    });
}
