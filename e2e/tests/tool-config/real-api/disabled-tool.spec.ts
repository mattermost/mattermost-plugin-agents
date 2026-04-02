import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { RunRealAPIContainer, REAL_API_BEFORE_ALL_TIMEOUT_MS } from 'helpers/real-api-container';
import {
    getAPIConfig,
    getAvailableProviders,
} from 'helpers/api-config';
import { createToolConfigAPIHelper } from 'helpers/tool-config';

/**
 * Test Suite: Disabled Tool Excluded (Real API) (4.10)
 *
 * Verifies that disabling an embedded tool removes it from the filtered tool list and prevents
 * a real-API prompt from surfacing that tool in the Copilot RHS.
 *
 * The test disables `read_post`, verifies it is filtered out of GET /mcp/tools (the same list
 * used by GetToolsForUser/filterToolsByConfig), then prompts the real model to use `read_post`
 * on a seeded post ID. The assertions stay focused on the behavior we care about: the disabled
 * tool never appears in the turn, and the seeded post contents are not surfaced.
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

const TARGET_TOOL_NAME = 'read_post';
const TARGET_TOOL_LABEL = 'Read Post';
const SEEDED_POST_MESSAGE =
    'Disabled tool e2e seed message: cobalt narwhal orchard 4821.';

const config = getAPIConfig();
const skipMessage =
    'Skipping disabled-tool tests: No ANTHROPIC_API_KEY or OPENAI_API_KEY found in environment.';
const providers = config.shouldRunTests ? getAvailableProviders() : [];

function buildEmbeddedToolConfigs(disabledToolNames: string[] = []) {
    const disabledTools = new Set(disabledToolNames);

    return VETTED_EMBEDDED_TOOLS.map((name) => ({
        name,
        policy: 'auto_run',
        enabled: !disabledTools.has(name),
    }));
}

for (const provider of providers) {
    test.describe(`Disabled Tool Excluded (${provider.name})`, () => {
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

        test('disabled embedded tool is filtered out and does not surface in a real RHS turn', async ({ page }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(120000);

            const apiHelper = await createToolConfigAPIHelper(mattermost);
            const userClient = await mattermost.getClient('regularuser', 'regularuser');
            const token = userClient.getToken();
            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);

            const teams = await userClient.getMyTeams();
            const team = teams[0];
            const channels = await userClient.getMyChannels(team.id);
            const defaultChannel = channels.find((channel) => channel.name === 'town-square') || channels[0];
            const seededPost = await userClient.createPost({
                channel_id: defaultChannel.id,
                message: SEEDED_POST_MESSAGE,
            });
            const toolPrompt =
                `Use the ${TARGET_TOOL_NAME} tool to read the Mattermost post with ID ${seededPost.id}. ` +
                'Summarize its contents. Do not answer from memory. Call the tool now.';

            await apiHelper.setEmbeddedServerToolConfigs(buildEmbeddedToolConfigs([TARGET_TOOL_NAME]));

            const toolsResponse = await apiHelper.getUserMCPTools(token);

            const embeddedServer = toolsResponse.servers.find(
                (s: any) => s.serverOrigin === 'embedded://mattermost',
            );
            expect(embeddedServer).toBeDefined();

            const names = embeddedServer.tools.map((t: any) => t.name);
            expect(names).not.toContain(TARGET_TOOL_NAME);
            expect(names).toContain('get_channel_info');

            await mmPage.login(mattermost.url(), 'regularuser', 'regularuser');
            await aiPlugin.openRHS();

            const rhsContainer = page.getByTestId('mattermost-ai-rhs');
            const botPosts = rhsContainer.locator('[data-testid="llm-bot-post"]');
            const stopButton = page.getByRole('button', { name: /stop/i });

            await aiPlugin.resetState();

            const botPostCount = await botPosts.count();
            await aiPlugin.sendMessage(toolPrompt);
            await expect(botPosts).toHaveCount(botPostCount + 1, { timeout: 90000 });
            const disabledBotPost = botPosts.last();

            await expect(stopButton).not.toBeVisible({ timeout: 90000 });
            await expect(disabledBotPost).toBeVisible();
            await expect(disabledBotPost.getByTestId('posttext')).toBeVisible({ timeout: 90000 });
            await expect(disabledBotPost.getByText(TARGET_TOOL_LABEL, { exact: true })).toHaveCount(0);
            await expect(disabledBotPost.getByTestId('posttext')).not.toContainText(SEEDED_POST_MESSAGE);
        });
    });
}

// Ensure at least one test runs even when skipped
if (providers.length === 0) {
    test('disabled tool excluded (skipped - no API keys)', async () => {
        test.skip(!config.shouldRunTests, skipMessage);
    });
}
