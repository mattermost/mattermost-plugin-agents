// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

import {test, expect, Locator, Page} from '@playwright/test';

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

const knownLLMErrorPatterns = [
    'llm did not return a result',
    'an error occurred while accessing the llm',
    'bifrost error:',
];

const maxLiveReplyAttempts = 3;

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

function isKnownErrorResponse(message: string): boolean {
    const normalized = message.toLowerCase();
    return knownLLMErrorPatterns.some((pattern) => normalized.includes(pattern));
}

function modelTokens(value: string): string[] {
    return value.toLowerCase().split(/[^a-z0-9]+/).filter((token) => token.length >= 3 || /^[0-9]+$/.test(token));
}

function canonicalModel(value: string): string {
    return value.toLowerCase().replace(/[^a-z0-9]/g, '');
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

async function requestNonErrorDMReply(
    mmPage: MattermostPage,
    regularClient: any,
    dmChannelID: string,
    botUserID: string,
): Promise<Post> {
    for (let attempt = 1; attempt <= maxLiveReplyAttempts; attempt++) {
        const dmPrompt = `Live DM verification ${Date.now()} attempt-${attempt}`;
        const dmStartTime = Date.now();
        await mmPage.sendChannelMessage(dmPrompt);

        const dmBotReply = await waitForPost(
            regularClient,
            dmChannelID,
            (post) => post.user_id === botUserID &&
                post.create_at >= dmStartTime &&
                post.message.trim().length > 0,
            180000,
        );

        if (!isKnownErrorResponse(dmBotReply.message)) {
            return dmBotReply;
        }
    }

    throw new Error(`Bot returned known error reply for all ${maxLiveReplyAttempts} DM attempts.`);
}

async function requestNonErrorMentionReply(
    page: Page,
    mmPage: MattermostPage,
    regularClient: any,
    channelID: string,
    botUsername: string,
    botUserID: string,
    regularUserID: string,
): Promise<Post> {
    for (let attempt = 1; attempt <= maxLiveReplyAttempts; attempt++) {
        const mentionPrompt = `live mention verification ${Date.now()} attempt-${attempt}`;
        const mentionStartTime = Date.now();
        await mmPage.mentionBot(botUsername, mentionPrompt);
        await expect(page.getByText(`@${botUsername} ${mentionPrompt}`, {exact: true})).toBeVisible();

        const mentionPost = await waitForPost(
            regularClient,
            channelID,
            (post) => post.user_id === regularUserID &&
                post.create_at >= mentionStartTime &&
                post.message.includes(mentionPrompt),
            60000,
        );

        const mentionBotReply = await waitForPost(
            regularClient,
            channelID,
            (post) => post.user_id === botUserID &&
                post.create_at >= mentionStartTime &&
                post.root_id === mentionPost.id &&
                post.message.trim().length > 0,
            180000,
        );

        if (!isKnownErrorResponse(mentionBotReply.message)) {
            return mentionBotReply;
        }
    }

    throw new Error(`Bot returned known error reply for all ${maxLiveReplyAttempts} mention attempts.`);
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

async function selectModelFromDropdown(
    container: Locator,
    page: Page,
    preferredModel: string,
    avoidTokens: string[] = [],
): Promise<string> {
    const dropdownInput = container.locator('input[id^="react-select-"][id$="-input"]').first();
    await expect(dropdownInput).toBeVisible({timeout: 90000});
    await dropdownInput.click();

    const options = page.locator('div[id^="react-select-"][id*="-option-"]');
    await expect(options.first()).toBeVisible({timeout: 90000});

    const optionTexts = (await options.allTextContents()).map((text) => text.trim());
    const preferredTokens = modelTokens(preferredModel);
    const canonicalPreferred = canonicalModel(preferredModel);
    const normalizedAvoidTokens = avoidTokens.map((token) => token.toLowerCase());

    let selectedIndex = 0;
    let bestScore = Number.NEGATIVE_INFINITY;
    for (const [index, optionText] of optionTexts.entries()) {
        const normalizedOption = optionText.toLowerCase();
        const canonicalOption = canonicalModel(optionText);
        const optionTokens = modelTokens(optionText);
        const preferredMatchCount = preferredTokens.filter((token) => optionTokens.includes(token) || normalizedOption.includes(token)).length;
        const isCanonicalPreferredMatch = canonicalPreferred.length > 0 &&
            (canonicalOption.includes(canonicalPreferred) || canonicalPreferred.includes(canonicalOption));
        const hasAvoidedToken = normalizedAvoidTokens.some((token) => normalizedOption.includes(token));
        const score = (isCanonicalPreferredMatch ? 1000 : 0) + (preferredMatchCount * 10) + (hasAvoidedToken ? -100 : 0);

        if (score > bestScore) {
            bestScore = score;
            selectedIndex = index;
        }
    }

    const model = optionTexts[selectedIndex] || '';
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

async function ensureBotCardExpanded(botCard: Locator): Promise<void> {
    const displayNameInput = botCard.getByPlaceholder(/display name/i);
    for (let i = 0; i < 3; i++) {
        if (await displayNameInput.isVisible().catch(() => false)) {
            break;
        }
        await botCard.click();
        await botCard.page().waitForTimeout(250);
    }
    await expect(displayNameInput).toBeVisible({timeout: 30000});
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

function isPersistedModelMatch(cardText: string, selectedModel: string): boolean {
    const normalizedCardText = cardText.toLowerCase();
    const normalizedSelectedModel = selectedModel.toLowerCase();

    if (normalizedCardText.includes(normalizedSelectedModel)) {
        return true;
    }

    const modelTokens = normalizedSelectedModel.split(/[^a-z0-9]+/).filter((token) => token.length >= 3);
    if (modelTokens.length === 0) {
        return false;
    }

    const matchedTokenCount = modelTokens.filter((token) => normalizedCardText.includes(token)).length;
    return matchedTokenCount >= Math.min(modelTokens.length, 2);
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
        const avoidModelTokens = selectedProviderType === 'anthropic' ? ['haiku'] : [];
        let selectedServiceModel = '';
        let selectedBotModel = '';

        // 1) Login as admin and configure service + bot in System Console.
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToPluginConfig(mattermost.url());

        const hasServiceAlready = await page.getByText(serviceName).first().isVisible().catch(() => false);
        if (!hasServiceAlready) {
            await systemConsole.clickAddService();

            const serviceCard = page.locator('[class*="ServiceContainer"]').last();
            await expect(serviceCard).toBeVisible();
            await ensureServiceCardExpanded(serviceCard);

            await serviceCard.getByPlaceholder(/service name/i).fill(serviceName);
            await serviceCard.getByRole('combobox').first().selectOption(provider.service.type);
            await serviceCard.getByPlaceholder(/api key/i).fill(provider.service.apiKey);

            if (provider.service.type === 'openaicompatible') {
                await serviceCard.getByPlaceholder(/api url/i).fill(provider.service.apiURL);
            }

            selectedServiceModel = await selectModelFromDropdown(serviceCard, page, provider.service.defaultModel, avoidModelTokens);
            await serviceCard.getByPlaceholder(/input token limit/i).fill(String(provider.service.tokenLimit));
            await serviceCard.getByPlaceholder(/output token limit/i).fill(String(provider.service.outputTokenLimit));

            const streamingTimeoutInput = serviceCard.getByPlaceholder(/streaming timeout seconds/i);
            if (await streamingTimeoutInput.isVisible().catch(() => false)) {
                await streamingTimeoutInput.fill(String(provider.service.streamingTimeoutSeconds || 30));
            }
        }

        await systemConsole.waitForBotsPanel();
        const hasBotAlready = await page.getByText(botDisplayName).first().isVisible().catch(() => false);
        if (!hasBotAlready) {
            await systemConsole.clickAddBot();

            const botCard = page.locator('[class*="BotContainer"]').last();
            await expect(botCard).toBeVisible();
            await ensureBotCardExpanded(botCard);

            await botCard.getByPlaceholder(/display name/i).fill(botDisplayName);
            await botCard.getByPlaceholder(/(bot|agent) username/i).fill(botUsername);
            await botCard.locator('select').first().selectOption({label: serviceName});
            selectedBotModel = await selectModelFromDropdown(botCard, page, provider.service.defaultModel, avoidModelTokens);
            await botCard.getByPlaceholder(/how would you like/i).fill(provider.bot.customInstructions);
        }

        await systemConsole.clickSave();
        await page.reload();
        await page.waitForLoadState('domcontentloaded');

        await expect(page.getByText(serviceName).first()).toBeVisible();
        await expect(page.getByText(botDisplayName).first()).toBeVisible();
        if (selectedServiceModel) {
            const reloadedServiceCard = page.locator('[class*="ServiceContainer"]').filter({hasText: serviceName}).first();
            await expect.poll(async () => {
                const cardText = (await reloadedServiceCard.textContent()) || '';
                return isPersistedModelMatch(cardText, selectedServiceModel);
            }).toBe(true);
        }

        if (selectedBotModel) {
            const reloadedBotCard = page.locator('[class*="BotContainer"]').filter({hasText: botDisplayName}).first();
            await reloadedBotCard.click();
            await expect.poll(async () => {
                const cardText = (await reloadedBotCard.textContent()) || '';
                return isPersistedModelMatch(cardText, selectedBotModel);
            }).toBe(true);
        }

        // 2) Validate bot account exists after saving.
        const botUserID = await waitForBotUserID(mattermost, botUsername);

        // 3) Login as regular user and verify DM flow with live service.
        await ensureLoggedOut(page, mattermost.url());
        await mmPage.login(`${mattermost.url()}/login`, regularUsername, regularPassword);

        const regularClient = await mattermost.getClient(regularUsername, regularPassword);
        const regularUser = await regularClient.getMe();
        const dmChannel = await regularClient.createDirectChannel([regularUser.id, botUserID]);

        await page.goto(`${mattermost.url()}/test/messages/@${botUsername}`);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const dmBotReply = await requestNonErrorDMReply(
            mmPage,
            regularClient,
            dmChannel.id,
            botUserID,
        );
        expect(dmBotReply.user_id).toBe(botUserID);
        expect(isKnownErrorResponse(dmBotReply.message)).toBe(false);

        // 4) Verify channel mention flow in town-square.
        const townSquareChannelID = await getTownSquareChannelID(regularClient);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const mentionBotReply = await requestNonErrorMentionReply(
            page,
            mmPage,
            regularClient,
            townSquareChannelID,
            botUsername,
            botUserID,
            regularUser.id,
        );
        expect(mentionBotReply.user_id).toBe(botUserID);
        expect(isKnownErrorResponse(mentionBotReply.message)).toBe(false);
    });
});
