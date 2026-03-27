import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { RunRealAPIContainer } from 'helpers/real-api-container';
import {
    getAPIConfig,
    getAvailableProviders,
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

            // Force a tool call: get_channel_info is vetted auto_run on the embedded server.
            await aiPlugin.sendMessage(
                'Use the get_channel_info tool to list channels in this team. Call the tool.',
            );

            const stopButton = page.getByRole('button', { name: /stop/i });
            await expect(stopButton).not.toBeVisible({ timeout: 90000 });

            const rhsContainer = page.getByTestId('mattermost-ai-rhs');
            await expect(rhsContainer).toBeVisible();

            const acceptButton = page.getByRole('button', { name: /accept/i });
            await expect(acceptButton).not.toBeVisible();

            await expect(rhsContainer.getByText('Auto-approved').first()).toBeVisible({
                timeout: 90000,
            });
        });
    });
}

// Ensure at least one test runs even when skipped
if (providers.length === 0) {
    test('auto_run policy (skipped - no API keys)', async () => {
        test.skip(!config.shouldRunTests, skipMessage);
    });
}
