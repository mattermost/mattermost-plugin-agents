import { test, expect, type Page, type Locator } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildToolCallResponse,
    buildTextResponse,
} from 'helpers/openai-mock';
import { RunToolConfigContainerWithDynamicPolicies } from 'helpers/tool-config-container';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Cross-turn derivation: interaction 1 runs the full search_tools -> load_tool ->
 * business tool prelude; interaction 2 in the same conversation calls the business
 * tool directly with no prelude. The deriver must reconstruct the loaded set from
 * retained turns so the second call executes instead of hitting the unloaded hint.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const businessTool = 'mattermost__get_channel_info';
const businessToolLabel = 'Get Channel Info';
const searchToolsLabel = 'Search Tools';
const loadToolLabel = 'Load Tool';

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

test.describe('Dynamic MCP Cross-Turn Derivation (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithDynamicPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('derives loaded tools from retained turns across interactions', async ({ page }) => {
        test.setTimeout(180000);

        const turn1User = `cross-turn derivation t1 ${Date.now()}`;
        const turn2User = `cross-turn derivation t2 ${Date.now()}`;
        const searchCallID = `call_xtd_search_${Date.now()}`;
        const loadCallID = `call_xtd_load_${Date.now()}`;
        const businessCallID1 = `call_xtd_business1_${Date.now()}`;
        const businessCallID2 = `call_xtd_business2_${Date.now()}`;
        const finalMarker1 = `XTD_FINAL_TURN1_${Date.now()}`;
        const finalMarker2 = `XTD_FINAL_TURN2_${Date.now()}`;

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
                    body: buildTextResponse('Cross-turn derivation'),
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
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildToolCallResponse(
                        searchCallID,
                        'search_tools',
                        '{"query":"get channel info"}',
                    ),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: searchCallID,
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildToolCallResponse(
                        loadCallID,
                        'load_tool',
                        `{"name":"${businessTool}"}`,
                    ),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: loadCallID,
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildToolCallResponse(
                        businessCallID1,
                        businessTool,
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
                        value: businessCallID1,
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildTextResponse(finalMarker1),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        value: turn2User,
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildToolCallResponse(
                        businessCallID2,
                        businessTool,
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
                        value: businessCallID2,
                    },
                },
                context: {times: 1},
                response: {
                    status: 200,
                    headers: {'Content-Type': 'text/event-stream'},
                    body: buildTextResponse(finalMarker2),
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

        await mentionBotAndOpenThread(page, mmPage, 'toolbot', turn1User);

        const rhs = page.locator('#rhsContainer');
        await rhs.waitFor({state: 'visible', timeout: 10000});

        const botPosts = rhs.locator('[data-testid="llm-bot-post"]');
        const firstBotPost = botPosts.last();

        await expect(firstBotPost.getByText(searchToolsLabel, {exact: true})).toBeVisible({timeout: 45000});
        await expect(firstBotPost.getByText(loadToolLabel, {exact: true})).toBeVisible({timeout: 45000});
        await expect(rhs.getByText('Auto-approved').first()).toBeVisible({timeout: 30000});

        await expect(firstBotPost.getByText(businessToolLabel, {exact: true})).toBeVisible({timeout: 45000});
        const acceptButton1 = rhs.getByRole('button', {name: /^accept$/i});
        await expect(acceptButton1).toBeVisible({timeout: 30000});
        await acceptButton1.click();

        await expect(firstBotPost.getByText(finalMarker1)).toBeVisible({timeout: 45000});
        await expect(rhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});
        await expect(botPosts).toHaveCount(1);
        await expect(firstBotPost.getByText(loadToolLabel, {exact: true})).toHaveCount(1);

        const replyTextbox = rhs.locator('textarea').first();
        await replyTextbox.waitFor({state: 'visible', timeout: 10000});
        await replyTextbox.fill(turn2User);
        await rhs.getByTestId('SendMessageButton').click();

        await expect(botPosts).toHaveCount(2, {timeout: 45000});
        const secondBotPost = botPosts.last();

        await expect(secondBotPost.getByText(businessToolLabel, {exact: true})).toBeVisible({timeout: 45000});
        await expect(secondBotPost.getByText(searchToolsLabel, {exact: true})).toHaveCount(0);
        await expect(secondBotPost.getByText(loadToolLabel, {exact: true})).toHaveCount(0);

        // Canary hint from mcp/meta_tools.go must be absent when the deriver restored the tool.
        await expect(secondBotPost.getByText(/available but not loaded/i)).toHaveCount(0);

        const acceptButton2 = rhs.getByRole('button', {name: /^accept$/i});
        await expect(acceptButton2).toBeVisible({timeout: 30000});
        await acceptButton2.click();

        await expect(secondBotPost.getByText(finalMarker2)).toBeVisible({timeout: 45000});
        await expect(rhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

        await expect(rhs.getByText(loadToolLabel, {exact: true})).toHaveCount(1);
        await expect(rhs.getByText(searchToolsLabel, {exact: true})).toHaveCount(1);
    });
});
