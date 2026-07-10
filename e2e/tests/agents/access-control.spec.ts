// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test, expect} from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText} from 'helpers/openai-mock';
import {
    RunAgentContainer,
    agentAdminUsername, agentAdminPassword,
    agentRegularUsername, agentRegularPassword,
    agentUnprivilegedUsername, agentUnprivilegedPassword,
    mockServiceId,
} from 'helpers/agent-container';
import {AgentAPIHelper, ChannelAccessLevel, UserAccessLevel} from 'helpers/agent-api';
import {AgentPageHelper} from 'helpers/agent-page';
import {AIPlugin} from 'helpers/ai-plugin';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

function buildResponsesAPIStream(message: string): string {
    const responseId = 'resp_agent_access';
    const itemId = 'msg_agent_access';
    const output = [{
        id: itemId,
        type: 'message',
        status: 'completed',
        role: 'assistant',
        content: [{
            type: 'output_text',
            text: message,
            annotations: [],
        }],
    }];

    return [
        `data: ${JSON.stringify({
            type: 'response.output_text.delta',
            sequence_number: 0,
            output_index: 0,
            content_index: 0,
            item_id: itemId,
            delta: message,
        })}`,
        `data: ${JSON.stringify({
            type: 'response.completed',
            sequence_number: 1,
            response: {
                id: responseId,
                object: 'response',
                created_at: 1_708_124_577,
                completed_at: 1_708_124_577,
                status: 'completed',
                model: 'gpt-access-persistence',
                output,
                tools: [],
                usage: {
                    input_tokens: 1,
                    output_tokens: 8,
                    total_tokens: 9,
                },
            },
        })}`,
    ].join('\n\n') + '\n\n';
}

test.describe('Agent Access Control', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunAgentContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await openAIMock.addCompletionMock(responseTest);
    });

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

    test('persists Access tab settings and keeps a DM-only agent out of channel actions', async ({page}) => {
        test.setTimeout(180000);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const regularClient = await mattermost.getClient(agentRegularUsername, agentRegularPassword);
        const delegatedAdminClient = await mattermost.getClient(agentUnprivilegedUsername, agentUnprivilegedPassword);
        const token = adminClient.getToken();
        const regularUser = await regularClient.getMe();
        const delegatedAdmin = await delegatedAdminClient.getMe();
        const teams = await adminClient.getMyTeams();
        const defaultTeam = teams.find((team) => team.name === 'test');
        if (!defaultTeam) {
            throw new Error('test team not found');
        }

        const suffix = Date.now().toString(36);
        const displayName = `DM Only Access ${suffix}`;
        const username = `dmonlyaccess${suffix}`;
        const enabledMCPTools = [
            {server_origin: 'embedded://mattermost', tool_name: 'read_post'},
        ];
        const enabledNativeTools = ['web_search'];
        const created = await agentApi.createTestAgent(token, {
            displayName,
            username,
            serviceID: mockServiceId,
            customInstructions: 'Keep this full-document field while access settings change.',
            channelAccessLevel: ChannelAccessLevel.All,
            channelIDs: [],
            userAccessLevel: UserAccessLevel.All,
            userIDs: [],
            teamIDs: [],
            adminUserIDs: [],
            enabledMCPTools,
            autoEnableNewMCPTools: false,
            mcpDynamicToolLoading: true,
            enabledNativeTools,
            model: 'gpt-access-persistence',
            enableVision: false,
            disableTools: true,
            reasoningEnabled: false,
            reasoningEffort: 'low',
            thinkingBudget: 321,
            structuredOutputEnabled: false,
            maxToolTurns: 30,
        });

        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await agentPage.openAgentActions(displayName);
        await agentPage.clickEditAction(displayName);
        await agentPage.waitForModal();
        await agentPage.getModalTab('Access').click();

        await expect(agentPage.getChannelAccessRadios()).toHaveCount(4);
        await agentPage.getChannelAccessRadio(ChannelAccessLevel.None).check();
        await expect(agentPage.getChannelAccessRadio(ChannelAccessLevel.None)).toBeChecked();
        await expect(agentPage.getChannelAccessSelect()).toHaveCount(0);

        await expect(agentPage.getUserAccessRadios()).toHaveCount(3);
        await agentPage.getUserAccessRadio(UserAccessLevel.Allow).check();
        await expect(agentPage.getUserAccessRadio(UserAccessLevel.Allow)).toBeChecked();

        const userAccessSelect = agentPage.getUserAccessSelect();
        await userAccessSelect.fill(agentRegularUsername);
        const regularUserOption = page.getByRole('option', {name: agentRegularUsername, exact: true});
        await expect(regularUserOption).toBeVisible({timeout: 10000});
        await regularUserOption.click();

        await userAccessSelect.fill(defaultTeam.display_name);
        const teamOption = page.getByRole('option').
            filter({hasText: defaultTeam.display_name}).
            filter({has: page.getByText('TEAM', {exact: true})});
        await expect(teamOption).toHaveCount(1);
        await teamOption.click();

        const adminsSelect = agentPage.getAgentAdminsSelect();
        await adminsSelect.fill(agentUnprivilegedUsername);
        const delegatedAdminOption = page.getByRole('option', {name: agentUnprivilegedUsername, exact: true});
        await expect(delegatedAdminOption).toBeVisible({timeout: 10000});
        await delegatedAdminOption.click();

        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();
        await expect(agentPage.getAgentRowByName(displayName)).toBeVisible({timeout: 10000});

        const saved = await agentApi.getAgent(token, created.id);
        expect(saved).toEqual({
            ...created,
            channelAccessLevel: ChannelAccessLevel.None,
            channelIDs: null,
            userAccessLevel: UserAccessLevel.Allow,
            userIDs: [regularUser.id],
            teamIDs: [defaultTeam.id],
            adminUserIDs: [delegatedAdmin.id],
            enabledMCPTools,
            autoEnableNewMCPTools: false,
            mcpDynamicToolLoading: true,
            enabledNativeTools,
            updateAt: expect.any(Number),
        });
        expect(saved.updateAt).toBeGreaterThan(created.updateAt ?? 0);

        await agentPage.openAgentActions(displayName);
        await agentPage.clickEditAction(displayName);
        await agentPage.waitForModal();
        await agentPage.getModalTab('Access').click();
        await expect(agentPage.getChannelAccessRadio(ChannelAccessLevel.None)).toBeChecked();
        await expect(agentPage.getChannelAccessSelect()).toHaveCount(0);
        await expect(agentPage.getUserAccessRadio(UserAccessLevel.Allow)).toBeChecked();
        await expect(agentPage.getUserAccessSection().getByText(agentRegularUsername, {exact: true})).toBeVisible();
        await expect(agentPage.getUserAccessSection().getByText(defaultTeam.display_name, {exact: true})).toBeVisible();
        await expect(agentPage.getAgentAdminsSection().getByText(agentUnprivilegedUsername, {exact: true})).toBeVisible();
        await agentPage.getBackButton().click();
        await agentPage.waitForModalClosed();

        await page.context().clearCookies();
        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);

        const aiPlugin = new AIPlugin(page);
        await expect(aiPlugin.appBarIcon).toBeVisible({timeout: 30000});
        await aiPlugin.appBarIcon.click();
        const rhs = aiPlugin.getRhsContainer();
        await expect(rhs).toBeVisible({timeout: 10000});
        await aiPlugin.ensureRhsNewChatTab();
        const rhsAgentSelector = rhs.getByTestId('bot-selector-rhs');
        await expect(rhsAgentSelector).toBeVisible({timeout: 10000});
        await rhsAgentSelector.click();
        const unscopedAgentMenu = page.getByTestId('dropdownmenu').filter({hasText: 'Choose an Agent'});
        await expect(unscopedAgentMenu.getByText('Choose an Agent', {exact: true})).toBeVisible();
        await expect(unscopedAgentMenu.getByRole('button', {name: displayName, exact: true})).toBeVisible();
        await page.keyboard.press('Escape');
        await expect(unscopedAgentMenu).not.toBeVisible();

        const channels = await regularClient.getMyChannels(defaultTeam.id);
        const townSquare = channels.find((channel) => channel.name === 'town-square');
        if (!townSquare) {
            throw new Error('town-square channel not found');
        }
        const channelPost = await regularClient.createPost({
            channel_id: townSquare.id,
            message: `Channel access selector check ${suffix}`,
        });
        await page.goto(`${mattermost.url()}/${defaultTeam.name}/channels/${townSquare.name}`);
        const channelPostLocator = page.locator(`#post_${channelPost.id}`);
        await expect(channelPostLocator).toBeVisible({timeout: 30000});
        await channelPostLocator.hover();

        const aiActionsButton = channelPostLocator.getByTestId('ai-actions-menu');
        await expect(aiActionsButton).toBeVisible();
        await aiActionsButton.click();
        const actionsMenu = page.getByTestId('dropdownmenu').filter({hasText: 'Summarize Thread'});
        await expect(actionsMenu.getByRole('button', {name: 'Summarize Thread'})).toBeVisible();
        const channelAgentSelector = actionsMenu.getByTitle('Mock Bot', {exact: true});
        await expect(channelAgentSelector).toBeVisible();
        await channelAgentSelector.click();

        const channelAgentMenu = page.getByTestId('dropdownmenu').filter({hasText: 'Choose an Agent'});
        await expect(channelAgentMenu.getByText('Choose an Agent', {exact: true})).toBeVisible();
        await expect(channelAgentMenu.getByRole('button', {name: 'Mock Bot', exact: true})).toBeVisible();
        await expect(channelAgentMenu.getByRole('button', {name: displayName, exact: true})).toHaveCount(0);

        await page.keyboard.press('Escape');
        await page.keyboard.press('Escape');
        const {client, channelId, botUserId} = await mmPage.getClientAndDmChannelForBot(
            mattermost,
            agentRegularUsername,
            agentRegularPassword,
            username,
        );
        await page.goto(`${mattermost.url()}/${defaultTeam.name}/messages/@${username}`);
        await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 30000});
        await expect(mmPage.postTextbox).toBeVisible({timeout: 30000});

        await openAIMock.addMock({
            request: {
                method: 'POST',
                path: '/v1/responses',
            },
            context: {
                times: 100,
            },
            response: {
                status: 200,
                headers: {
                    'Content-Type': 'text/event-stream',
                },
                body: buildResponsesAPIStream(responseTestText),
            },
        });

        const sinceMs = Date.now();
        await mmPage.sendChannelMessage('Prove DM-only access works');
        await mmPage.expectBotDmReplyFromApi(
            client,
            channelId,
            botUserId,
            sinceMs,
            {expectedMessage: responseTestText},
        );
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
