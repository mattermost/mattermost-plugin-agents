import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, buildTextResponse } from 'helpers/openai-mock';
import {
    RunAgentContainer,
    agentAdminUsername, agentAdminPassword,
    agentRegularUsername, agentRegularPassword,
    mockServiceId,
} from 'helpers/agent-container';
import { AgentAPIHelper } from 'helpers/agent-api';
import { AgentPageHelper } from 'helpers/agent-page';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Agent MCP Tools', () => {
    test.beforeAll(async () => {
        mattermost = await RunAgentContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('agent with no enabled_tools gets no MCP tools', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        // Create agent with empty enabled_tools (= no MCP tools allowed)
        await agentApi.createTestAgent(token, {
            display_name: 'No Tools Agent',
            username: 'notoolsagent',
            service_id: mockServiceId,
            enabled_tools: [],
        });

        // Mock LLM with a simple text response
        await openAIMock.addCompletionMock(buildTextResponse('I have no tools available.'));

        // Login and DM the agent
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost, agentRegularUsername, agentRegularPassword, 'notoolsagent'
        );

        await mmPage.sendChannelMessage('Hello');
        await mmPage.waitForReply();

        // The response should come through without tool calls
        await expect(page.locator('text=I have no tools available')).toBeVisible({ timeout: 15000 });
    });

    test('MCPs tab shows tool toggles in config modal', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        // Create a new agent
        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        // Navigate to MCPs tab
        await agentPage.getModalTab('MCPs').click();

        // Verify the MCPs tab content is visible (search input or tool toggles)
        await expect(
            agentPage.getMCPSearchInput()
                .or(agentPage.getToolToggles().first())
        ).toBeVisible({ timeout: 10000 });
    });

    test('agent with specific enabled_tools responds correctly', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        // Create agent with a specific tool enabled
        await agentApi.createTestAgent(token, {
            display_name: 'Selective Tools Agent',
            username: 'selectivetoolsagent',
            service_id: mockServiceId,
            enabled_tools: [
                { server_origin: 'mattermost://embedded', tool_name: 'GetChannelPosts' },
            ],
        });

        // Mock LLM with a text response
        await openAIMock.addCompletionMock(buildTextResponse('I used the selected tool.'));

        // Login and DM the agent
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost, agentRegularUsername, agentRegularPassword, 'selectivetoolsagent'
        );

        await mmPage.sendChannelMessage('Get the channel posts');
        await mmPage.waitForReply();

        // Verify the agent responded
        await expect(page.locator('text=I used the selected tool')).toBeVisible({ timeout: 15000 });
    });
});
