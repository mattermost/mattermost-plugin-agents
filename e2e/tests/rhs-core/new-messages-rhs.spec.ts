// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, test} from '@playwright/test';
import type {Locator, Page} from '@playwright/test';
import type {Client4} from '@mattermost/client';
import type {Post} from '@mattermost/types/posts';

import {AIPlugin} from 'helpers/ai-plugin';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {
    buildChatCompletionMockRule,
    buildTextResponse,
    OpenAIMockContainer,
    RunOpenAIMocks,
} from 'helpers/openai-mock';
import type {
    OpenAIChatCompletionRequest,
    OpenAIMockHistoryEntry,
} from 'helpers/openai-mock';
import RunContainer from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const botUsername = 'mock';
const secondBotUsername = 'second';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

type TypedChatCompletionRequest = OpenAIChatCompletionRequest & {
    tools?: unknown[];
};

type MarkerProviderRequest = {
    path: string;
    body: TypedChatCompletionRequest;
};

type UnreadScenario = {
    aiPlugin: AIPlugin;
    userClient: Client4;
    channelID: string;
    baselineMessage: string;
    unreadMessage: string;
    channelPostIDsBeforeAnalysis: string[];
};

test.beforeAll(async () => {
    test.setTimeout(180000);
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.beforeEach(async () => {
    await openAIMock.resetMocks();
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

function getPosts(response: {posts: Record<string, Post>}): Post[] {
    return Object.values(response.posts);
}

function escapeRegex(value: string): string {
    return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function isChatCompletionRequest(value: unknown): value is TypedChatCompletionRequest {
    if (typeof value !== 'object' || value === null) {
        return false;
    }

    const candidate = value as OpenAIChatCompletionRequest;
    return Array.isArray(candidate.messages) && (candidate.tools === undefined || Array.isArray(candidate.tools));
}

function chatCompletionRequestText(request: TypedChatCompletionRequest): string {
    return (request.messages ?? []).map((message) => {
        if (typeof message.content === 'string') {
            return message.content;
        }
        if (Array.isArray(message.content)) {
            return message.content.map((part) => part.text ?? '').join('\n');
        }
        return '';
    }).join('\n');
}

function markerProviderRequests(
    history: OpenAIMockHistoryEntry[],
    marker: string,
): MarkerProviderRequest[] {
    const requests: MarkerProviderRequest[] = [];

    for (const entry of history) {
        if (!isChatCompletionRequest(entry.request.body)) {
            continue;
        }
        if (!chatCompletionRequestText(entry.request.body).includes(marker)) {
            continue;
        }

        requests.push({
            path: typeof entry.request.path === 'string' ? entry.request.path : '',
            body: entry.request.body,
        });
    }

    return requests;
}

async function getTownSquare(client: Client4) {
    const teams = await client.getMyTeams();
    const channels = await client.getMyChannels(teams[0].id);
    const townSquare = channels.find((channel) => channel.name === 'town-square');
    if (!townSquare) {
        throw new Error('Town Square channel was not available');
    }
    return townSquare;
}

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);

    await mmPage.login(mattermost.url(), username, password);

    return {mmPage, aiPlugin};
}

async function establishUnreadScenario(
    page: Page,
    baselineMessage: string,
    unreadMessage: string,
): Promise<UnreadScenario> {
    const userClient = await mattermost.getClient(username, password);
    const townSquare = await getTownSquare(userClient);
    const {mmPage, aiPlugin} = await setupTestPage(page);

    await mmPage.sendChannelMessage(baselineMessage);
    await expect(page.getByText(baselineMessage, {exact: true})).toBeVisible({timeout: 30000});

    const unreadPost = await mmPage.sendMessageAsUser(
        mattermost,
        'seconduser',
        'seconduser',
        unreadMessage,
        townSquare.id,
    );
    await expect(page.locator(`#post_${unreadPost.id}`).getByText(unreadMessage, {exact: true})).
        toBeVisible({timeout: 30000});

    await mmPage.markMessageAsUnread(unreadPost.id);

    const channelPostIDsBeforeAnalysis = getPosts(await userClient.getPosts(townSquare.id, 0, 200)).
        map((post) => post.id).
        sort();

    return {
        aiPlugin,
        userClient,
        channelID: townSquare.id,
        baselineMessage,
        unreadMessage,
        channelPostIDsBeforeAnalysis,
    };
}

async function openSeparatorMenu(page: Page, aiPlugin: AIPlugin): Promise<Locator> {
    await aiPlugin.clickNewMessagesButton();
    const menu = page.getByTestId('dropdownmenu');
    await expect(menu).toBeVisible();
    return menu;
}

async function expectIntervalProviderRequest(
    marker: string,
    promptMarker: string,
    scenario: UnreadScenario,
    expectedPath?: RegExp,
): Promise<void> {
    const requests = markerProviderRequests(await openAIMock.getHistory(), marker);
    expect(requests).toHaveLength(1);

    const request = requests[0];
    const requestText = chatCompletionRequestText(request.body);
    expect(requestText).toContain(promptMarker);
    expect(requestText).toContain(scenario.unreadMessage);
    expect(requestText).not.toContain(scenario.baselineMessage);
    expect((request.body.tools ?? []).length).toBe(0);

    if (expectedPath) {
        expect(request.path).toMatch(expectedPath);
    }
}

async function expectChannelUnchanged(scenario: UnreadScenario, responseText: string): Promise<void> {
    const channelPostsAfterAnalysis = getPosts(
        await scenario.userClient.getPosts(scenario.channelID, 0, 200),
    );
    expect(channelPostsAfterAnalysis.map((post) => post.id).sort()).
        toEqual(scenario.channelPostIDsBeforeAnalysis);
    expect(channelPostsAfterAnalysis.filter((post) => post.message === scenario.baselineMessage)).toHaveLength(1);
    expect(channelPostsAfterAnalysis.filter((post) => post.message === scenario.unreadMessage)).toHaveLength(1);
    expect(channelPostsAfterAnalysis.filter((post) => post.message.includes(responseText))).toHaveLength(0);
}

async function expectPersistedAndRestored(
    page: Page,
    scenario: UnreadScenario,
    expectedBotUsername: string,
    historyTitle: string,
    responseText: string,
): Promise<void> {
    const currentUser = await scenario.userClient.getMe();
    const bot = await scenario.userClient.getUserByUsername(expectedBotUsername);
    const botDM = await scenario.userClient.createDirectChannel([currentUser.id, bot.id]);

    await expect.poll(async () => {
        const posts = getPosts(await scenario.userClient.getPosts(botDM.id, 0, 200));
        return posts.some((post) => (
            post.user_id === bot.id &&
            post.message.includes(responseText) &&
            typeof post.props?.conversation_id === 'string'
        ));
    }, {
        message: `${historyTitle} result did not persist with a conversation ID`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).toBe(true);

    await page.reload({waitUntil: 'domcontentloaded'});
    await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 60000});
    await scenario.aiPlugin.openRHS();
    await scenario.aiPlugin.openChatHistory();

    const historyItem = scenario.aiPlugin.threadsListContainer.getByText(historyTitle, {exact: true});
    await expect(historyItem).toBeVisible({timeout: 30000});
    await historyItem.click();
    await expect(scenario.aiPlugin.getRhsContainer().getByText(responseText, {exact: true})).
        toBeVisible({timeout: 30000});
}

test.describe('New Messages Line RHS Functionality', () => {
    test('summarizes only unread messages and restores the result from history', async ({page}) => {
        test.setTimeout(120000);

        const timestamp = Date.now();
        const baselineMarker = `SUMMARIZE_VIEWED_${timestamp}`;
        const unreadMarker = `SUMMARIZE_UNREAD_${timestamp}`;
        const baselineMessage = `${baselineMarker} deployment planning was already reviewed.`;
        const unreadMessage = `${unreadMarker} the lunar deployment window moved to Friday.`;
        const responseText = `${unreadMarker} summary: the lunar deployment window moved to Friday.`;
        const promptMarker = 'You are an expert that summarizes unread posts from a channel.';
        const scenario = await establishUnreadScenario(page, baselineMessage, unreadMessage);

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(responseText), {
                bodyMatches: `(?s)${escapeRegex(promptMarker)}.*${escapeRegex(unreadMessage)}`,
                times: 1,
            }),
        ]);

        const menu = await openSeparatorMenu(page, scenario.aiPlugin);
        const summarizeMenuItem = menu.getByRole('button', {name: 'Summarize new messages', exact: true});
        await expect(summarizeMenuItem).toBeVisible();
        await summarizeMenuItem.click();

        await scenario.aiPlugin.waitForBotResponse(responseText);
        await expect(scenario.aiPlugin.getRhsContainer().getByText(responseText, {exact: true})).toBeVisible();

        await expectIntervalProviderRequest(unreadMarker, promptMarker, scenario);
        await expectChannelUnchanged(scenario, responseText);
        await expectPersistedAndRestored(
            page,
            scenario,
            botUsername,
            'Summarize Unreads',
            responseText,
        );
    });

    test('finds open questions from new messages and restores the result from history', async ({page}) => {
        test.setTimeout(120000);

        const timestamp = Date.now();
        const baselineMarker = `OPEN_QUESTIONS_VIEWED_${timestamp}`;
        const unreadMarker = `OPEN_QUESTIONS_UNREAD_${timestamp}`;
        const baselineMessage = `${baselineMarker} the earlier checklist discussion is complete.`;
        const unreadMessage = `${unreadMarker}: Who will approve the lunar release checklist?`;
        const responseText = `${unreadMarker} open question: Who will approve the lunar release checklist?`;
        const promptMarker = 'Analyze the conversation thread to find open questions.';
        const scenario = await establishUnreadScenario(page, baselineMessage, unreadMessage);

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(responseText), {
                bodyMatches: `(?s)${escapeRegex(promptMarker)}.*${escapeRegex(unreadMessage)}`,
                times: 1,
            }),
        ]);

        const menu = await openSeparatorMenu(page, scenario.aiPlugin);
        const openQuestionsMenuItem = menu.getByRole('button', {name: 'Find open questions', exact: true});
        await expect(openQuestionsMenuItem).toBeVisible();
        await openQuestionsMenuItem.click();

        await scenario.aiPlugin.waitForBotResponse(responseText);
        await expect(scenario.aiPlugin.getRhsContainer().getByText(responseText, {exact: true})).toBeVisible();

        await expectIntervalProviderRequest(unreadMarker, promptMarker, scenario);
        await expectChannelUnchanged(scenario, responseText);
        await expectPersistedAndRestored(page, scenario, botUsername, 'Open Questions', responseText);
    });

    test('finds unread action items with the selected agent and restores the result from history', async ({page}) => {
        test.setTimeout(120000);

        const timestamp = Date.now();
        const baselineMarker = `ACTION_ITEMS_VIEWED_${timestamp}`;
        const unreadMarker = `ACTION_ITEMS_UNREAD_${timestamp}`;
        const baselineMessage = `${baselineMarker} the previous release tasks are complete.`;
        const unreadMessage = `${unreadMarker}: @regularuser, please publish the lunar release notes by Friday.`;
        const responseText = `${unreadMarker} action item: regularuser must publish the lunar release notes by Friday.`;
        const defaultPathTrapText = `${unreadMarker} wrong default-agent path`;
        const promptMarker = 'Analyze the conversation thread to find action items.';
        const requestPattern = `(?s)${escapeRegex(promptMarker)}.*${escapeRegex(unreadMessage)}`;
        const scenario = await establishUnreadScenario(page, baselineMessage, unreadMessage);

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(defaultPathTrapText), {
                bodyMatches: requestPattern,
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(responseText), {
                bodyMatches: requestPattern,
                botPrefix: secondBotUsername,
                times: 1,
            }),
        ]);

        let menu = await openSeparatorMenu(page, scenario.aiPlugin);
        const defaultBotSelector = menu.locator('[role="button"][title="Mock Bot"]');
        await expect(defaultBotSelector).toBeVisible();
        await defaultBotSelector.click();

        const secondBotOption = page.getByRole('button', {name: 'Second Bot', exact: true});
        await expect(secondBotOption).toBeVisible();
        await secondBotOption.click();
        await page.keyboard.press('Escape');
        await expect(menu).not.toBeVisible();

        menu = await openSeparatorMenu(page, scenario.aiPlugin);
        await expect(menu.locator('[role="button"][title="Second Bot"]')).toBeVisible();
        const actionItemsMenuItem = menu.getByRole('button', {name: 'Find action items', exact: true});
        await expect(actionItemsMenuItem).toBeVisible();
        await actionItemsMenuItem.click();

        const rhs = scenario.aiPlugin.getRhsContainer();
        const expectedResponse = rhs.getByText(responseText, {exact: true});
        const defaultPathTrap = rhs.getByText(defaultPathTrapText, {exact: true});
        await expect(expectedResponse.or(defaultPathTrap)).toBeVisible({timeout: 30000});
        await expect(expectedResponse).toBeVisible();
        await expect(defaultPathTrap).toHaveCount(0);
        await scenario.aiPlugin.waitForBotResponse(responseText);

        await expectIntervalProviderRequest(
            unreadMarker,
            promptMarker,
            scenario,
            /^\/second\/(?:v1\/)?chat\/completions$/,
        );
        await expectChannelUnchanged(scenario, responseText);
        await expectPersistedAndRestored(
            page,
            scenario,
            secondBotUsername,
            'Action Items',
            responseText,
        );
    });
});
