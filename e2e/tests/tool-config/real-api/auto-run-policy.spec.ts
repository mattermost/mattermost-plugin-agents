import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { RunToolConfigRealAPIContainer, REAL_API_BEFORE_ALL_TIMEOUT_MS } from 'helpers/real-api-container';
import {
    getAPIConfig,
    getAvailableProviders,
} from 'helpers/api-config';

/**
 * Test Suite: Auto Run (DM) Policy (Real API) (4.8)
 *
 * Verifies that tools configured with the legacy auto_run policy execute without
 * user approval in a DM with the bot.
 *
 * Skip-gated: requires ANTHROPIC_API_KEY or OPENAI_API_KEY.
 */

const config = getAPIConfig();
const skipMessage =
    'Skipping auto-run policy tests: No ANTHROPIC_API_KEY or OPENAI_API_KEY found in environment.';
const providers = config.shouldRunTests ? getAvailableProviders() : [];

for (const provider of providers) {
    test.describe(`Auto Run (DM) Policy (${provider.name})`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            test.setTimeout(REAL_API_BEFORE_ALL_TIMEOUT_MS);
            mattermost = await RunToolConfigRealAPIContainer({
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
            test.setTimeout(180000);

            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);

            // Login
            await mmPage.login(mattermost.url(), 'regularuser', 'regularuser');

            // Open Copilot RHS
            await aiPlugin.openRHS();

            // Give the model an explicit channel_id — otherwise some models answer with clarifying
            // text instead of calling get_channel_info (seen on Anthropic).
            const userClient = await mattermost.getClient('regularuser', 'regularuser');
            const teams = await userClient.getMyTeams();
            const channels = await userClient.getMyChannels(teams[0].id);
            const townSquare = channels.find((c) => c.name === 'town-square');
            if (!townSquare) {
                throw new Error('e2e setup: town-square channel not found');
            }

            // Force a tool call: get_channel_info is vetted auto_run on the embedded server
            // and now surfaced in the console as "Auto Run (DM)".
            await aiPlugin.sendMessage(
                `Call the get_channel_info tool now with channel_id "${townSquare.id}". ` +
                    'Do not reply without calling the tool.',
            );

            const stopButton = page.getByRole('button', { name: /stop/i });
            await expect(stopButton).not.toBeVisible({ timeout: 120000 });

            const rhsContainer = page.getByTestId('mattermost-ai-rhs');
            await expect(rhsContainer).toBeVisible();

            const acceptButton = page.getByRole('button', { name: /accept/i });
            await expect(acceptButton).not.toBeVisible();

            // Prefer the Auto-approved badge; some provider streams surface tool output before/without the badge.
            const autoApprovedBadge = rhsContainer.getByText('Auto-approved').first();
            const toolResultFromGetChannelInfo = rhsContainer.getByText(/Channel Information:/i).first();
            await expect(autoApprovedBadge.or(toolResultFromGetChannelInfo)).toBeVisible({
                timeout: 120000,
            });
        });
    });
}

// Ensure at least one test runs even when skipped
if (providers.length === 0) {
    test('auto_run DM policy (skipped - no API keys)', async () => {
        test.skip(!config.shouldRunTests, skipMessage);
    });
}
