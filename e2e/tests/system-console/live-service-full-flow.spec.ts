// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {test, expect, Locator, Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {buildTextResponse, buildTitleFixture, mergeFixtureFiles, stubAimockModelFetch} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {SystemConsoleHelper} from 'helpers/system-console';
import {AgentPageHelper} from 'helpers/agent-page';
import {AgentAPIHelper} from 'helpers/agent-api';
import RunSystemConsoleContainer, {adminUsername, adminPassword} from 'helpers/system-console-container';

const regularUsername = 'regularuser';
const regularPassword = 'regularuser';

const serviceName = 'Aimock Live Service';
const botDisplayName = 'Aimock Live Agent';
const botUsername = 'aimocklive';
const aimockModel = 'gpt-mock';

type Post = {
    id: string;
    user_id: string;
    message: string;
    root_id?: string;
    create_at: number;
};

let mattermost: MattermostContainer;
let aimock: AIMockContainer;

async function setTestPreferences(mattermostInstance: MattermostContainer, username: string, password: string): Promise<void> {
    const userClient = await mattermostInstance.getClient(username, password);
    const user = await userClient.getMe();
    await userClient.savePreferences(user.id, [
        {user_id: user.id, category: 'tutorial_step', name: user.id, value: '999'},
        {user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false'},
        {user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false'},
        {
            user_id: user.id,
            category: 'drafts',
            name: 'drafts_tour_tip_showed',
            value: JSON.stringify({drafts_tour_tip_showed: true}),
        },
        {user_id: user.id, category: 'crt_thread_pane_step', name: user.id, value: '999'},
    ]);
}

async function setupRegularUser(mattermostInstance: MattermostContainer): Promise<void> {
    await mattermostInstance.createUser('regularuser@sample.com', regularUsername, regularPassword);
    await mattermostInstance.addUserToTeam(regularUsername, 'test');
    await setTestPreferences(mattermostInstance, regularUsername, regularPassword);

    const adminClient = await mattermostInstance.getClient(adminUsername, adminPassword);
    await adminClient.completeSetup({
        organization: 'test',
        install_plugins: [],
    });
}

function getPostsArray(postsResponse: {posts?: Record<string, Post>}): Post[] {
    return Object.values(postsResponse.posts || {});
}

async function waitForPost(
    client: any,
    channelID: string,
    predicate: (post: Post) => boolean,
    timeoutMs: number = 120000,
): Promise<Post> {
    let matchedPost: Post | undefined;

    await expect.poll(async () => {
        const getPosts = client.getPostsForChannel || client.getPosts;
        if (typeof getPosts !== 'function') {
            throw new Error('Mattermost client does not expose getPostsForChannel or getPosts');
        }

        const postsResponse = await getPosts.call(client, channelID, 0, 200);
        const posts = getPostsArray(postsResponse);
        matchedPost = posts.find(predicate);
        return Boolean(matchedPost);
    }, {
        timeout: timeoutMs,
        intervals: [1000, 2000, 5000],
    }).toBe(true);

    return matchedPost!;
}

async function waitForBotUserID(mattermostInstance: MattermostContainer, username: string): Promise<string> {
    const adminClient = await mattermostInstance.getClient(adminUsername, adminPassword);
    let botUserID = '';

    await expect.poll(async () => {
        try {
            const botUser = await adminClient.getUserByUsername(username);
            botUserID = botUser.id;
            return true;
        } catch {
            return false;
        }
    }, {
        timeout: 90000,
        intervals: [1000, 2000, 3000],
    }).toBe(true);

    return botUserID;
}

async function getTownSquareChannelID(client: any): Promise<string> {
    const teams = await client.getMyTeams();
    const team = teams[0];
    const channels = await client.getMyChannels(team.id);
    const townSquare = channels.find((channel: {name: string}) => channel.name === 'town-square');

    if (!townSquare) {
        throw new Error('Could not find town-square channel');
    }

    return townSquare.id;
}

async function selectModelFromDropdown(
    container: Locator,
    page: Page,
    preferredModel: string,
): Promise<string> {
    const dropdownInput = container.locator('input[id^="react-select-"][id$="-input"]').first();
    await expect(dropdownInput).toBeVisible({timeout: 90000});
    await dropdownInput.click();

    const options = page.locator('div[id^="react-select-"][id*="-option-"]');
    await expect(options.first()).toBeVisible({timeout: 90000});

    const optionTexts = (await options.allTextContents()).map((text) => text.trim());
    const preferredIndex = optionTexts.findIndex((text) => text.toLowerCase().includes(preferredModel.toLowerCase()));
    const selectedIndex = preferredIndex >= 0 ? preferredIndex : 0;
    const model = optionTexts[selectedIndex] || preferredModel;

    expect(model.length).toBeGreaterThan(0);
    await options.nth(selectedIndex).click();

    return model;
}

async function ensureServiceCardExpanded(serviceCard: Locator): Promise<void> {
    const serviceNameInput = serviceCard.getByPlaceholder(/service name/i);
    for (let i = 0; i < 3; i++) {
        if (await serviceNameInput.isVisible().catch(() => false)) {
            break;
        }
        await serviceCard.click();
        await serviceCard.page().waitForTimeout(250);
    }
    await expect(serviceNameInput).toBeVisible({timeout: 30000});
}

async function ensureLoggedOut(page: Page, baseURL: string): Promise<void> {
    await page.context().clearCookies();
    await page.goto(baseURL, {waitUntil: 'domcontentloaded'});
    await page.evaluate(() => {
        window.localStorage.clear();
        window.sessionStorage.clear();
    });
    await page.goto(`${baseURL}/login`, {waitUntil: 'domcontentloaded'});
    await expect(page.getByText('Log in to your account')).toBeVisible({timeout: 30000});
}

test.describe.serial('System Console Aimock Live Service Full Flow', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);

        mattermost = await RunSystemConsoleContainer({
            services: [],
            bots: [],
            enableChannelMentionToolCalling: true,
        });
        await setupRegularUser(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: {fixtures: [buildTitleFixture('System console bootstrap')]},
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        if (mattermost) {
            await mattermost.stop();
        }
    });

    test('should install plugin, configure aimock service+agent, and validate DM + channel mention', async ({page}) => {
        test.setTimeout(480000);

        await stubAimockModelFetch(page, [{id: aimockModel, displayName: aimockModel}]);

        const systemConsole = new SystemConsoleHelper(page);
        const mmPage = new MattermostPage(page);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const adminClient = await mattermost.getClient(adminUsername, adminPassword);
        const adminToken = adminClient.getToken();

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToPluginConfig(mattermost.url());

        await systemConsole.clickAddService();

        const serviceCard = page.locator('[class*="ServiceContainer"]').last();
        await expect(serviceCard).toBeVisible();
        await ensureServiceCardExpanded(serviceCard);

        await serviceCard.getByPlaceholder(/service name/i).fill(serviceName);
        await serviceCard.getByRole('combobox').first().selectOption('openaicompatible');
        await serviceCard.getByPlaceholder(/api key/i).fill('mock');
        await serviceCard.getByPlaceholder(/api url/i).fill('http://openai:8080');

        const selectedServiceModel = await selectModelFromDropdown(serviceCard, page, aimockModel);

        const inputTokenLimitField = serviceCard.getByPlaceholder(/input token limit/i);
        if (await inputTokenLimitField.isEnabled()) {
            await inputTokenLimitField.fill('16384');
        }
        const outputTokenLimitField = serviceCard.getByPlaceholder(/output token limit/i);
        if (await outputTokenLimitField.isEnabled()) {
            await outputTokenLimitField.fill('4096');
        }

        const streamingTimeoutInput = serviceCard.getByPlaceholder(/streaming timeout seconds/i);
        if (await streamingTimeoutInput.isVisible().catch(() => false)) {
            await streamingTimeoutInput.fill('30');
        }

        await systemConsole.clickSave();
        await page.reload();
        await page.waitForLoadState('domcontentloaded');
        await systemConsole.navigateToPluginConfig(mattermost.url());

        await expect(page.getByText(serviceName).first()).toBeVisible();
        const reloadedServiceCard = page.locator('[class*="ServiceContainer"]').filter({hasText: serviceName}).first();
        await expect(reloadedServiceCard).toContainText(selectedServiceModel);

        await systemConsole.waitForBotsPanel();

        await page.goto(`${mattermost.url()}/plug/mattermost-ai/agents`);
        await page.waitForLoadState('domcontentloaded');
        const agentPage = new AgentPageHelper(page);
        await agentPage.getCreateButton().waitFor({state: 'visible', timeout: 15000});

        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();
        await agentPage.fillConfigTab({
            displayName: botDisplayName,
            username: botUsername,
            serviceLabel: serviceName,
            instructions: 'You are a concise and deterministic assistant for e2e verification.',
        });
        const modelInput = page.locator('input[id^="react-select-"]').first();
        if (await modelInput.isVisible({timeout: 15000}).catch(() => false)) {
            await selectModelFromDropdown(page.locator('body'), page, aimockModel);
        }
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        await expect(page.getByText(botDisplayName).first()).toBeVisible();
        const createdAgent = (await agentApi.getAgents(adminToken)).find((agent) => agent.name === botUsername);
        expect(createdAgent).toBeTruthy();

        await agentApi.updateAgent(adminToken, createdAgent!.id, {
            enabledNativeTools: [],
            autoEnableNewMCPTools: false,
            enabledMCPTools: [],
            disableTools: false,
        });

        const botUserID = await waitForBotUserID(mattermost, botUsername);

        const dmPrompt = `aimock dm verification ${Date.now()}`;
        const dmReplyText = `AIMOCK_DM_OK_${Date.now()}`;
        const mentionPrompt = `aimock mention verification ${Date.now()}`;
        const mentionReplyText = `AIMOCK_MENTION_OK_${Date.now()}`;

        await aimock.setFixtures(mergeFixtureFiles(
            buildTextResponse({
                userMessage: dmPrompt,
                content: dmReplyText,
                title: 'Aimock live DM',
            }),
            buildTextResponse({
                userMessage: mentionPrompt,
                content: mentionReplyText,
                title: 'Aimock live mention',
            }),
        ));

        await ensureLoggedOut(page, mattermost.url());
        await mmPage.login(`${mattermost.url()}/login`, regularUsername, regularPassword);

        const regularClient = await mattermost.getClient(regularUsername, regularPassword);
        const regularUser = await regularClient.getMe();
        const dmChannel = await regularClient.createDirectChannel([regularUser.id, botUserID]);

        await page.goto(`${mattermost.url()}/test/messages/@${botUsername}`);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const postTimeSkewMs = 5000;
        const dmStartTime = Date.now();
        await mmPage.sendChannelMessage(dmPrompt);

        const dmBotReply = await waitForPost(
            regularClient,
            dmChannel.id,
            (post) => post.user_id === botUserID &&
                post.create_at >= dmStartTime - postTimeSkewMs &&
                post.message.includes(dmReplyText),
        );
        expect(dmBotReply.user_id).toBe(botUserID);
        expect(dmBotReply.message).toContain(dmReplyText);

        const townSquareChannelID = await getTownSquareChannelID(regularClient);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const mentionStartTime = Date.now();
        await mmPage.mentionBot(botUsername, mentionPrompt);
        await expect(page.getByText(`@${botUsername} ${mentionPrompt}`, {exact: true})).toBeVisible();

        const mentionPost = await waitForPost(
            regularClient,
            townSquareChannelID,
            (post) => post.user_id === regularUser.id &&
                post.create_at >= mentionStartTime - postTimeSkewMs &&
                post.message.includes(mentionPrompt),
            60000,
        );

        const mentionBotReply = await waitForPost(
            regularClient,
            townSquareChannelID,
            (post) => post.user_id === botUserID &&
                post.create_at >= mentionStartTime - postTimeSkewMs &&
                post.root_id === mentionPost.id &&
                post.message.includes(mentionReplyText),
        );
        expect(mentionBotReply.user_id).toBe(botUserID);
        expect(mentionBotReply.message).toContain(mentionReplyText);
    });
});
