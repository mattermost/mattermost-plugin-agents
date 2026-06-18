import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { AIMockContainer, RunAIMockSidecar } from 'helpers/aimock-container';
import { buildTextResponse } from 'helpers/aimock-fixtures';
import { RunToolConfigAIMockContainer, setupRegularTestUser } from 'helpers/tool-config-container';
import { createToolConfigAPIHelper } from 'helpers/tool-config';

const username = 'regularuser';
const password = 'regularuser';

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
const SEEDED_POST_MESSAGE = 'Disabled tool e2e seed message: cobalt narwhal orchard 4821.';

function buildEmbeddedToolConfigs(disabledToolNames: string[] = []) {
    const disabledTools = new Set(disabledToolNames);

    return VETTED_EMBEDDED_TOOLS.map((name) => ({
        name,
        policy: 'auto_run_in_dm' as const,
        enabled: !disabledTools.has(name),
    }));
}

test.describe('Disabled Tool Excluded (Aimock)', () => {
    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunToolConfigAIMockContainer(buildEmbeddedToolConfigs([TARGET_TOOL_NAME]));
        await setupRegularTestUser(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: buildTextResponse({
                userMessage: 'aimock-disabled-tool-placeholder',
                content: 'placeholder',
            }),
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('disabled embedded tool is filtered out and does not surface in RHS turn', async ({ page }) => {
        test.setTimeout(120000);

        const apiHelper = await createToolConfigAPIHelper(mattermost);
        const userClient = await mattermost.getClient(username, password);
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
            `aimock disabled read_post should not be available. The disabled post id is ${seededPost.id}.`;

        await aimock.setFixtures(
            buildTextResponse({
                userMessage: toolPrompt,
                content: 'Aimock disabled-tool response without using read_post.',
                title: 'Aimock disabled tool',
            }),
        );

        const toolsResponse = await apiHelper.getUserMCPTools(token);
        const embeddedServer = toolsResponse.servers.find(
            (s: { serverOrigin: string }) => s.serverOrigin === 'embedded://mattermost',
        );
        expect(embeddedServer).toBeDefined();

        const names = embeddedServer.tools.map((t: { name: string }) => t.name);
        expect(names).not.toContain(TARGET_TOOL_NAME);
        expect(names).toContain('get_channel_info');

        await mmPage.login(mattermost.url(), username, password);
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
        await expect(disabledBotPost.getByText('Aimock disabled-tool response without using read_post.')).toBeVisible({
            timeout: 90000,
        });
        await expect(disabledBotPost.getByText(TARGET_TOOL_LABEL, { exact: true })).toHaveCount(0);
        await expect(rhsContainer.getByRole('button', { name: /^accept$/i })).not.toBeVisible();
        await expect(rhsContainer.getByRole('button', { name: /^reject$/i })).not.toBeVisible();
        await expect(rhsContainer.getByText('Auto-approved')).not.toBeVisible();
    });
});
