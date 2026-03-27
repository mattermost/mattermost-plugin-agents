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
 * Test Suite: Disabled Tool Excluded (Real API) (4.10)
 *
 * Verifies that tools with enabled=false are not exposed to the LLM and are not invoked.
 *
 * Skip-gated: requires ANTHROPIC_API_KEY or OPENAI_API_KEY.
 */

const VETTED_EMBEDDED_TOOLS = [
    'read_post',
    'read_channel',
    'get_channel_info',
    'get_channel_members',
    'get_team_info',
    'get_team_members',
    'search_posts',
    'search_users',
    'get_user_channels',
];

const config = getAPIConfig();
const skipMessage =
    'Skipping disabled-tool tests: No ANTHROPIC_API_KEY or OPENAI_API_KEY found in environment.';
const REAL_API_SETUP_TIMEOUT_MS = 180000;

const providers = config.shouldRunTests ? getAvailableProviders() : [];

for (const provider of providers) {
    test.describe(`Disabled Tool Excluded (${provider.name})`, () => {
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

        test('disabled tool not in user tools API and not invoked in RHS', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(120000);

            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);

            await mmPage.login(mattermost.url(), 'regularuser', 'regularuser');

            const apiHelper = await createToolConfigAPIHelper(mattermost);
            const adminClient = await mattermost.getAdminClient();
            const token = adminClient.getToken();

            const embeddedToolConfigs = VETTED_EMBEDDED_TOOLS.map((name) => ({
                name,
                policy: 'auto_run',
                enabled: name !== 'get_channel_info',
            }));

            await apiHelper.setEmbeddedServerToolConfigs(embeddedToolConfigs);

            const toolsResponse = await apiHelper.getUserMCPTools(
                mattermost.url(),
                token,
            );

            const embeddedServer = toolsResponse.servers.find((s: any) =>
                s.tools?.some((t: any) => t.name === 'get_channel_info'),
            );
            expect(embeddedServer).toBeDefined();
            const channelInfo = embeddedServer.tools.find(
                (t: any) => t.name === 'get_channel_info',
            );
            expect(channelInfo?.enabled).toBe(false);

            await aiPlugin.openRHS();

            await aiPlugin.sendMessage(
                'Use the get_channel_info tool to list channels in this team. Be concise.',
            );

            const stopButton = page.getByRole('button', { name: /stop/i });
            await expect(stopButton).not.toBeVisible({ timeout: 90000 });

            const rhsContainer = page.getByTestId('mattermost-ai-rhs');
            await expect(rhsContainer).toBeVisible();

            await expect(
                rhsContainer.getByText('get_channel_info', { exact: true }),
            ).toHaveCount(0);
        });
    });
}

// Ensure at least one test runs even when skipped
if (providers.length === 0) {
    test('disabled tool excluded (skipped - no API keys)', async () => {
        test.skip(!config.shouldRunTests, skipMessage);
    });
}
