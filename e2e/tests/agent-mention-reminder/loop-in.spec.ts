// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, test, type Locator, type Page, type Route} from '@playwright/test';
import type {Client4} from '@mattermost/client';
import type {Post} from '@mattermost/types/posts';

import {AgentAPIHelper} from 'helpers/agent-api';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {
    buildChatCompletionMockRule,
    buildTextResponse,
    OpenAIMockContainer,
    RunOpenAIMocks,
} from 'helpers/openai-mock';
import RunContainer from 'helpers/plugincontainer';

const requesterUsername = 'regularuser';
const requesterPassword = 'regularuser';
const onlookerUsername = 'seconduser';
const onlookerPassword = 'seconduser';
const botUsername = 'mock';
const botDisplayName = 'Mock Bot';
const channelAccessNone = 3;

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

function escapeRegExp(value: string): string {
    return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function currentUserMessagePattern(message: string): string {
    return `(?s)"role"\\s*:\\s*"user"\\s*,\\s*"content"\\s*:\\s*"${escapeRegExp(message)}"\\s*}\\s*]\\s*}`;
}

async function setTestPreferences(client: Client4): Promise<void> {
    const user = await client.getMe();
    await client.savePreferences(user.id, [
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

async function getTownSquareChannelID(client: Client4): Promise<string> {
    const teams = await client.getMyTeams();
    const channels = await client.getMyChannels(teams[0].id);
    const townSquare = channels.find((channel) => channel.name === 'town-square');
    if (!townSquare) {
        throw new Error('Could not find town-square channel');
    }
    return townSquare.id;
}

async function waitForChannelPost(
    client: Client4,
    channelID: string,
    predicate: (post: Post) => boolean,
): Promise<Post> {
    let matchedPost: Post | undefined;
    await expect.poll(async () => {
        const posts = await client.getPosts(channelID, 0, 200);
        matchedPost = Object.values(posts.posts).find(predicate);
        return matchedPost?.id ?? '';
    }, {
        message: 'expected channel post was not persisted',
        timeout: 60000,
        intervals: [250, 500, 1000],
    }).not.toBe('');

    if (!matchedPost) {
        throw new Error('Expected channel post was not found');
    }
    return matchedPost;
}

async function waitForThreadPost(
    client: Client4,
    rootPostID: string,
    predicate: (post: Post) => boolean,
): Promise<Post> {
    let matchedPost: Post | undefined;
    await expect.poll(async () => {
        const thread = await client.getPostThread(rootPostID);
        matchedPost = thread.order.
            map((postID) => thread.posts[postID]).
            find((post): post is Post => Boolean(post) && predicate(post));
        return matchedPost?.id ?? '';
    }, {
        message: 'expected thread post was not persisted',
        timeout: 60000,
        intervals: [250, 500, 1000],
    }).not.toBe('');

    if (!matchedPost) {
        throw new Error('Expected thread post was not found');
    }
    return matchedPost;
}

function getChannelPost(page: Page, message: string): Locator {
    return page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, {exact: true}),
    }).last();
}

async function navigateToTownSquare(page: Page): Promise<void> {
    await page.goto(`${mattermost.url()}/test/channels/town-square`);
    await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 60000});
}

async function openThread(page: Page, rootPost: Locator, firstAgentReply: string): Promise<Locator> {
    const replyIndicator = rootPost.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({timeout: 60000});
    await replyIndicator.click();

    const rhs = page.locator('#rhsContainer');
    await expect(rhs).toBeVisible({timeout: 10000});
    await expect(rhs.getByText(firstAgentReply, {exact: true})).toBeVisible({timeout: 30000});
    return rhs;
}

async function sendThreadReply(rhs: Locator, message: string): Promise<void> {
    const replyTextbox = rhs.locator('textarea').first();
    await expect(replyTextbox).toBeVisible({timeout: 10000});
    await replyTextbox.fill(message);
    await rhs.getByTestId('SendMessageButton').click();
}

test.describe('Agent mention reminder and one-click loop-in', () => {
    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);

        const onlookerClient = await mattermost.getClient(onlookerUsername, onlookerPassword);
        await setTestPreferences(onlookerClient);
    });

    test.beforeEach(async () => {
        await openAIMock.resetMocks();
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('reminds only the requester and loops the unchanged reply into the agent', async ({browser}) => {
        test.setTimeout(180000);

        const marker = `LOOP_IN_${Date.now()}`;
        const initialPrompt = `Start a public thread ${marker}`;
        const initialChannelMessage = `@${botUsername} ${initialPrompt}`;
        const firstAgentReply = `First agent reply ${marker}`;
        const unmentionedReply = `Continue from this unchanged reply ${marker}`;
        const secondAgentReply = `Second looped-in reply ${marker}`;

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(`Loop in ${marker}`), {
                bodyContains: 'Write a short title for the following request.',
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(secondAgentReply), {
                bodyMatches: currentUserMessagePattern(`@${botUsername} ${unmentionedReply}`),
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(firstAgentReply), {
                bodyMatches: currentUserMessagePattern(initialChannelMessage),
                times: 1,
            }),
        ]);

        const requesterContext = await browser.newContext();
        const onlookerContext = await browser.newContext();
        const requesterPage = await requesterContext.newPage();
        const onlookerPage = await onlookerContext.newPage();

        try {
            const requesterMM = new MattermostPage(requesterPage);
            const onlookerMM = new MattermostPage(onlookerPage);
            await requesterMM.login(mattermost.url(), requesterUsername, requesterPassword);
            await onlookerMM.login(mattermost.url(), onlookerUsername, onlookerPassword);
            await navigateToTownSquare(requesterPage);
            await navigateToTownSquare(onlookerPage);

            const requesterClient = await mattermost.getClient(requesterUsername, requesterPassword);
            const requester = await requesterClient.getMe();
            const bot = await requesterClient.getUserByUsername(botUsername);
            const channelID = await getTownSquareChannelID(requesterClient);

            await requesterMM.mentionBot(botUsername, initialPrompt);
            const rootPost = await waitForChannelPost(
                requesterClient,
                channelID,
                (post) => post.user_id === requester.id && post.message === initialChannelMessage,
            );
            const firstReply = await waitForThreadPost(
                requesterClient,
                rootPost.id,
                (post) => post.user_id === bot.id && post.message.includes(firstAgentReply),
            );

            const requesterRootPost = getChannelPost(requesterPage, initialChannelMessage);
            const onlookerRootPost = getChannelPost(onlookerPage, initialChannelMessage);
            await expect(requesterRootPost).toBeVisible({timeout: 30000});
            await expect(onlookerRootPost).toBeVisible({timeout: 30000});
            const requesterRhs = await openThread(requesterPage, requesterRootPost, firstAgentReply);
            const onlookerRhs = await openThread(onlookerPage, onlookerRootPost, firstAgentReply);

            await sendThreadReply(requesterRhs, unmentionedReply);
            const originalReply = await waitForThreadPost(
                requesterClient,
                rootPost.id,
                (post) => post.user_id === requester.id && post.message === unmentionedReply,
            );
            await expect(onlookerRhs.getByText(unmentionedReply, {exact: true})).
                toBeVisible({timeout: 30000});

            const loopInLink = requesterRhs.getByRole('link', {
                name: `click here to loop in @${botDisplayName}`,
                exact: true,
            });
            await expect(loopInLink).toBeVisible({timeout: 30000});
            await expect(loopInLink).toHaveText(`click here to loop in @${botDisplayName}`);
            await expect(loopInLink.locator('..')).toContainText('To respond to an agent you must @mention them.');
            await expect(onlookerRhs.getByRole('link', {name: /click here to loop in/i})).toHaveCount(0);

            const loopInRoutePattern =
                `**/plugins/mattermost-ai/post/${originalReply.id}/loop_in_agent**`;
            let loopInPostRequestCount = 0;
            let releaseLoopInRequest = () => {};
            let markLoopInRequestHeld = () => {};
            const loopInRequestReleased = new Promise<void>((resolve) => {
                releaseLoopInRequest = resolve;
            });
            const loopInRequestHeld = new Promise<void>((resolve) => {
                markLoopInRequestHeld = resolve;
            });
            const holdLoopInRequest = async (route: Route) => {
                if (route.request().method() !== 'POST') {
                    await route.continue();
                    return;
                }

                loopInPostRequestCount++;
                markLoopInRequestHeld();
                await loopInRequestReleased;
                await route.continue();
            };
            await requesterPage.route(loopInRoutePattern, holdLoopInRequest);
            try {
                await loopInLink.click();
                await loopInRequestHeld;
                await expect(loopInLink).toHaveAttribute('aria-disabled', 'true');

                await loopInLink.press('Enter');
                await requesterPage.evaluate(
                    () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
                );
                expect(loopInPostRequestCount).toBe(1);

                releaseLoopInRequest();
                await expect(requesterRhs.getByText(`Looped in @${botDisplayName}.`, {exact: true})).
                    toBeVisible({timeout: 60000});
                expect(loopInPostRequestCount).toBe(1);
            } finally {
                releaseLoopInRequest();
                await requesterPage.unroute(loopInRoutePattern, holdLoopInRequest);
            }
            await expect(requesterRhs.getByText(secondAgentReply, {exact: true})).
                toBeVisible({timeout: 60000});

            const secondReply = await waitForThreadPost(
                requesterClient,
                rootPost.id,
                (post) => post.user_id === bot.id && post.message.includes(secondAgentReply),
            );
            expect(secondReply.id).not.toBe(firstReply.id);

            const finalThread = await requesterClient.getPostThread(rootPost.id);
            expect(finalThread.posts[originalReply.id].message).toBe(unmentionedReply);
            const requesterThreadReplies = finalThread.order.
                map((postID) => finalThread.posts[postID]).
                filter((post): post is Post => Boolean(post) && post.root_id === rootPost.id && post.user_id === requester.id);
            expect(requesterThreadReplies).toEqual([
                expect.objectContaining({
                    id: originalReply.id,
                    message: unmentionedReply,
                }),
            ]);
            expect(requesterThreadReplies.some((post) => post.message.includes(`@${botUsername}`))).toBe(false);
            const agentThreadReplies = finalThread.order.
                map((postID) => finalThread.posts[postID]).
                filter((post): post is Post => Boolean(post) && post.root_id === rootPost.id && post.user_id === bot.id);
            expect(agentThreadReplies.map((post) => post.id)).toEqual([firstReply.id, secondReply.id]);
        } finally {
            await requesterContext.close();
            await onlookerContext.close();
        }
    });

    test('does not remind after an explicit agent mention', async ({page}) => {
        test.setTimeout(180000);

        const marker = `EXPLICIT_MENTION_${Date.now()}`;
        const initialPrompt = `Start the explicit mention thread ${marker}`;
        const initialChannelMessage = `@${botUsername} ${initialPrompt}`;
        const firstAgentReply = `Explicit branch first reply ${marker}`;
        const explicitReply = `@${botUsername} Continue explicitly ${marker}`;
        const secondAgentReply = `Explicit branch second reply ${marker}`;

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(`Explicit mention ${marker}`), {
                bodyContains: 'Write a short title for the following request.',
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(secondAgentReply), {
                bodyMatches: currentUserMessagePattern(explicitReply),
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(firstAgentReply), {
                bodyMatches: currentUserMessagePattern(initialChannelMessage),
                times: 1,
            }),
        ]);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), requesterUsername, requesterPassword);
        await navigateToTownSquare(page);

        const requesterClient = await mattermost.getClient(requesterUsername, requesterPassword);
        const requester = await requesterClient.getMe();
        const bot = await requesterClient.getUserByUsername(botUsername);
        const channelID = await getTownSquareChannelID(requesterClient);

        await mmPage.mentionBot(botUsername, initialPrompt);
        const rootPost = await waitForChannelPost(
            requesterClient,
            channelID,
            (post) => post.user_id === requester.id && post.message === initialChannelMessage,
        );
        const firstReply = await waitForThreadPost(
            requesterClient,
            rootPost.id,
            (post) => post.user_id === bot.id && post.message.includes(firstAgentReply),
        );

        const rootPostLocator = getChannelPost(page, initialChannelMessage);
        await expect(rootPostLocator).toBeVisible({timeout: 30000});
        const rhs = await openThread(page, rootPostLocator, firstAgentReply);
        await sendThreadReply(rhs, explicitReply);

        const persistedExplicitReply = await waitForThreadPost(
            requesterClient,
            rootPost.id,
            (post) => post.user_id === requester.id && post.message === explicitReply,
        );
        const threadAfterExplicitReply = await requesterClient.getPostThread(rootPost.id);
        const explicitReplyIndex = threadAfterExplicitReply.order.indexOf(persistedExplicitReply.id);
        expect(explicitReplyIndex).toBeGreaterThan(0);
        expect(threadAfterExplicitReply.order[explicitReplyIndex - 1]).toBe(firstReply.id);

        await waitForThreadPost(
            requesterClient,
            rootPost.id,
            (post) => post.user_id === bot.id && post.message.includes(secondAgentReply),
        );
        await expect(rhs.getByText(secondAgentReply, {exact: true})).toBeVisible({timeout: 60000});
        await expect(rhs.getByRole('link', {name: /click here to loop in/i})).toHaveCount(0);
        await expect(rhs.getByText('To respond to an agent you must @mention them.', {exact: false})).toHaveCount(0);
    });

    test('shows a real loop-in failure and retries successfully after access is restored', async ({page}) => {
        test.setTimeout(180000);

        const marker = `LOOP_IN_RETRY_${Date.now()}`;
        const initialPrompt = `Start a retry thread ${marker}`;
        const initialChannelMessage = `@${botUsername} ${initialPrompt}`;
        const firstAgentReply = `Retry branch first reply ${marker}`;
        const unmentionedReply = `Retry this unchanged reply ${marker}`;
        const secondAgentReply = `Retry branch looped-in reply ${marker}`;

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(`Loop in retry ${marker}`), {
                bodyContains: 'Write a short title for the following request.',
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(secondAgentReply), {
                bodyMatches: currentUserMessagePattern(`@${botUsername} ${unmentionedReply}`),
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(firstAgentReply), {
                bodyMatches: currentUserMessagePattern(initialChannelMessage),
                times: 1,
            }),
        ]);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), requesterUsername, requesterPassword);
        await navigateToTownSquare(page);

        const requesterClient = await mattermost.getClient(requesterUsername, requesterPassword);
        const requester = await requesterClient.getMe();
        const bot = await requesterClient.getUserByUsername(botUsername);
        const channelID = await getTownSquareChannelID(requesterClient);

        await mmPage.mentionBot(botUsername, initialPrompt);
        const rootPost = await waitForChannelPost(
            requesterClient,
            channelID,
            (post) => post.user_id === requester.id && post.message === initialChannelMessage,
        );
        const firstReply = await waitForThreadPost(
            requesterClient,
            rootPost.id,
            (post) => post.user_id === bot.id && post.message.includes(firstAgentReply),
        );

        const rootPostLocator = getChannelPost(page, initialChannelMessage);
        await expect(rootPostLocator).toBeVisible({timeout: 30000});
        const rhs = await openThread(page, rootPostLocator, firstAgentReply);
        await sendThreadReply(rhs, unmentionedReply);
        const originalReply = await waitForThreadPost(
            requesterClient,
            rootPost.id,
            (post) => post.user_id === requester.id && post.message === unmentionedReply,
        );

        const loopInLink = rhs.getByRole('link', {
            name: `click here to loop in @${botDisplayName}`,
            exact: true,
        });
        await expect(loopInLink).toBeVisible({timeout: 30000});

        const adminClient = await mattermost.getAdminClient();
        const agentAPI = new AgentAPIHelper(mattermost.url());
        const agents = await agentAPI.getAgents(adminClient.getToken());
        const mockAgent = agents.find((agent) => agent.name === botUsername);
        if (!mockAgent) {
            throw new Error(`Could not find @${botUsername} through the agent admin API`);
        }

        const originalChannelAccessLevel = mockAgent.channelAccessLevel;
        const originalChannelIDs = mockAgent.channelIDs ?? [];
        let agentAccessRestricted = false;
        const waitForChannelAccessLevel = async (expectedLevel: number) => {
            await expect.poll(
                async () => (await agentAPI.getAgent(adminClient.getToken(), mockAgent.id)).channelAccessLevel,
                {
                    message: `expected @${botUsername} channel access level ${expectedLevel}`,
                    timeout: 30000,
                    intervals: [100, 250, 500],
                },
            ).toBe(expectedLevel);
        };

        try {
            await agentAPI.updateAgent(adminClient.getToken(), mockAgent.id, {
                channelAccessLevel: channelAccessNone,
                channelIDs: [],
            });
            agentAccessRestricted = true;
            await waitForChannelAccessLevel(channelAccessNone);

            await loopInLink.click();
            await expect(rhs.getByText(
                `Failed to loop in @${botDisplayName}. Please try again.`,
                {exact: true},
            )).toBeVisible({timeout: 30000});
            await expect(loopInLink).toHaveAttribute('aria-disabled', 'false');

            await agentAPI.updateAgent(adminClient.getToken(), mockAgent.id, {
                channelAccessLevel: originalChannelAccessLevel,
                channelIDs: originalChannelIDs,
            });
            agentAccessRestricted = false;
            await waitForChannelAccessLevel(originalChannelAccessLevel);

            await loopInLink.click();
            await expect(rhs.getByText(`Looped in @${botDisplayName}.`, {exact: true})).
                toBeVisible({timeout: 60000});
            await expect(rhs.getByText(secondAgentReply, {exact: true})).
                toBeVisible({timeout: 60000});
        } finally {
            if (agentAccessRestricted) {
                await agentAPI.updateAgent(adminClient.getToken(), mockAgent.id, {
                    channelAccessLevel: originalChannelAccessLevel,
                    channelIDs: originalChannelIDs,
                });
                await waitForChannelAccessLevel(originalChannelAccessLevel);
            }
        }

        const secondReply = await waitForThreadPost(
            requesterClient,
            rootPost.id,
            (post) => post.user_id === bot.id && post.message.includes(secondAgentReply),
        );
        expect(secondReply.id).not.toBe(firstReply.id);

        const finalThread = await requesterClient.getPostThread(rootPost.id);
        expect(finalThread.posts[originalReply.id].message).toBe(unmentionedReply);
        const requesterThreadReplies = finalThread.order.
            map((postID) => finalThread.posts[postID]).
            filter((post): post is Post => Boolean(post) && post.root_id === rootPost.id && post.user_id === requester.id);
        expect(requesterThreadReplies).toEqual([
            expect.objectContaining({
                id: originalReply.id,
                message: unmentionedReply,
            }),
        ]);
        expect(requesterThreadReplies.some((post) => post.message.includes(`@${botUsername}`))).toBe(false);
    });
});
