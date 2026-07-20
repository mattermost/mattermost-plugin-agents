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
 * Test Suite: Rich create_post tool card (renderer registry)
 *
 * Uses Smocker to return a synthetic create_post tool call and verifies the
 * rich card renders the resolved channel and message body (not a raw JSON
 * blob), and that "View raw" exposes the exact arguments payload. The "ask"
 * policy keeps the card in the pending approval stage.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const embeddedCreatePostTool = 'mattermost__create_post';
const createPostLabel = 'Create Post';

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

test.describe('Rich create_post tool card (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await setEmbeddedToolPolicies([
            {name: 'create_post', policy: 'ask', enabled: true},
        ]);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('renders the resolved channel and message, and View raw shows the exact payload', async ({ page }) => {
        test.setTimeout(120000);

        const townSquareChannelID = await getTownSquareChannelID();
        const userMessage = 'Post to town square ' + Date.now();
        const postBody = 'Deploy is complete — thanks everyone!';
        const createPostArgs = {
            channel_id: townSquareChannelID,
            channel_display_name: 'Town Square',
            team_display_name: 'test',
            message: postBody,
        };

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
                    body: buildTextResponse('Town square post'),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',

                    // The main turn includes the embedded tools list; title
                    // generation runs WithToolsDisabled, so "create_post" is a
                    // reliable differentiator for the tool-call request.
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'create_post',
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildToolCallResponse(
                        'call_rich_create_post',
                        embeddedCreatePostTool,
                        JSON.stringify(createPostArgs),
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

        // Rich card header uses the prettified display name.
        await expect(botPost.getByText(createPostLabel, {exact: true})).toBeVisible({timeout: 30000});

        // The channel chip resolves channel_id to the Town Square channel, and
        // the message body renders as readable text (not a JSON blob).
        await expect(botPost.getByText('Town Square')).toBeVisible({timeout: 30000});
        await expect(botPost.getByText(postBody)).toBeVisible({timeout: 30000});

        // "ask" policy keeps the call in the approval stage.
        await expect(rhs.getByRole('button', {name: /^accept$/i})).toBeVisible({timeout: 30000});

        // The shared shell exposes View raw, which reveals the exact payload,
        // including the raw channel_id the chip resolved.
        await botPost.getByText('View raw', {exact: true}).click();
        await expect(botPost.getByText(new RegExp(townSquareChannelID))).toBeVisible({timeout: 30000});
    });
});
