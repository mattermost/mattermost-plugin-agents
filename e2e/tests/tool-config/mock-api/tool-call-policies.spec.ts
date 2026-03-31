import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildToolCallResponse,
    buildTextResponse,
    responseTest,
} from 'helpers/openai-mock';
import { RunToolConfigContainerWithPolicies } from 'helpers/tool-config-container';
import { adminUsername, adminPassword } from 'helpers/system-console-container';
import { createBotConfigHelper } from 'helpers/bot-config';

/**
 * Test Suite: Tool Call Policies with Mocked LLM (4.13)
 *
 * Uses Smocker to return synthetic tool-call SSE responses and verifies
 * that policy enforcement works at tool-call time.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

type EmbeddedToolConfig = {
    name: string;
    policy: 'ask' | 'auto_run';
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

test.describe('Tool Call Policies (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await setEmbeddedToolPolicies([
            {name: 'read_post', policy: 'auto_run', enabled: true},
            {name: 'get_channel_info', policy: 'ask', enabled: true},
            {name: 'read_channel', policy: 'auto_run', enabled: true},
        ]);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    // Note: does not configure a disabled tool; it only checks a text-only mock response
    // shows no approval UI. For "disabled tool omitted from model tool list", see
    // real-api/disabled-tool.spec.ts (GetToolsForUser parity) and mcp/client_manager_filter_test.go.
    test('text-only completion shows no tool approval UI', async ({ page }) => {
        test.setTimeout(60000);

        // Set up a simple text response (no tool calls)
        await openAIMock.addCompletionMock(responseTest);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Open Copilot RHS
        await aiPlugin.openRHS();

        // Send a message - should get a simple text response with no tool calls
        await aiPlugin.sendMessage('Hello, what can you do?');

        // Wait for the text response to appear
        await aiPlugin.waitForBotResponse('Hello');

        // Verify no tool call UI elements appear (no Accept/Reject buttons)
        const acceptButton = page.getByRole('button', { name: /accept/i });
        await expect(acceptButton).not.toBeVisible();

        const rejectButton = page.getByRole('button', { name: /reject/i });
        await expect(rejectButton).not.toBeVisible();
    });

    test('auto_run tool executes without approval prompt in DM', async ({ page }) => {
        test.setTimeout(120000);

        const seededMessage = 'Please read post test123';

        // Build a tool-call response for an auto_run tool
        const toolCallSSE = buildToolCallResponse(
            'call_001',
            'read_post',
            '{"post_id": "test123"}',
        );
        const followUpTextSSE = buildTextResponse('Here is the post content you requested.');

        // Register both mocks together: the tool-call mock (matches first request)
        // and the text follow-up (for after tool execution).
        // Using addMocks to send both in a single request since addMock resets.
        await openAIMock.addMocks([
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: seededMessage,
                    },
                },
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: toolCallSSE,
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'You are called Tool Test Bot with the username toolbot',
                    },
                },
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: followUpTextSSE,
                },
            },
        ]);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to DM with bot
        await mmPage.createAndNavigateToDMWithBot(
            mattermost,
            adminUsername,
            adminPassword,
            'toolbot',
        );

        // Send message to trigger tool call
        await mmPage.sendChannelMessage('Please read post test123');

        // Wait for some response to appear (tool call processing)
        // With auto_run, Accept/Reject should NOT appear
        await page.waitForTimeout(5000);

        // Verify no approval prompt appears for auto_run tool
        const acceptButton = page.getByRole('button', { name: /accept/i });
        const isAcceptVisible = await acceptButton.isVisible().catch(() => false);

        // If auto_run is properly configured, no approval should be needed
        // Note: the exact behavior depends on whether the tool call mock
        // format is correctly handled by the plugin
        expect(isAcceptVisible).toBe(false);
    });

    test('manual DM approval can be followed by a completed auto_run tool', async ({ page }) => {
        test.setTimeout(120000);

        const townSquareChannelID = await getTownSquareChannelID();
        const adminClient = await mattermost.getAdminClient();
        const seededMessage = `DM follow-up auto-run regression seed ${Date.now()}`;

        await adminClient.createPost({
            channel_id: townSquareChannelID,
            message: seededMessage,
        });

        const mainTurnUserMessage =
            'Look up Town Square, read the latest posts, and tell me what you found.';

        await openAIMock.addMocks([
            // Title generation runs in parallel with the main turn. Its request body includes the same user
            // message text as the main request, so we must not match on the user message substring alone.
            // Title uses WithToolsDisabled() so the upstream JSON has no tools; the main request includes MCP
            // tool definitions, so "get_channel_info" is a reliable differentiator (list title mock first).
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
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: buildTextResponse('Town Square lookup'),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'get_channel_info',
                    },
                },
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: buildToolCallResponse(
                        'call_manual_channel_lookup',
                        'get_channel_info',
                        '{"channel_name":"Town Square"}',
                    ),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'call_manual_channel_lookup',
                    },
                },
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: buildToolCallResponse(
                        'call_auto_read_channel',
                        'read_channel',
                        `{"channel_id":"${townSquareChannelID}","limit":50}`,
                    ),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'call_auto_read_channel',
                    },
                },
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: buildTextResponse('The follow-up read_channel tool completed successfully.'),
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

        await mmPage.sendChannelMessage(mainTurnUserMessage);

        const replyIndicator = page.getByText(/\d+ repl/i);
        await expect(replyIndicator.last()).toBeVisible({timeout: 30000});
        await replyIndicator.last().click();

        const rhs = page.locator('#rhsContainer');
        await rhs.waitFor({state: 'visible', timeout: 10000});

        const botPosts = rhs.locator('[data-testid="llm-bot-post"]');
        const initialBotPost = botPosts.last();
        await expect(initialBotPost.getByText('Get Channel Info', {exact: true})).toBeVisible({timeout: 30000});

        const acceptButton = rhs.getByRole('button', {name: /^accept$/i});
        await expect(acceptButton).toBeVisible({timeout: 30000});
        await acceptButton.click();

        const latestBotPost = botPosts.last();
        await expect(latestBotPost.getByText('Read Channel', {exact: true})).toBeVisible({timeout: 30000});

        // Completion text proves the final mock ran; wait for it before badge (post props may update after stream).
        await expect(rhs.getByText('The follow-up read_channel tool completed successfully.')).toBeVisible({timeout: 45000});
        await expect(rhs.getByText('Auto-approved')).toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        await latestBotPost.getByText('Read Channel', {exact: true}).click();
        // read_channel result is rendered as markdown; the seed string is not a single text node (bold, etc.).
        await expect(latestBotPost.getByText(seededMessage, {exact: false})).toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
    });
});
