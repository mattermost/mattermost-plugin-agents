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
import {mattermostAIPluginRoutes, PluginRoutesApi} from 'helpers/plugin-http';
import RunContainer from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const botUsername = 'mock';

type RawSearchResponse = {
    results: Array<{
        post_id: string;
    }>;
};

type SearchSource = {
    postId: string;
    channelId: string;
    userId: string;
    content: string;
    score: number;
};

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

function getPosts(response: {posts: Record<string, Post>}): Post[] {
    return Object.values(response.posts);
}

function escapeRegex(value: string): string {
    return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function mainRequestPattern(systemPromptMarker: string, firstMarker: string, secondMarker: string): string {
    const firstThenSecond = `${escapeRegex(firstMarker)}.*${escapeRegex(secondMarker)}`;
    const secondThenFirst = `${escapeRegex(secondMarker)}.*${escapeRegex(firstMarker)}`;
    return `(?s)${escapeRegex(systemPromptMarker)}.*(?:${firstThenSecond}|${secondThenFirst})`;
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

async function getBotDM(client: Client4, username = botUsername) {
    const user = await client.getMe();
    const bot = await client.getUserByUsername(username);
    const channel = await client.createDirectChannel([user.id, bot.id]);
    return {bot, channel};
}

async function setPostCreateTimes(posts: Array<{id: string; createAt: number}>): Promise<void> {
    const database = await mattermost.db();
    try {
        for (const post of posts) {
            await database.query(
                'UPDATE Posts SET CreateAt = $1, UpdateAt = $1 WHERE Id = $2',
                [post.createAt, post.id],
            );
        }
    } finally {
        await database.end();
    }
}

async function submitCenterComposerCommand(mmPage: MattermostPage, command: string): Promise<void> {
    await expect(mmPage.postTextbox).toBeEditable({timeout: 30000});
    await mmPage.postTextbox.fill(command);
    await expect(mmPage.postTextbox).toHaveValue(command);
    await mmPage.sendButton.click();
}

async function expectCommandNotPersisted(
    client: Client4,
    channelID: string,
    command: string,
): Promise<void> {
    const posts = getPosts(await client.getPosts(channelID, 0, 200));
    expect(posts.filter((post) => post.message === command)).toHaveLength(0);
}

async function waitForIndexedPost(
    routes: PluginRoutesApi,
    client: Client4,
    channelID: string,
    query: string,
    postID: string,
): Promise<void> {
    await expect.poll(async () => {
        const response = await routes.postJson('search/raw', client.getToken(), {
            query,
            channel_id: channelID,
            limit: 10,
        }) as RawSearchResponse;
        return response.results.some((result) => result.post_id === postID);
    }, {
        message: `post ${postID} was not returned by the real semantic search index`,
        timeout: 30000,
        intervals: [250, 500, 1000, 2000],
    }).toBe(true);
}

async function waitForPersistedBotResult(
    client: Client4,
    expectedText: string,
    expectedBotUsername = botUsername,
): Promise<Post> {
    const {bot, channel} = await getBotDM(client, expectedBotUsername);
    let matchedPost: Post | undefined;

    await expect.poll(async () => {
        const posts = getPosts(await client.getPosts(channel.id, 0, 200));
        matchedPost = posts.find((post) => (
            post.user_id === bot.id &&
            post.message.includes(expectedText) &&
            typeof post.props?.conversation_id === 'string'
        ));
        return matchedPost?.props?.conversation_id || '';
    }, {
        message: `bot result containing ${expectedText} did not persist with a conversation_id`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).not.toBe('');

    if (!matchedPost) {
        throw new Error(`Persisted bot result containing ${expectedText} was not found`);
    }
    return matchedPost;
}

async function reopenHistoryConversation(page: Page, aiPlugin: AIPlugin, title: string): Promise<void> {
    await page.reload({waitUntil: 'domcontentloaded'});
    await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 60000});
    await aiPlugin.openRHS();
    await aiPlugin.openChatHistory();

    const historyItem = aiPlugin.threadsListContainer.getByText(title, {exact: true});
    await expect(historyItem).toBeVisible({timeout: 30000});
    await historyItem.click();
}

async function expectSearchSourceUI(
    botPost: Locator,
    intendedSource: string,
    excludedSource: string,
): Promise<void> {
    const sourcesHeader = botPost.getByText('Sources', {exact: true});
    await expect(sourcesHeader).toBeVisible({timeout: 30000});
    await sourcesHeader.click();
    await expect(botPost.getByText(intendedSource, {exact: true})).toBeVisible({timeout: 30000});
    await expect(botPost.getByText(excludedSource, {exact: true})).toHaveCount(0);
}

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);

    await mmPage.login(mattermost.url(), username, password);
    await expect(aiPlugin.appBarIcon).toBeVisible({timeout: 30000});

    return {mmPage, aiPlugin};
}

test.describe('Channel composer slash commands', () => {
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

    test('/summarize-channel submits from the center composer and persists the summary', async ({page}) => {
        test.setTimeout(120000);

        const marker = `SLASH_SUMMARY_${Date.now()}`;
        const recentMarker = `${marker}_RECENT`;
        const threeDayMarker = `${marker}_THREE_DAYS`;
        const outsidePeriodMarker = `${marker}_OUTSIDE_PERIOD`;
        const recentSource = `${recentMarker} production deployment completed.`;
        const threeDaySource = `${threeDayMarker} release captain approved the rollout.`;
        const outsidePeriodSource = `${outsidePeriodMarker} obsolete rollback guidance.`;
        const summaryText = `${marker} summary: production deployment completed and the release captain approved the rollout.`;
        const filterTrapText = `${marker} FILTER_TRAP_OUTSIDE_PERIOD`;
        const command = '/summarize-channel --period 7d --bot second';
        const userClient = await mattermost.getClient(username, password);
        const townSquare = await getTownSquare(userClient);
        const recentPost = await userClient.createPost({
            channel_id: townSquare.id,
            message: recentSource,
        });
        const threeDayPost = await userClient.createPost({
            channel_id: townSquare.id,
            message: threeDaySource,
        });
        const outsidePeriodPost = await userClient.createPost({
            channel_id: townSquare.id,
            message: outsidePeriodSource,
        });
        const now = Date.now();
        await setPostCreateTimes([
            {id: recentPost.id, createAt: now},
            {id: threeDayPost.id, createAt: now - (3 * 24 * 60 * 60 * 1000)},
            {id: outsidePeriodPost.id, createAt: now - (8 * 24 * 60 * 60 * 1000)},
        ]);

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(summaryText), {
                bodyMatches: mainRequestPattern(
                    'Summarize the following posts from a Mattermost channel.',
                    recentMarker,
                    threeDayMarker,
                ),
                botPrefix: 'second',
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(filterTrapText), {
                bodyContains: outsidePeriodMarker,
                botPrefix: 'second',
                times: 1,
            }),
        ]);

        const {mmPage, aiPlugin} = await setupTestPage(page);
        await submitCenterComposerCommand(mmPage, command);

        const rhs = aiPlugin.getRhsContainer();
        await expect(rhs).toBeVisible({timeout: 30000});
        await aiPlugin.waitForBotResponse(summaryText);
        await expect(rhs.getByText(summaryText, {exact: true})).toBeVisible();
        await expectCommandNotPersisted(userClient, townSquare.id, command);

        await reopenHistoryConversation(page, aiPlugin, 'Summarize Channel');
        await expect(aiPlugin.getRhsContainer().getByText(summaryText, {exact: true})).toBeVisible({timeout: 30000});
    });

    test('/ask-channel returns indexed channel content with channel-scoped sources', async ({page}) => {
        test.setTimeout(120000);

        const marker = `SLASH_ASK_${Date.now()}`;
        const projectCode = `${marker}_PROJECT`;
        const intendedSecret = `${marker}_TOWN_SECRET`;
        const decoySecret = `${marker}_DECOY_SECRET`;
        const sourceText = `Project ${projectCode} launch credential is ${intendedSecret}; owner is regularuser.`;
        const decoyText = `Project ${projectCode} launch credential is ${decoySecret}; owner is regularuser.`;
        const query = `What launch credential is assigned to project ${projectCode}?`;
        const command = `/ask-channel ${query}`;
        const answerText = `${marker} answer: the launch credential is ${intendedSecret}.`;
        const trapAnswer = `${marker} DECOY_TRAP ${decoySecret}`;
        const userClient = await mattermost.getClient(username, password);
        const routes = mattermostAIPluginRoutes(mattermost.url());
        const teams = await userClient.getMyTeams();
        const townSquare = await getTownSquare(userClient);
        const decoyChannel = await userClient.createChannel({
            team_id: teams[0].id,
            name: `slash-ask-decoy-${Date.now()}`,
            display_name: `${marker} decoy`,
            type: 'O',
        });
        const sourcePost = await userClient.createPost({
            channel_id: townSquare.id,
            message: sourceText,
        });
        const decoyPost = await userClient.createPost({
            channel_id: decoyChannel.id,
            message: decoyText,
        });

        await waitForIndexedPost(routes, userClient, townSquare.id, query, sourcePost.id);
        await waitForIndexedPost(routes, userClient, decoyChannel.id, query, decoyPost.id);

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(answerText), {
                bodyMatches: mainRequestPattern(
                    'answering questions based on message history',
                    projectCode,
                    intendedSecret,
                ),
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(trapAnswer), {
                bodyContains: decoySecret,
                times: 1,
            }),
        ]);

        const {mmPage, aiPlugin} = await setupTestPage(page);
        await submitCenterComposerCommand(mmPage, command);

        const rhs = aiPlugin.getRhsContainer();
        await expect(rhs).toBeVisible({timeout: 30000});
        await aiPlugin.waitForBotResponse(answerText);
        await expectCommandNotPersisted(userClient, townSquare.id, command);

        const resultPost = await waitForPersistedBotResult(userClient, answerText);
        const botPost = rhs.locator('[data-testid="llm-bot-post"]').filter({hasText: answerText}).last();
        await expect(botPost).toBeVisible({timeout: 30000});
        const postText = botPost.locator('[data-testid="posttext"]').last();
        await expect(postText.getByText(answerText, {exact: true})).toBeVisible();
        await expectSearchSourceUI(botPost, sourceText, decoyText);

        const rawSearchSources = resultPost.props?.search_results;
        if (typeof rawSearchSources !== 'string') {
            throw new Error(`Result post ${resultPost.id} did not persist search_results`);
        }
        const persistedSources = JSON.parse(rawSearchSources) as SearchSource[];
        const persistedPostIDs = persistedSources.map((source) => source.postId);
        expect(persistedPostIDs).toContain(sourcePost.id);
        expect(persistedPostIDs).not.toContain(decoyPost.id);

        await reopenHistoryConversation(page, aiPlugin, 'Conversation with Agents');
        const restoredPost = aiPlugin.getRhsContainer().
            locator('[data-testid="llm-bot-post"]').
            filter({hasText: answerText}).
            last();
        await expect(restoredPost.getByText(answerText, {exact: true})).toBeVisible({timeout: 30000});
        await expectSearchSourceUI(restoredPost, sourceText, decoyText);
    });

    test('/ask-channel without a query shows validation and has no analysis side effect', async ({page}) => {
        test.setTimeout(120000);

        const command = '/ask-channel';
        const validationError = 'Please provide a search query after /ask-channel';
        const userClient = await mattermost.getClient(username, password);
        const townSquare = await getTownSquare(userClient);
        const {channel: botDM} = await getBotDM(userClient);
        const beforeDMPostIDs = new Set(
            getPosts(await userClient.getPosts(botDM.id, 0, 200)).map((post) => post.id),
        );
        const {mmPage, aiPlugin} = await setupTestPage(page);

        await submitCenterComposerCommand(mmPage, command);

        await expect(page.getByText(validationError, {exact: true})).toBeVisible({timeout: 30000});
        await expect(mmPage.postTextbox).toHaveValue(command);
        await expect(mmPage.postTextbox).toBeEditable();
        await expect(aiPlugin.getRhsContainer()).not.toBeVisible();
        await expectCommandNotPersisted(userClient, townSquare.id, command);
        const afterDMPostIDs = new Set(
            getPosts(await userClient.getPosts(botDM.id, 0, 200)).map((post) => post.id),
        );
        expect(afterDMPostIDs).toEqual(beforeDMPostIDs);
    });
});
