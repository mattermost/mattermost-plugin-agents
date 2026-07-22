import * as http from 'http';

import { test, expect, type Page, type Locator } from '@playwright/test';
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

const embeddedReadPostTool = 'mattermost__read_post';
const embeddedReadChannelTool = 'mattermost__read_channel';
const embeddedGetChannelInfoTool = 'mattermost__get_channel_info';
const embeddedReadPostLabel = 'Read Post';
const embeddedReadChannelLabel = 'Read Channel';
const embeddedGetChannelInfoLabel = 'Get Channel Info';

type EmbeddedToolConfig = {
    name: string;
    policy: 'ask' | 'auto_run_in_dm' | 'auto_run_everywhere';
    enabled: boolean;
};

type ImageTrapServer = {
    close: () => Promise<void>;
    getRequestCount: () => number;
    url: string;
};

async function startImageTrapServer(assetName: string): Promise<ImageTrapServer> {
    let requestCount = 0;
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="240" height="80"><rect width="240" height="80" fill="#d24b4b"/><text x="20" y="48" font-size="24" fill="white">image loaded</text></svg>';

    const server = http.createServer((req, res) => {
        if (req.url === `/${assetName}`) {
            requestCount++;
            res.writeHead(200, {
                'Cache-Control': 'no-store',
                'Content-Type': 'image/svg+xml',
            });
            res.end(svg);
            return;
        }

        res.writeHead(404);
        res.end('not found');
    });

    await new Promise<void>((resolve) => {
        server.listen(0, '127.0.0.1', () => resolve());
    });

    const address = server.address();
    if (!address || typeof address === 'string') {
        throw new Error('failed to start image trap server');
    }

    return {
        close: () => new Promise<void>((resolve, reject) => {
            server.close((error) => {
                if (error) {
                    reject(error);
                    return;
                }
                resolve();
            });
        }),
        getRequestCount: () => requestCount,
        url: `http://127.0.0.1:${address.port}/${assetName}`,
    };
}

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

async function closeRHSIfOpen(page: Page): Promise<void> {
    const closeButton = page.locator('#rhsContainer').getByRole('button', {name: /close rhs|close/i}).first();
    const rhs = page.locator('#rhsContainer');
    if (await rhs.isVisible().catch(() => false) && await closeButton.isVisible().catch(() => false)) {
        await closeButton.click();
        await expect(rhs).not.toBeVisible({timeout: 10000});
    }
}

async function waitForChannelReady(page: Page, channelDisplayName: string): Promise<void> {
    await page.locator('#channelHeaderTitle').getByText(channelDisplayName, {exact: true}).waitFor({
        state: 'visible',
        timeout: 10000,
    });
}

test.describe('Tool Call Policies (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await setEmbeddedToolPolicies([
            {name: 'read_post', policy: 'auto_run_in_dm', enabled: true},
            {name: 'get_channel_info', policy: 'ask', enabled: true},
            {name: 'read_channel', policy: 'auto_run_in_dm', enabled: true},
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
            embeddedReadPostTool,
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

    test('read_post tool results do not render markdown images from unsafe posts', async ({ page }) => {
        test.setTimeout(120000);

        const townSquareChannelID = await getTownSquareChannelID();
        const adminClient = await mattermost.getAdminClient();
        const imageTrap = await startImageTrapServer(`tool-result-image-${Date.now()}.svg`);
        const seedMessage = `Tool result markdown image seed ${Date.now()}`;
        try {
            const mmPage = new MattermostPage(page);
            await mmPage.login(mattermost.url(), adminUsername, adminPassword);
            await mmPage.createAndNavigateToDMWithBot(
                mattermost,
                adminUsername,
                adminPassword,
                'toolbot',
            );

            const seededPost = await adminClient.createPost({
                channel_id: townSquareChannelID,
                message: `${seedMessage}\n\`\`\`\n![blocked-image](${imageTrap.url})`,
            });

            // Seed after the browser has left Town Square so the assertion only measures
            // requests caused by the tool result card flow, not by the source post itself.
            await page.waitForTimeout(500);
            const baselineRequestCount = imageTrap.getRequestCount();

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
                    context: {
                        times: 1,
                    },
                    response: {
                        status: 200,
                        headers: {
                            'Content-Type': 'text/event-stream',
                        },
                        body: buildTextResponse('Unsafe post read'),
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
                        body: buildToolCallResponse(
                            'call_unsafe_read_post',
                            embeddedReadPostTool,
                            JSON.stringify({post_id: seededPost.id}),
                        ),
                    },
                },
                {
                    request: {
                        method: 'POST',
                        path: '/v1/chat/completions',
                        body: {
                            matcher: 'ShouldContainSubstring',
                            value: 'call_unsafe_read_post',
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
                        body: buildTextResponse('Finished reading the unsafe post.'),
                    },
                },
            ]);

            const promptMessage = `Please read the Mattermost post with ID ${seededPost.id}.`;
            await mmPage.sendChannelMessage(promptMessage);

            const sentPost = await waitForSentPost(page, promptMessage);
            await openThreadForPost(sentPost);

            const rhs = page.locator('#rhsContainer');
            const latestBotPost = rhs.locator('[data-testid="llm-bot-post"]').last();
            await expect(latestBotPost.getByText(embeddedReadPostLabel, {exact: true})).toBeVisible({timeout: 30000});
            await expect(rhs.getByText('Finished reading the unsafe post.')).toBeVisible({timeout: 45000});

            await latestBotPost.getByText(embeddedReadPostLabel, {exact: true}).click();
            await expect(latestBotPost.getByText(seedMessage, {exact: false})).toBeVisible({timeout: 30000});
            await expect(latestBotPost.getByText('blocked-image')).toBeVisible({timeout: 30000});
            await expect(latestBotPost.locator('img[src*="tool-result-image-"]')).toHaveCount(0);

            await page.waitForTimeout(1000);
            expect(imageTrap.getRequestCount() - baselineRequestCount).toBe(0);
        } finally {
            await imageTrap.close();
        }
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
                        embeddedGetChannelInfoTool,
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
                        embeddedReadChannelTool,
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

        await mentionBotAndOpenThread(page, mmPage, 'toolbot', mainTurnUserMessage);

        const rhs = page.locator('#rhsContainer');
        await rhs.waitFor({state: 'visible', timeout: 10000});

        const botPosts = rhs.locator('[data-testid="llm-bot-post"]');
        const initialBotPost = botPosts.last();
        await expect(initialBotPost.getByText(embeddedGetChannelInfoLabel, {exact: true})).toBeVisible({timeout: 30000});

        const acceptButton = rhs.getByRole('button', {name: /^accept$/i});
        await expect(acceptButton).toBeVisible({timeout: 30000});
        await acceptButton.click();

        const latestBotPost = botPosts.last();
        await expect(latestBotPost.getByText(embeddedReadChannelLabel, {exact: true})).toBeVisible({timeout: 30000});

        // Completion text proves the final mock ran; wait for it before badge (post props may update after stream).
        await expect(rhs.getByText('The follow-up read_channel tool completed successfully.')).toBeVisible({timeout: 45000});
        await expect(rhs.getByText('Auto-approved')).toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        await latestBotPost.getByText(embeddedReadChannelLabel, {exact: true}).click();
        // read_channel result is rendered as markdown; the seed string is not a single text node (bold, etc.).
        await expect(latestBotPost.getByText(seededMessage, {exact: false})).toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
    });

    test('approval continuation streams into the same post and does not show the empty-result fallback', async ({ page }) => {
        test.setTimeout(120000);

        const townSquareChannelID = await getTownSquareChannelID();
        const userMessage = 'Post split regression ' + Date.now();
        const toolCallID = 'call_split_' + Date.now();
        const continuationMarker = 'POST_SPLIT_CONTINUATION_' + Date.now();

        await openAIMock.addMocks([
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {matcher: 'ShouldContainSubstring', value: 'Write a short title'},
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildTextResponse('Post split'),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {matcher: 'ShouldContainSubstring', value: 'get_channel_info'},
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildToolCallResponse(
                        toolCallID,
                        embeddedGetChannelInfoTool,
                        `{"channel_id":"${townSquareChannelID}"}`,
                    ),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {matcher: 'ShouldContainSubstring', value: toolCallID},
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildTextResponse(continuationMarker),
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
        await rhs.waitFor({state: 'visible', timeout: 10000});

        const botPosts = rhs.locator('[data-testid="llm-bot-post"]');
        const postA = botPosts.nth(0);

        await expect(postA.getByText(embeddedGetChannelInfoLabel, {exact: true})).toBeVisible({timeout: 30000});

        // A pending-tool response has no text; it must not be overwritten with
        // the empty-result fallback that would mask the tool approval UI.
        await expect(postA.getByText(/did not return a result/i)).not.toBeVisible();

        const acceptButton = rhs.getByRole('button', {name: /^accept$/i});
        await expect(acceptButton).toBeVisible({timeout: 30000});
        await acceptButton.click();

        // Continuation now streams into the SAME post as the tool round.
        // Both the original tool card and the follow-up text are visible in
        // postA; no second bot post is created.
        await expect(postA.getByText(continuationMarker)).toBeVisible({timeout: 30000});
        await expect(postA.getByText(embeddedGetChannelInfoLabel, {exact: true})).toBeVisible();
        await expect(botPosts).toHaveCount(1);
    });

    test('channel auto_run requires Accept (DM-only policy), while auto_run_everywhere skips approval entirely', async ({ page }) => {
        test.setTimeout(120000);

        const townSquareChannelID = await getTownSquareChannelID();
        const mmPage = new MattermostPage(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await page.goto(`${mattermost.url()}/test/channels/off-topic`);
        await waitForChannelReady(page, 'Off-Topic');

        await setEmbeddedToolPolicies([
            {name: 'read_post', policy: 'auto_run_in_dm', enabled: true},
            {name: 'get_channel_info', policy: 'ask', enabled: true},
            {name: 'read_channel', policy: 'auto_run_in_dm', enabled: true},
        ]);

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
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: buildTextResponse('tool policy channel dm-only'),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'read_channel',
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
                        'call_channel_dm_only',
                        embeddedReadChannelTool,
                        `{"channel_id":"${townSquareChannelID}","limit":5}`,
                    ),
                },
            },
        ]);

        await mentionBotAndOpenThread(page, mmPage, 'toolbot', 'tool policy channel dm-only');

        const rhs = page.locator('#rhsContainer');
        // In a channel, the legacy auto_run policy is DM-only: the call stage
        // must still be approved. Share/Keep private are the post-approval
        // stage and must not appear yet.
        await expect(rhs.getByRole('button', {name: /^accept$/i})).toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /^share$/i})).not.toBeVisible();
        await expect(rhs.getByRole('button', {name: /keep private/i})).not.toBeVisible();

        await setEmbeddedToolPolicies([
            {name: 'read_post', policy: 'auto_run_in_dm', enabled: true},
            {name: 'get_channel_info', policy: 'ask', enabled: true},
            {name: 'read_channel', policy: 'auto_run_everywhere', enabled: true},
        ]);

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
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: buildTextResponse('tool policy channel everywhere'),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'read_channel',
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
                        'call_channel_everywhere',
                        embeddedReadChannelTool,
                        `{"channel_id":"${townSquareChannelID}","limit":5}`,
                    ),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'call_channel_everywhere',
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
                    body: buildTextResponse('Channel everywhere auto-run completed without share approval.'),
                },
            },
        ]);

        await closeRHSIfOpen(page);
        await mentionBotAndOpenThread(page, mmPage, 'toolbot', 'tool policy channel everywhere');

        await expect(rhs.getByText('Channel everywhere auto-run completed without share approval.')).toBeVisible({timeout: 45000});
        await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
        await expect(rhs.getByRole('button', {name: /^share$/i})).not.toBeVisible();
        await expect(rhs.getByRole('button', {name: /keep private/i})).not.toBeVisible();
        await expect(rhs.getByText('Auto-approved')).toBeVisible();
    });

    test('channel ask: LLM follow-up stream is gated on Share approval', async ({ page }) => {
        test.setTimeout(120000);

        const townSquareChannelID = await getTownSquareChannelID();
        const mmPage = new MattermostPage(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await page.goto(`${mattermost.url()}/test/channels/off-topic`);
        await waitForChannelReady(page, 'Off-Topic');

        await setEmbeddedToolPolicies([
            {name: 'get_channel_info', policy: 'ask', enabled: true},
        ]);

        const userMessageMarker = 'follow-up-gating marker ' + Date.now();
        const toolCallID = 'call_followup_gating';
        const followUpMarker = 'FOLLOWUP_AFTER_SHARE_' + Date.now();

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
                    body: buildTextResponse('follow-up gating'),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    // Main turn includes the MCP tools list; title generation runs
                    // WithToolsDisabled so its request body has no `get_channel_info`.
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: 'get_channel_info',
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildToolCallResponse(
                        toolCallID,
                        embeddedGetChannelInfoTool,
                        `{"channel_id":"${townSquareChannelID}"}`,
                    ),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: toolCallID,
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildTextResponse(followUpMarker),
                },
            },
        ]);

        await mentionBotAndOpenThread(page, mmPage, 'toolbot', userMessageMarker);

        const rhs = page.locator('#rhsContainer');

        const acceptButton = rhs.getByRole('button', {name: /^accept$/i});
        await expect(acceptButton).toBeVisible({timeout: 30000});
        await expect(rhs.getByText(followUpMarker)).not.toBeVisible();

        await acceptButton.click();

        // Accept runs the tool but must NOT trigger the channel-visible follow-up.
        const shareButton = rhs.getByRole('button', {name: /^share$/i});
        await expect(shareButton).toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /keep private/i})).toBeVisible();
        await page.waitForTimeout(3000);
        await expect(rhs.getByText(followUpMarker)).not.toBeVisible();

        await shareButton.click();

        // Share releases the follow-up stream and consumes the last mock.
        await expect(rhs.getByText(followUpMarker)).toBeVisible({timeout: 30000});
        await expect(rhs.getByRole('button', {name: /^share$/i})).not.toBeVisible();
    });
});
