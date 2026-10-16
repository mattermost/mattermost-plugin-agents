import { test, expect, type BrowserContext, type Page, type Locator } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { expandToolActivity } from 'helpers/llmbot-post';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildToolCallResponse,
    buildTextResponse,
    titleGenerationMockRule,
} from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';
import {
    MOCK_CONNECTED_TOOL_NAME,
    MOCK_OAUTH_AUTHORIZE_URL,
    MOCK_OAUTH_SERVER_NAME,
    MOCK_OAUTH_SERVER_URL,
    buildMCPOAuthConnectedMocks,
    buildMCPOAuthMocks,
} from 'helpers/mcp-oauth-mock';

/**
 * Test Suite: Agents ask users to connect MCP servers they have not connected
 *
 * The mock MCP server answers 401 until the user finishes OAuth. The agent must
 * still learn the server exists (the tool-call mock only matches requests that
 * describe it) and can ask for a connection, which renders a Connect
 * card instead of a refusal. The card offers only Connect and Skip; once the
 * user is connected, the conversation continues without another click.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const reason = 'I need it to look up your open tickets.';
const requestArgs = JSON.stringify({ server: MOCK_OAUTH_SERVER_NAME, reason });
const oauthStartPath = `/plugins/mattermost-ai/mcp/oauth/${encodeURIComponent(MOCK_OAUTH_SERVER_NAME)}/start`;

// The admin's description only reaches the model, in the connection request
// tool's schema, when the server is offered for connection, so matching on it
// also proves it is shown.
const adminDescription = 'Help desk tickets for the support team.';
const unconnectedPromptMatch = adminDescription;

async function openBotReply(page: Page, userMessage: string, beforeMessage?: () => Promise<void>): Promise<Locator> {
    const mmPage = new MattermostPage(page);
    await mmPage.login(mattermost.url(), adminUsername, adminPassword);

    // Every test starts unconnected, whatever an earlier test or retry did.
    const disconnect = await page.evaluate(async (serverName) => {
        const response = await fetch(`/plugins/mattermost-ai/mcp/oauth/${encodeURIComponent(serverName)}`, {
            method: 'DELETE',
            headers: { 'X-CSRF-Token': document.cookie.match(/MMCSRF=([^;]+)/)?.[1] ?? '' },
        });
        return response.status;
    }, MOCK_OAUTH_SERVER_NAME);
    expect(disconnect).toBe(200);
    await beforeMessage?.();

    await mmPage.createAndNavigateToDMWithBot(mattermost, adminUsername, adminPassword, 'toolbot');
    await mmPage.mentionBot('toolbot', userMessage);

    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(`@toolbot ${userMessage}`, { exact: true }),
    }).last();
    await expect(post.getByText(/\d+ repl/i)).toBeVisible({ timeout: 30000 });
    await post.getByText(/\d+ repl/i).click();

    return page.locator('#rhsContainer').locator('[data-testid="llm-bot-post"]').last();
}

// Connects the signed-in user to the mock server through the plugin's OAuth
// routes, acting as a provider that approves immediately.
async function connectMockServer(context: BrowserContext) {
    const start = await context.request.get(`${mattermost.url()}${oauthStartPath}`, { maxRedirects: 0 });
    expect(start.status()).toBe(302);
    const authorizeURL = new URL(start.headers().location);
    const callbackURL = new URL(authorizeURL.searchParams.get('redirect_uri') ?? '');
    callbackURL.searchParams.set('code', 'mock-authorization-code');
    callbackURL.searchParams.set('state', authorizeURL.searchParams.get('state') ?? '');
    const callback = await context.request.get(callbackURL.toString());
    expect(callback.status()).toBe(200);
}

async function expectButtons(card: Locator, labels: string[]) {
    await expect(card.getByRole('button')).toHaveText(labels, { timeout: 30000 });
}

// Clicks Connect and returns the sign-in window it opens, plus the provider
// authorization URL the plugin's OAuth start route redirected that window to.
// The provider lives on the Docker network, so that page never loads in the
// browser; tests act as the provider themselves.
async function clickConnect(context: BrowserContext, card: Locator): Promise<{ signInWindow: Page; authorizeURL: URL }> {
    const startRequest = context.waitForEvent('request', (request) => request.url().includes(oauthStartPath));
    const authorizeRequest = context.waitForEvent('request', (request) => request.url().startsWith(MOCK_OAUTH_AUTHORIZE_URL));
    const signInWindow = context.waitForEvent('page');
    await card.getByRole('button', { name: /^connect$/i }).click();

    expect(new URL((await startRequest).url()).origin).toBe(mattermost.url());
    return { signInWindow: await signInWindow, authorizeURL: new URL((await authorizeRequest).url()) };
}

test.describe('MCP connection requests (Mocked LLM)', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunSystemConsoleContainer({
            // mentionBot @-mentions the agent, which routes through the mention path.
            enableChannelMentionToolCalling: true,
            services: [
                {
                    id: 'mock-service',
                    name: 'Mock Service',
                    type: 'openaicompatible',
                    apiKey: 'mock',
                    apiURL: 'http://openai:8080',
                    defaultModel: 'gpt-mock',
                    useResponsesAPI: false,
                },
            ],
            bots: [
                {
                    id: 'tool-test-bot',
                    name: 'toolbot',
                    displayName: 'Tool Test Bot',
                    serviceID: 'mock-service',
                    customInstructions: '',
                    disableTools: false,
                    mcpDynamicToolLoading: false,
                    enabledNativeTools: [],
                },
            ],
            mcp: {
                enabled: true,
                enablePluginServer: true,
                embeddedServer: { enabled: true },
                idleTimeoutMinutes: 30,
                servers: [
                    {
                        name: MOCK_OAUTH_SERVER_NAME,
                        enabled: true,
                        baseURL: MOCK_OAUTH_SERVER_URL,
                        description: adminDescription,
                        clientID: 'test-client-id',
                        clientSecret: 'test-client-secret',
                    },
                ],
            },
        });
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('the card offers Connect and Skip, and skipping lets the agent continue without the server', async ({ page }) => {
        test.setTimeout(120000);

        const followUpText = 'Understood, continuing without the ticket tracker.';
        // addMocks resets Smocker, so the OAuth server mocks are registered with every turn.
        await openAIMock.addMocks([
            ...buildMCPOAuthMocks(),
            buildChatCompletionMockRule(
                buildToolCallResponse('call_connect_skip', 'RequestMCPConnection', requestArgs),
                { bodyContains: unconnectedPromptMatch },
            ),
            titleGenerationMockRule(),
            buildChatCompletionMockRule(buildTextResponse(followUpText), {
                bodyContains: `chose not to connect ${MOCK_OAUTH_SERVER_NAME}`,
            }),
        ]);

        const botPost = await openBotReply(page, 'Summarize my open tickets ' + Date.now());
        const card = botPost.getByTestId('connection-request-card');

        await expect(card.getByText(`Connect ${MOCK_OAUTH_SERVER_NAME}`, { exact: true })).toBeVisible({ timeout: 30000 });
        await expect(card.getByText(reason, { exact: true })).toBeVisible();
        await expect(card.getByText(/sign in to .* with your own account/i)).toBeVisible();
        await expectButtons(card, ['Skip', 'Connect']);

        await card.getByRole('button', { name: /^skip$/i }).click();

        await expect(page.locator('#rhsContainer').getByText(followUpText, { exact: false })).toBeVisible({ timeout: 30000 });
        const activityRounds = await expandToolActivity(botPost);
        await expect(activityRounds.getByText('Skipped', { exact: true })).toBeVisible({ timeout: 30000 });
    });

    test('closing the sign-in window before finishing keeps only Connect and Skip', async ({ page, context }) => {
        test.setTimeout(120000);

        const followUpText = 'No problem, I will continue without the ticket tracker.';
        await openAIMock.addMocks([
            ...buildMCPOAuthMocks(),
            buildChatCompletionMockRule(
                buildToolCallResponse('call_connect_abandon', 'RequestMCPConnection', requestArgs),
                { bodyContains: unconnectedPromptMatch },
            ),
            titleGenerationMockRule(),
            buildChatCompletionMockRule(buildTextResponse(followUpText), {
                bodyContains: `chose not to connect ${MOCK_OAUTH_SERVER_NAME}`,
            }),
        ]);

        const botPost = await openBotReply(page, 'Find tickets assigned to me ' + Date.now());
        const card = botPost.getByTestId('connection-request-card');
        await expectButtons(card, ['Skip', 'Connect']);

        const { signInWindow } = await clickConnect(context, card);
        await signInWindow.close();
        await page.bringToFront();

        await expect(card.getByText(/finish signing in in the new window/i)).toBeVisible({ timeout: 30000 });
        await expectButtons(card, ['Skip', 'Connect']);

        // Connect stays available to retry.
        const { signInWindow: retryWindow } = await clickConnect(context, card);
        await retryWindow.close();
        await page.bringToFront();
        await expectButtons(card, ['Skip', 'Connect']);

        await card.getByRole('button', { name: /^skip$/i }).click();

        await expect(page.locator('#rhsContainer').getByText(followUpText, { exact: false })).toBeVisible({ timeout: 30000 });
        const activityRounds = await expandToolActivity(botPost);
        await expect(activityRounds.getByText('Skipped', { exact: true })).toBeVisible({ timeout: 30000 });
    });

    test('finishing sign-in continues the conversation automatically', async ({ page, context }) => {
        test.setTimeout(120000);

        const followUpText = 'You are connected now, here are your open tickets.';
        await openAIMock.addMocks([
            ...buildMCPOAuthMocks(),
            ...buildMCPOAuthConnectedMocks(),
            buildChatCompletionMockRule(
                buildToolCallResponse('call_connect_finish', 'RequestMCPConnection', requestArgs),
                { bodyContains: unconnectedPromptMatch },
            ),
            titleGenerationMockRule(),
            // Only sent when the server sees the connected tools after the user signs in.
            buildChatCompletionMockRule(buildTextResponse(followUpText), {
                bodyContains: `The user connected ${MOCK_OAUTH_SERVER_NAME}.`,
            }),
        ]);

        const botPost = await openBotReply(page, 'What tickets do I have open? ' + Date.now());
        const card = botPost.getByTestId('connection-request-card');
        await expectButtons(card, ['Skip', 'Connect']);

        const { signInWindow, authorizeURL } = await clickConnect(context, card);
        await expect(card.getByText(/finish signing in in the new window/i)).toBeVisible({ timeout: 30000 });
        await expectButtons(card, ['Skip', 'Connect']);

        // Approve as the provider: send the user back to the plugin's callback
        // with their session, then close the window as the callback page does.
        const callbackURL = new URL(authorizeURL.searchParams.get('redirect_uri') ?? '');
        expect(callbackURL.origin).toBe(mattermost.url());
        callbackURL.searchParams.set('code', 'mock-authorization-code');
        callbackURL.searchParams.set('state', authorizeURL.searchParams.get('state') ?? '');
        const callback = await context.request.get(callbackURL.toString());
        expect(callback.status()).toBe(200);
        await signInWindow.close();

        await expect(page.locator('#rhsContainer').getByText(followUpText, { exact: false })).toBeVisible({ timeout: 30000 });
        const activityRounds = await expandToolActivity(botPost);
        await expect(activityRounds.getByText('Connected', { exact: true })).toBeVisible({ timeout: 30000 });
    });

    test('a connection whose authorization lapses mid-conversation is offered for reconnection', async ({ page, context }) => {
        test.setTimeout(120000);

        const userMessage = 'List my open tickets ' + Date.now();
        const toolName = `mock_oauth_server__${MOCK_CONNECTED_TOOL_NAME}`;
        const followUpText = 'Understood, I will continue without the ticket tracker for now.';

        // The user is connected and the agent asks to run one of the server's tools.
        await openAIMock.addMocks([
            ...buildMCPOAuthMocks(),
            ...buildMCPOAuthConnectedMocks(),
            buildChatCompletionMockRule(buildToolCallResponse('call_list_tickets', toolName, '{}'), { bodyContains: userMessage }),
            titleGenerationMockRule(),
        ]);
        const botPost = await openBotReply(page, userMessage, () => connectMockServer(context));
        const accept = botPost.getByRole('button', { name: /^accept$/i });
        await expect(accept).toBeVisible({ timeout: 30000 });

        // The grant lapses before the call runs: the server now rejects the token.
        await openAIMock.addMocks([
            ...buildMCPOAuthMocks(),
            buildChatCompletionMockRule(buildToolCallResponse('call_reconnect', 'RequestMCPConnection', requestArgs), {
                bodyContains: 'has expired or was revoked',
            }),
            titleGenerationMockRule(),
            buildChatCompletionMockRule(buildTextResponse(followUpText), {
                bodyContains: `chose not to connect ${MOCK_OAUTH_SERVER_NAME}`,
            }),
        ]);
        await accept.click();

        // Without another message from the user, the agent offers to reconnect.
        const card = page.locator('#rhsContainer').getByTestId('connection-request-card').last();
        await expect(card.getByText(`Connect ${MOCK_OAUTH_SERVER_NAME}`, { exact: true })).toBeVisible({ timeout: 30000 });
        await expectButtons(card, ['Skip', 'Connect']);

        await card.getByRole('button', { name: /^skip$/i }).click();
        await expect(page.locator('#rhsContainer').getByText(followUpText, { exact: false })).toBeVisible({ timeout: 30000 });
    });
});
