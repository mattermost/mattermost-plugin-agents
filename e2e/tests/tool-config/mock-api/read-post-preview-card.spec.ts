import { test, expect, type Page, type Locator } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildToolCallResponse,
    buildTextResponse,
} from 'helpers/openai-mock';
import { RunToolConfigContainerWithPolicies } from 'helpers/tool-config-container';
import { adminUsername, adminPassword } from 'helpers/system-console-container';
import { createBotConfigHelper } from 'helpers/bot-config';

/**
 * Test Suite: read_post preview card (renderer registry)
 *
 * Uses Smocker to return a synthetic read_post tool call referencing a seeded
 * post and verifies the pending approval card shows a permalink-style preview
 * of that post (so the user sees what the tool will read before approving),
 * with "View raw" still exposing the exact arguments payload. The "ask" policy
 * keeps the card in the pending approval stage.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const embeddedReadPostTool = 'mattermost__read_post';
const readPostLabel = 'Read Post';

type EmbeddedToolConfig = {
    name: string;
    policy: 'ask' | 'auto_run_in_dm' | 'auto_run_everywhere';
    enabled: boolean;
};

async function setEmbeddedToolPolicies(toolConfigs: EmbeddedToolConfig[]) {
    const helper = await createBotConfigHelper(mattermost);
    const pluginConfig = await helper.getPluginConfig();

    if (!pluginConfig.config.mcp) {
        throw new Error('mattermost-ai MCP config is not available');
    }

    pluginConfig.config.mcp.embeddedServer = {
        ...(pluginConfig.config.mcp.embeddedServer || {}),
        enabled: true,
        tool_configs: toolConfigs,
    };

    await helper.updatePluginConfig(pluginConfig);
}

async function getTownSquareChannelID(): Promise<string> {
    const adminClient = await mattermost.getAdminClient();
    const teams = await adminClient.getMyTeams();
    const defaultTeam = teams[0];
    const channels = await adminClient.getMyChannels(defaultTeam.id);
    const townSquare = channels.find((channel) => channel.name === 'town-square');

    if (!townSquare) {
        throw new Error('town-square channel not found');
    }

    return townSquare.id;
}

async function waitForSentPost(page: Page, message: string, timeout: number = 30000): Promise<Locator> {
    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, {exact: true}),
    }).last();
    await expect(post).toBeVisible({timeout});
    return post;
}

async function openThreadForPost(post: Locator, timeout: number = 30000): Promise<void> {
    const replyIndicator = post.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({timeout});
    await replyIndicator.click();
    const rhs = post.page().locator('#rhsContainer');
    await rhs.waitFor({state: 'visible', timeout: 10000});
    await rhs.locator('[data-testid="llm-bot-post"]').first().waitFor({state: 'visible', timeout: 10000});
}

async function mentionBotAndOpenThread(page: Page, mmPage: MattermostPage, botName: string, message: string, timeout: number = 30000): Promise<void> {
    await mmPage.mentionBot(botName, message);
    const post = await waitForSentPost(page, `@${botName} ${message}`, timeout);
    await openThreadForPost(post, timeout);
}

test.describe('read_post preview card (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await setEmbeddedToolPolicies([
            {name: 'read_post', policy: 'ask', enabled: true},
        ]);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('shows a preview of the referenced post before approval, with View raw', async ({ page }) => {
        test.setTimeout(120000);

        const townSquareChannelID = await getTownSquareChannelID();
        const adminClient = await mattermost.getAdminClient();
        const seededMessage = `Post preview seed ${Date.now()}`;
        const seededPost = await adminClient.createPost({
            channel_id: townSquareChannelID,
            message: seededMessage,
        });

        const userMessage = 'Read that post for me ' + Date.now();
        const readPostArgs = {post_id: seededPost.id, include_thread: false};

        await openAIMock.addMocks([
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value:
                            'Write a short title for the following request. Include only the title and nothing else, no quotations. Request:',
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildTextResponse('Post preview'),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',

                    // The main turn includes the embedded tools list; title
                    // generation runs WithToolsDisabled, so "read_post" is a
                    // reliable differentiator for the tool-call request.
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'read_post',
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildToolCallResponse(
                        'call_read_post_preview',
                        embeddedReadPostTool,
                        JSON.stringify(readPostArgs),
                    ),
                },
            },
        ]);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost,
            adminUsername,
            adminPassword,
            'toolbot',
        );

        await mentionBotAndOpenThread(page, mmPage, 'toolbot', userMessage);

        const rhs = page.locator('#rhsContainer');
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();

        // Card header + pending approval stage.
        await expect(botPost.getByText(readPostLabel, {exact: true})).toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /^accept$/i})).toBeVisible({timeout: 30000});

        // The permalink-style preview shows the seeded post's content before
        // the call is approved.
        await expect(botPost.getByText(seededMessage, {exact: false})).toBeVisible({timeout: 30000});

        // View raw exposes the exact arguments payload, including the post id.
        await botPost.getByText('View raw', {exact: true}).click();
        await expect(botPost.getByText(new RegExp(seededPost.id))).toBeVisible({timeout: 30000});
    });
});
