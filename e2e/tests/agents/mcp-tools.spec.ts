import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildTextResponse,
    buildChatCompletionMockRule,
} from 'helpers/openai-mock';
import {
    RunAgentContainer,
    agentAdminUsername, agentAdminPassword,
    agentRegularUsername, agentRegularPassword,
    mockServiceId,
} from 'helpers/agent-container';
import { AgentAPIHelper } from 'helpers/agent-api';
import { AgentPageHelper } from 'helpers/agent-page';
import { AIPlugin } from 'helpers/ai-plugin';

/** Matches mcp.EmbeddedClientKey — MCP server origin for the embedded Mattermost tools server. */
const embeddedMattermostOrigin = 'embedded://mattermost';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Agent MCP Tools', () => {
    test.beforeAll(async () => {
        mattermost = await RunAgentContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    }, { timeout: 180000 });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('agent with no enabled_tools gets no MCP tools', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const noToolsAgent = await agentApi.createTestAgent(token, {
            display_name: 'No Tools Agent',
            username: 'notoolsagent',
            service_id: mockServiceId,
            enabled_tools: [],
        });

        // Prove enabled_tools=[] reaches the LLM without read_post in the completion payload: first rule
        // would match if "read_post" were sent; second rule is the catch-all success response.
        await openAIMock.addMocks([
            buildChatCompletionMockRule(
                buildTextResponse('WRONG: read_post in completion request when enabled_tools is empty'),
                { bodyContains: 'read_post' },
            ),
            buildChatCompletionMockRule(buildTextResponse('I have no tools available.')),
        ]);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 60000 });

        await aiPlugin.openRHS();
        await aiPlugin.switchBotWhenListed(noToolsAgent.display_name);
        await aiPlugin.sendMessage('Hello');
        await aiPlugin.waitForBotResponse('I have no tools available.');
    });

    test('MCPs tab shows embedded server and tool affordances in config modal', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        await agentPage.getModalTab('MCPs').click();

        await expect(agentPage.getMCPSearchInput()).toBeVisible({ timeout: 15000 });
        // Provider name from GET /mcp/tools (mcp.EmbeddedServerName)
        await expect(agentPage.getModal().getByText('Mattermost')).toBeVisible({ timeout: 15000 });
    });

    test('agent with specific enabled_tools responds correctly', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const selectiveAgent = await agentApi.createTestAgent(token, {
            display_name: 'Selective Tools Agent',
            username: 'selectivetoolsagent',
            service_id: mockServiceId,
            enabled_tools: [
                { server_origin: embeddedMattermostOrigin, tool_name: 'read_post' },
            ],
        });

        // Prove read_post appears in at least one completion payload (tool-enabled round). Follow-up
        // completions may omit tools from the body, so the catch-all returns the same success text.
        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse('I used the selected tool.'), {
                bodyContains: 'read_post',
            }),
            buildChatCompletionMockRule(buildTextResponse('I used the selected tool.')),
        ]);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 60000 });

        await aiPlugin.openRHS();
        await aiPlugin.switchBotWhenListed(selectiveAgent.display_name);
        await aiPlugin.sendMessage('Summarize this channel');
        await aiPlugin.waitForBotResponse('I used the selected tool.');
    });

    // RHS Tool Providers popover filters by server (provider) using activeBot.enabledMCPTools origins;
    // individual tools are not listed here — only MCPs tab / server policy cover tool-level affordances.
    test('RHS Tools popover shows no providers when agent has empty enabled_tools', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const agent = await agentApi.createTestAgent(token, {
            display_name: 'RHS Empty MCP Agent',
            service_id: mockServiceId,
            enabled_tools: [],
        });

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost, agentRegularUsername, agentRegularPassword, agent.username,
        );

        await aiPlugin.openRHS();
        await expect(page.getByTestId('bot-selector-rhs')).toBeVisible({ timeout: 15000 });
        await aiPlugin.switchBotWhenListed(agent.display_name);

        const menu = await aiPlugin.openRhsToolProvidersMenu();
        await expect(menu.getByText('Tool Providers', { exact: true })).toBeVisible();
        await expect(menu.getByText('No tool providers available')).toBeVisible({ timeout: 20000 });
    });

    test('RHS Tools popover shows Mattermost provider when only embedded tools are enabled', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        const agent = await agentApi.createTestAgent(token, {
            display_name: 'RHS Embedded Only Agent',
            service_id: mockServiceId,
            enabled_tools: [
                { server_origin: embeddedMattermostOrigin, tool_name: 'read_post' },
            ],
        });

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost, agentRegularUsername, agentRegularPassword, agent.username,
        );

        await aiPlugin.openRHS();
        await expect(page.getByTestId('bot-selector-rhs')).toBeVisible({ timeout: 15000 });
        await aiPlugin.switchBotWhenListed(agent.display_name);

        const menu = await aiPlugin.openRhsToolProvidersMenu();
        await expect(menu.getByText('Tool Providers', { exact: true })).toBeVisible();
        await expect(menu.getByText('Mattermost')).toBeVisible({ timeout: 20000 });
        await expect(menu.getByText('No tool providers available')).not.toBeVisible();
    });

    test('RHS Tools popover lists Mattermost for default Mock Bot (no per-agent MCP restriction)', async ({
        page,
    }) => {
        test.setTimeout(90000);
        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 60000 });

        await aiPlugin.openRHS();
        await expect(page.getByTestId('bot-selector-rhs')).toBeVisible({ timeout: 15000 });
        await aiPlugin.switchBotWhenListed('Mock Bot');

        const menu = await aiPlugin.openRhsToolProvidersMenu();
        await expect(menu.getByText('Mattermost')).toBeVisible({ timeout: 20000 });
    });
});
