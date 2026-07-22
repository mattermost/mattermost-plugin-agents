import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';
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

test.describe('Agent Access Control', () => {
    test.beforeAll(async () => {
        mattermost = await RunAgentContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await openAIMock.addCompletionMock(responseTest);
    }, { timeout: 180000 });

    test.afterAll(async () => {
        await Promise.allSettled([
            openAIMock ? openAIMock.stop() : Promise.resolve(),
            mattermost ? mattermost.stop() : Promise.resolve(),
        ]);
    });

    test('should block user when UserAccessLevel=Block', async ({ page }) => {
        test.setTimeout(120000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        // Get the regularuser's ID for the blocklist
        const regularClient = await mattermost.getClient(agentRegularUsername, agentRegularPassword);
        const regularUser = await regularClient.getMe();

        // Create agent that blocks regularuser
        await agentApi.createTestAgent(token, {
            displayName: 'Blocking Agent',
            username: 'blockingagent',
            serviceID: mockServiceId,
            userAccessLevel: 2, // UserAccessLevelBlock
            userIDs: [regularUser.id],
        });

        const mmPage = new MattermostPage(page);
        const { client, channelId, botUserId } = await mmPage.getClientAndDmChannelForBot(
            mattermost, agentRegularUsername, agentRegularPassword, 'blockingagent',
        );

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(mattermost, agentRegularUsername, agentRegularPassword, 'blockingagent');

        const sinceMs = Date.now();
        await mmPage.sendChannelMessage('Hello agent');

        await mmPage.expectNoBotDmReplyFromApi(client, channelId, botUserId, sinceMs);
    });

    test('should allow user when UserAccessLevel=Allow and user is in allowlist', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        // Get the regularuser's ID for the allowlist
        const regularClient = await mattermost.getClient(agentRegularUsername, agentRegularPassword);
        const regularUser = await regularClient.getMe();

        // Create agent that only allows regularuser
        await agentApi.createTestAgent(token, {
            displayName: 'Restricted Agent',
            username: 'restrictedagent',
            serviceID: mockServiceId,
            userAccessLevel: 1, // UserAccessLevelAllow
            userIDs: [regularUser.id],
        });

        const mmPage = new MattermostPage(page);
        const { client, channelId, botUserId } = await mmPage.getClientAndDmChannelForBot(
            mattermost, agentRegularUsername, agentRegularPassword, 'restrictedagent',
        );

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(mattermost, agentRegularUsername, agentRegularPassword, 'restrictedagent');

        const sinceMs = Date.now();
        await mmPage.sendChannelMessage('Hello agent');
        await mmPage.expectBotDmReplyFromApi(client, channelId, botUserId, sinceMs);
    });

    test('creator should have access to their own agent via API', async () => {
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        // Create agent
        const agent = await agentApi.createTestAgent(token, {
            displayName: 'Admin Check Agent',
            username: 'admincheckagent',
            serviceID: mockServiceId,
        });

        // Verify via API that the agent is accessible
        const fetched = await agentApi.getAgent(token, agent.id);
        expect(fetched.displayName).toBe('Admin Check Agent');
        expect(fetched.creatorID).toBeTruthy();
    });

    test('should block user not on allowlist when UserAccessLevel=Allow', async ({ page }) => {
        test.setTimeout(120000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();
        const adminUser = await adminClient.getMe();

        await agentApi.createTestAgent(token, {
            displayName: 'Allowlist Only Admin',
            username: 'allowonlyadmin',
            serviceID: mockServiceId,
            userAccessLevel: 1,
            userIDs: [adminUser.id],
        });

        const mmPage = new MattermostPage(page);
        const { client, channelId, botUserId } = await mmPage.getClientAndDmChannelForBot(
            mattermost, agentRegularUsername, agentRegularPassword, 'allowonlyadmin',
        );

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await mmPage.createAndNavigateToDMWithBot(
            mattermost, agentRegularUsername, agentRegularPassword, 'allowonlyadmin',
        );

        const sinceMs = Date.now();
        await mmPage.sendChannelMessage('Hello agent');
        await mmPage.expectNoBotDmReplyFromApi(client, channelId, botUserId, sinceMs);
    });

    test('UserAccessLevel=None hides agent from non-creators in the agents list', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();

        await agentApi.createTestAgent(token, {
            displayName: 'Private To Creator Only',
            username: 'privatetocreator',
            serviceID: mockServiceId,
            userAccessLevel: 3,
        });

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await expect(agentPage.getAgentRowByName('Private To Creator Only')).not.toBeVisible({
            timeout: 5000,
        });

        await page.context().clearCookies();
        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await expect(agentPage.getAgentRowByName('Private To Creator Only')).toBeVisible({ timeout: 10000 });
    });

    test('delegated admin can edit an agent from the listing', async ({ page }) => {
        test.setTimeout(90000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const regularClient = await mattermost.getClient(agentRegularUsername, agentRegularPassword);
        const token = adminClient.getToken();
        const regularUser = await regularClient.getMe();

        const created = await agentApi.createTestAgent(token, {
            displayName: 'Delegate Me',
            username: 'delegatemeagent',
            serviceID: mockServiceId,
        });

        await agentApi.updateAgent(token, created.id, {
            adminUserIDs: [regularUser.id],
        });

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await agentPage.navigateToAgents(mattermost.url());

        await agentPage.openAgentActions('Delegate Me');
        await agentPage.clickEditAction('Delegate Me');
        await agentPage.waitForModal();

        await agentPage.getDisplayNameInput().clear();
        await agentPage.getDisplayNameInput().fill('Delegated By Editor');

        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        await expect(agentPage.getAgentRowByName('Delegated By Editor')).toBeVisible({ timeout: 10000 });
    });
});
