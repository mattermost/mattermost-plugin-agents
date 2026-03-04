// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

import {test, expect} from '@playwright/test';

import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {SystemConsoleHelper} from 'helpers/system-console';
import {
    ProviderBundle,
    createCustomProvider,
    getAPIConfig,
} from 'helpers/api-config';
import {checkAPIHealth} from 'helpers/api-health-check';
import {attachAPIErrorContext} from 'helpers/log-scanner';

const adminUsername = 'admin';
const adminPassword = 'admin';
const regularUsername = 'regularuser';
const regularPassword = 'regularuser';

type ProviderType = 'anthropic' | 'openai';
type Post = {
    id: string;
    user_id: string;
    message: string;
    root_id?: string;
    create_at: number;
};

const selectedProviderType = getSelectedProviderType();
const apiConfig = getAPIConfig();
const shouldRunProvider = selectedProviderType === 'anthropic' ? apiConfig.hasAnthropicKey : apiConfig.hasOpenAIKey;
const missingKeyMessage = selectedProviderType === 'anthropic' ?
    'Skipping live system-console flow: ANTHROPIC_API_KEY is required (or set E2E_LIVE_PROVIDER=openai).' :
    'Skipping live system-console flow: OPENAI_API_KEY is required (or set E2E_LIVE_PROVIDER=anthropic).';

let mattermost: MattermostContainer;
let provider: ProviderBundle;

function getSelectedProviderType(): ProviderType {
    const raw = (process.env.E2E_LIVE_PROVIDER || 'anthropic').trim().toLowerCase();
    if (raw === 'anthropic' || raw === 'openai') {
        return raw;
    }

    throw new Error(`Invalid E2E_LIVE_PROVIDER="${raw}". Expected "anthropic" or "openai".`);
}

function findPluginFile(): string {
    const distPath = path.resolve(__dirname, '../../../dist');
    const files = fs.readdirSync(distPath);
    const pluginFile = files.find((file) => file.endsWith('.tar.gz'));

    if (!pluginFile) {
        throw new Error(`No plugin tarball found in ${distPath}. Run "make dist" first.`);
    }

    return path.join(distPath, pluginFile);
}

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

async function setupTestUsers(mattermostInstance: MattermostContainer): Promise<void> {
    await mattermostInstance.createUser('regularuser@sample.com', regularUsername, regularPassword);
    await mattermostInstance.addUserToTeam(regularUsername, 'test');
    await setTestPreferences(mattermostInstance, adminUsername, adminPassword);
    await setTestPreferences(mattermostInstance, regularUsername, regularPassword);

    const adminClient = await mattermostInstance.getAdminClient();
    await adminClient.completeSetup({
        organization: 'test',
        install_plugins: [],
    });
}

async function installPlugin(mattermostInstance: MattermostContainer): Promise<void> {
    const pluginPath = findPluginFile();
    const pluginConfig = {
        config: {
            allowPrivateChannels: true,
            disableFunctionCalls: false,
            enableLLMTrace: true,
            enableUserRestrictions: false,
            enableVectorIndex: false,
            services: [],
            bots: [],
        },
    };

    await mattermostInstance.installPlugin(pluginPath, 'mattermost-ai', pluginConfig);
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
        const postsResponse = await client.getPostsForChannel(channelID, 0, 200);
        const posts = getPostsArray(postsResponse);
        matchedPost = posts.find(predicate);
        return Boolean(matchedPost);
    }, {
        timeout: timeoutMs,
        intervals: [1000, 2000, 5000],
    }).toBe(true);

    return matchedPost!;
}

async function waitForBotUserID(mattermostInstance: MattermostContainer, botUsername: string): Promise<string> {
    const adminClient = await mattermostInstance.getAdminClient();
    let botUserID = '';

    await expect.poll(async () => {
        try {
            const botUser = await adminClient.getUserByUsername(botUsername);
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

test.describe.serial('System Console Real Live Service Full Flow', () => {
    test.beforeAll(async () => {
        if (!shouldRunProvider) {
            return;
        }

        provider = createCustomProvider(selectedProviderType, {
            name: selectedProviderType === 'anthropic' ? 'Anthropic Live Service' : 'OpenAI Live Service',
        }, {
            name: selectedProviderType === 'anthropic' ? 'anthropiclive' : 'openailive',
            displayName: selectedProviderType === 'anthropic' ? 'Anthropic Live Agent' : 'OpenAI Live Agent',
            customInstructions: 'You are a concise and helpful assistant for e2e verification.',
            enabledNativeTools: [],
            reasoningEnabled: true,
            disableTools: false,
        });

        await checkAPIHealth(provider.service);

        mattermost = await new MattermostContainer().start();
        await setupTestUsers(mattermost);
        await installPlugin(mattermost);
    });

    test.afterEach(async ({}, testInfo) => {
        await attachAPIErrorContext(testInfo);
    });

    test.afterAll(async () => {
        if (mattermost) {
            await mattermost.stop();
        }
    });

    test('should install plugin, configure live service+agent, and validate DM + channel mention', async ({page}) => {
        test.skip(!shouldRunProvider, missingKeyMessage);
        test.setTimeout(480000);

        const systemConsole = new SystemConsoleHelper(page);
        const mmPage = new MattermostPage(page);
        const serviceName = provider.service.name;
        const botDisplayName = provider.bot.displayName;
        const botUsername = provider.bot.name;

        // 1) Login as admin and configure service + bot in System Console.
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToPluginConfig(mattermost.url());

        await expect(systemConsole.getNoServicesMessage()).toBeVisible();
        await systemConsole.clickAddService();

        const serviceCard = page.locator('[class*="ServiceContainer"]').last();
        await expect(serviceCard).toBeVisible();
        await serviceCard.click();

        await serviceCard.getByLabel(/service name/i).fill(serviceName);
        await serviceCard.getByLabel(/service type/i).selectOption(provider.service.type);
        await serviceCard.getByLabel(/^API Key$/i).fill(provider.service.apiKey);

        if (provider.service.type === 'openaicompatible') {
            await serviceCard.getByLabel(/api url/i).fill(provider.service.apiURL);
        }

        await serviceCard.getByLabel(/default model/i).fill(provider.service.defaultModel);
        await serviceCard.getByLabel(/input token limit/i).fill(String(provider.service.tokenLimit));
        await serviceCard.getByLabel(/output token limit/i).fill(String(provider.service.outputTokenLimit));

        const streamingTimeoutInput = serviceCard.getByLabel(/streaming timeout seconds/i);
        if (await streamingTimeoutInput.isVisible().catch(() => false)) {
            await streamingTimeoutInput.fill(String(provider.service.streamingTimeoutSeconds || 30));
        }

        await systemConsole.waitForBotsPanel();
        await systemConsole.clickAddBot();

        const botCard = page.locator('[class*="BotContainer"]').last();
        await expect(botCard).toBeVisible();
        await botCard.click();

        await botCard.getByLabel(/display name/i).fill(botDisplayName);
        await botCard.getByLabel(/(bot|agent) username/i).fill(botUsername);
        await botCard.getByLabel(/ai service/i).selectOption({label: serviceName});
        await botCard.getByLabel(/custom instructions/i).fill(provider.bot.customInstructions);

        await systemConsole.clickSave();
        await page.reload();
        await page.waitForLoadState('domcontentloaded');

        await expect(page.getByText(serviceName).first()).toBeVisible();
        await expect(page.getByText(botDisplayName).first()).toBeVisible();

        // 2) Validate bot account exists after saving.
        const botUserID = await waitForBotUserID(mattermost, botUsername);

        // 3) Login as regular user and verify DM flow with live service.
        await page.goto(`${mattermost.url()}/logout`);
        await mmPage.login(mattermost.url(), regularUsername, regularPassword);

        const regularClient = await mattermost.getClient(regularUsername, regularPassword);
        const regularUser = await regularClient.getMe();
        const dmChannel = await regularClient.createDirectChannel([regularUser.id, botUserID]);

        await page.goto(`${mattermost.url()}/test/messages/@${botUsername}`);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const dmPrompt = `Live DM verification ${Date.now()}`;
        const dmStartTime = Date.now();
        await mmPage.sendChannelMessage(dmPrompt);

        await waitForPost(
            regularClient,
            dmChannel.id,
            (post) => post.user_id === botUserID &&
                post.create_at >= dmStartTime &&
                post.message.trim().length > 0,
            180000,
        );

        // 4) Verify channel mention flow in town-square.
        const townSquareChannelID = await getTownSquareChannelID(regularClient);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const mentionPrompt = `live mention verification ${Date.now()}`;
        const mentionStartTime = Date.now();
        await mmPage.mentionBot(botUsername, mentionPrompt);
        await expect(page.getByText(mentionPrompt)).toBeVisible();

        const mentionPost = await waitForPost(
            regularClient,
            townSquareChannelID,
            (post) => post.user_id === regularUser.id &&
                post.create_at >= mentionStartTime &&
                post.message.includes(mentionPrompt),
            60000,
        );

        await waitForPost(
            regularClient,
            townSquareChannelID,
            (post) => post.user_id === botUserID &&
                post.create_at >= mentionStartTime &&
                post.root_id === mentionPost.id &&
                post.message.trim().length > 0,
            180000,
        );
    });
});
