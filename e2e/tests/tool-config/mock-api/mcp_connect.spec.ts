// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { test, expect, type Browser, type Locator, type Page } from '@playwright/test';

import { AgentAPIHelper } from 'helpers/agent-api';
import { expandToolActivity } from 'helpers/llmbot-post';
import {
    buildMCPOAuthMocks,
    MOCK_OAUTH_SERVER_NAME,
    MOCK_OAUTH_SERVER_URL,
} from 'helpers/mcp-oauth-mock';
import { MattermostPage } from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    TITLE_GENERATION_BODY_MATCH,
    buildChatCompletionMockRule,
    buildTextResponse,
    buildToolCallResponse,
    titleGenerationMockRule,
} from 'helpers/openai-mock';
import { AIMOCK_BOT_NAME, RunAIMockContainer } from 'helpers/plugincontainer';

/**
 * Connecting an unconnected (OAuth-needed) MCP server from a conversation.
 *
 * The mock MCP server always answers 401 with OAuth metadata, so for every
 * user it stays unconnected: the agent is offered ConnectMCPServer, and the
 * model's call renders as a Connect card the requester can act on.
 */

const SERVER_DESCRIPTION = 'Ticket tracker for the e2e team';
const CONNECT_TOOL = 'ConnectMCPServer';

const requester = { username: 'regularuser', password: 'regularuser' };
const onlooker = { username: 'seconduser', password: 'seconduser' };

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

/**
 * Mocks for one turn: the OAuth-protected MCP server, a ConnectMCPServer call
 * as the model's first answer, and a text reply once the call's result (which
 * carries its unique ID) is sent back.
 */
async function registerConnectTurn(callID: string, followUpText: string): Promise<void> {
    await openAIMock.addMocks([
        ...buildMCPOAuthMocks(),
        buildChatCompletionMockRule(
            buildToolCallResponse(callID, CONNECT_TOOL, JSON.stringify({ server: MOCK_OAUTH_SERVER_NAME })),
        ),
        titleGenerationMockRule(),
        buildChatCompletionMockRule(buildTextResponse(followUpText), { bodyContains: callID }),
    ]);
}

/** The first chat completion request of the turn started by `userMessage`. */
async function firstTurnRequest(userMessage: string): Promise<any> {
    const requests = await openAIMock.getChatCompletionRequests();
    const request = requests.find((body) => {
        const raw = JSON.stringify(body);
        return raw.includes(userMessage) && !raw.includes(TITLE_GENERATION_BODY_MATCH);
    });
    expect(request, `no chat completion request for "${userMessage}"`).toBeDefined();
    return request;
}

function offeredTool(request: any, name: string): any {
    return (request.tools ?? []).find((tool: any) => tool?.function?.name === name);
}

/**
 * The connect tool lists servers only in its model-facing schema; the
 * top-level description is copied onto persisted tool calls.
 */
function expectServerListedOnlyInSchema(connectTool: any, ...needles: string[]): void {
    const serverParam = connectTool.function.parameters?.properties?.server;
    for (const needle of needles) {
        expect(serverParam?.description).toContain(needle);
        expect(connectTool.function.description).not.toContain(needle);
    }
}

function systemPrompt(request: any): string {
    return (request.messages ?? []).
        filter((message: any) => message.role === 'system').
        map((message: any) => JSON.stringify(message.content)).
        join('\n');
}

async function login(browser: Browser, user: { username: string; password: string }): Promise<{ page: Page; mm: MattermostPage; close: () => Promise<void> }> {
    const context = await browser.newContext();
    const page = await context.newPage();
    const mm = new MattermostPage(page);
    await mm.login(mattermost.url(), user.username, user.password);
    return { page, mm, close: () => context.close() };
}

/** RunAIMockContainer only dismisses onboarding for regularuser and the admin. */
async function dismissOnboarding(user: { username: string; password: string }): Promise<void> {
    const client = await mattermost.getClient(user.username, user.password);
    const me = await client.getMe();
    await client.savePreferences(me.id, [
        { user_id: me.id, category: 'tutorial_step', name: me.id, value: '999' },
        { user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
        { user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
        { user_id: me.id, category: 'crt_thread_pane_step', name: me.id, value: '999' },
    ]);
}

async function openThreadOf(page: Page, message: string): Promise<Locator> {
    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, { exact: true }),
    }).last();
    await expect(post).toBeVisible({ timeout: 30000 });
    const replies = post.getByText(/\d+ repl/i);
    await expect(replies).toBeVisible({ timeout: 60000 });
    await replies.click();
    const rhs = page.locator('#rhsContainer');
    await expect(rhs.locator('[data-testid="llm-bot-post"]').first()).toBeVisible({ timeout: 30000 });
    return rhs;
}

async function expectInteractiveConnectCard(botPost: Locator): Promise<Locator> {
    const card = botPost.getByTestId('connect-mcp-card');
    await expect(card).toBeVisible({ timeout: 30000 });
    await expect(card.getByTestId('connect-mcp-server-name')).toHaveText(MOCK_OAUTH_SERVER_NAME);
    await expect(card.getByTestId('connect-mcp-skip-button')).toBeVisible();
    // Connect is enabled once the card has loaded the server's auth URL.
    await expect(card.getByTestId('connect-mcp-connect-button')).toBeEnabled({ timeout: 30000 });
    return card;
}

/** A resolved connect call folds into the post's activity area. */
async function expectResolvedState(botPost: Locator, testID: string): Promise<void> {
    const rounds = await expandToolActivity(botPost);
    await expect(rounds.getByTestId(testID)).toBeVisible({ timeout: 30000 });
}

test.describe('Connect MCP server from a conversation (Mocked LLM)', () => {
    test.describe.configure({ mode: 'serial' });

    test.beforeAll(async () => {
        test.setTimeout(240000);
        mattermost = await RunAIMockContainer({
            enableChannelMentionToolCalling: true,
            bot: { mcpDynamicToolLoading: false },
            mcp: {
                enabled: true,
                enablePluginServer: false,
                embeddedServer: { enabled: true },
                idleTimeoutMinutes: 30,
                servers: [
                    {
                        name: MOCK_OAUTH_SERVER_NAME,
                        enabled: true,
                        baseURL: MOCK_OAUTH_SERVER_URL,
                        description: SERVER_DESCRIPTION,
                        clientID: 'test-client-id',
                        clientSecret: 'test-client-secret',
                    },
                ],
            },
        });
        await dismissOnboarding(onlooker);
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await openAIMock.addMocks(buildMCPOAuthMocks());
    });

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('DM: offers ConnectMCPServer, renders the Connect card and continues after Skip', async ({ browser }) => {
        test.setTimeout(180000);

        const userMessage = `open a ticket in the tracker ${Date.now()}`;
        const callID = `call_connect_dm_${Date.now()}`;
        const followUpText = `CONNECT_DM_SKIPPED_${Date.now()}`;
        await registerConnectTurn(callID, followUpText);

        const user = await login(browser, requester);
        try {
            await user.mm.createAndNavigateToDMWithBot(mattermost, requester.username, requester.password, AIMOCK_BOT_NAME);
            await user.mm.sendChannelMessage(userMessage);
            const rhs = await openThreadOf(user.page, userMessage);
            const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();

            const card = await expectInteractiveConnectCard(botPost);

            const request = await firstTurnRequest(userMessage);
            const connectTool = offeredTool(request, CONNECT_TOOL);
            expect(connectTool, 'ConnectMCPServer offered to the model').toBeDefined();
            expectServerListedOnlyInSchema(connectTool, MOCK_OAUTH_SERVER_NAME, SERVER_DESCRIPTION);
            // The per-turn tool schema is the only list of unconnected servers.
            expect(systemPrompt(request)).not.toContain(SERVER_DESCRIPTION);

            await card.getByTestId('connect-mcp-skip-button').click();

            await expect(botPost.getByText(followUpText)).toBeVisible({ timeout: 60000 });
            await expectResolvedState(botPost, 'connect-mcp-skipped');
            await expect(botPost.getByTestId('connect-mcp-connect-button')).toHaveCount(0);
        } finally {
            await user.close();
        }
    });

    test('channel mention: only the requester can act on the Connect card', async ({ browser }) => {
        test.setTimeout(240000);

        const userMessage = `connect check in channel ${Date.now()}`;
        const callID = `call_connect_channel_${Date.now()}`;
        const followUpText = `CONNECT_CHANNEL_SKIPPED_${Date.now()}`;
        await registerConnectTurn(callID, followUpText);

        const owner = await login(browser, requester);
        const other = await login(browser, onlooker);
        try {
            const channelURL = `${mattermost.url()}/test/channels/off-topic`;
            await owner.page.goto(channelURL);
            await other.page.goto(channelURL);

            await owner.mm.mentionBot(AIMOCK_BOT_NAME, userMessage);
            const ownerRhs = await openThreadOf(owner.page, `@${AIMOCK_BOT_NAME} ${userMessage}`);
            const ownerPost = ownerRhs.locator('[data-testid="llm-bot-post"]').last();
            const card = await expectInteractiveConnectCard(ownerPost);

            const otherRhs = await openThreadOf(other.page, `@${AIMOCK_BOT_NAME} ${userMessage}`);
            const otherPost = otherRhs.locator('[data-testid="llm-bot-post"]').last();
            // Onlookers owe no decision, so the pending call folds into the
            // activity area like any other tool call they cannot act on.
            const otherRounds = await expandToolActivity(otherPost);
            await expect(otherRounds.getByTestId('connect-mcp-waiting')).toBeVisible({ timeout: 30000 });
            await expect(otherRhs.getByTestId('connect-mcp-connect-button')).toHaveCount(0);
            await expect(otherRhs.getByTestId('connect-mcp-skip-button')).toHaveCount(0);
            await expect(otherRhs.getByTestId('connect-mcp-continue-button')).toHaveCount(0);

            await card.getByTestId('connect-mcp-skip-button').click();
            await expect(ownerPost.getByText(followUpText)).toBeVisible({ timeout: 60000 });
            await expect(otherPost.getByText(followUpText)).toBeVisible({ timeout: 60000 });
            await expectResolvedState(otherPost, 'connect-mcp-skipped');
        } finally {
            await owner.close();
            await other.close();
        }
    });

    test('dynamic tool loading: ConnectMCPServer is still offered directly', async ({ browser }) => {
        test.setTimeout(180000);

        const adminToken = (await mattermost.getAdminClient()).getToken();
        const agents = new AgentAPIHelper(mattermost.url());
        const agent = (await agents.getAgents(adminToken)).find((a) => a.name === AIMOCK_BOT_NAME);
        expect(agent).toBeDefined();
        await agents.updateAgent(adminToken, agent!.id, { mcpDynamicToolLoading: true } as any);

        const userMessage = `dynamic connect check ${Date.now()}`;
        const callID = `call_connect_dynamic_${Date.now()}`;
        const followUpText = `CONNECT_DYNAMIC_SKIPPED_${Date.now()}`;
        await registerConnectTurn(callID, followUpText);

        const user = await login(browser, requester);
        try {
            await user.mm.createAndNavigateToDMWithBot(mattermost, requester.username, requester.password, AIMOCK_BOT_NAME);
            await user.mm.sendChannelMessage(userMessage);
            const rhs = await openThreadOf(user.page, userMessage);
            const botPost = rhs.locator('[data-testid="llm-bot-post"]').last();
            const card = await expectInteractiveConnectCard(botPost);

            const request = await firstTurnRequest(userMessage);
            expect(offeredTool(request, 'search_tools'), 'dynamic mode is active').toBeDefined();
            const connectTool = offeredTool(request, CONNECT_TOOL);
            expect(connectTool, 'ConnectMCPServer offered to the model').toBeDefined();
            expectServerListedOnlyInSchema(connectTool, MOCK_OAUTH_SERVER_NAME);

            await card.getByTestId('connect-mcp-skip-button').click();
            await expect(botPost.getByText(followUpText)).toBeVisible({ timeout: 60000 });
        } finally {
            await user.close();
        }
    });
});
