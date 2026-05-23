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
 * Dynamic MCP approval: search_tools -> load_tool -> ask-policy business tool.
 * Legacy tool-call-policies.spec.ts keeps mcpDynamicToolLoading=false.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const embeddedGetChannelInfoTool = 'mattermost__get_channel_info';
const embeddedGetChannelInfoLabel = 'Get Channel Info';
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

test.describe('Dynamic MCP Tool Approval (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithDynamicPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('ask policy applies after search_tools and load_tool materialize the business tool', async ({ page }) => {
        test.setTimeout(120000);

        const userMessage = `dynamic mcp approval ${Date.now()}`;
        const searchCallID = `call_dynamic_search_${Date.now()}`;
        const loadCallID = `call_dynamic_load_${Date.now()}`;
        const businessCallID = `call_dynamic_business_${Date.now()}`;
        const continuationMarker = `DYNAMIC_MCP_APPROVAL_CONTINUATION_${Date.now()}`;

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
                    body: buildTextResponse('Dynamic MCP channel lookup'),
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                    body: {
                        matcher: 'ShouldContainSubstring',
                        // Main turn includes the bot system prompt; title generation is user-only.
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
                        `{"name":"${embeddedGetChannelInfoTool}"}`,
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
                        businessCallID,
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
                        value: businessCallID,
                    },
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
        const initialBotPost = botPosts.last();

        // Meta-tools auto-run during the dynamic prelude before the ask-policy business tool.
        await expect(initialBotPost.getByText(searchToolsLabel, {exact: true})).toBeVisible({timeout: 45000});
        await expect(initialBotPost.getByText(loadToolLabel, {exact: true})).toBeVisible({timeout: 45000});
        await expect(rhs.getByText('Auto-approved').first()).toBeVisible({timeout: 30000});

        await expect(initialBotPost.getByText(embeddedGetChannelInfoLabel, {exact: true})).toBeVisible({timeout: 45000});

        // Pending-tool responses must not be masked by the empty-result fallback.
        await expect(initialBotPost.getByText(/did not return a result/i)).not.toBeVisible();
        await expect(initialBotPost.getByText(continuationMarker)).not.toBeVisible();

        const acceptButton = rhs.getByRole('button', {name: /^accept$/i});
        await expect(acceptButton).toBeVisible({timeout: 30000});
        await acceptButton.click();

        await expect(initialBotPost.getByText(continuationMarker)).toBeVisible({timeout: 45000});
        await expect(initialBotPost.getByText(embeddedGetChannelInfoLabel, {exact: true})).toBeVisible();
        await expect(initialBotPost.getByText(/did not return a result/i)).not.toBeVisible();
        await expect(botPosts).toHaveCount(1);
        await expect(rhs.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
        await expect(rhs.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});
    });
});
