// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { test, expect, type Locator } from '@playwright/test';
import { Client4 } from '@mattermost/client';
import type { Post } from '@mattermost/types/posts';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
} from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';
const agentBotUsername = 'mock';

// Minimal WebVTT transcript parsed by subtitles.NewSubtitlesFromVTT on the server.
function sampleVTT(transcriptMarker: string): string {
    return `WEBVTT

00:00:00.000 --> 00:00:05.000
Alice: ${transcriptMarker}. We should ship the feature by Friday.

00:00:05.000 --> 00:00:10.000
Bob: Agreed, I'll handle the backend work.
`;
}

function exactTextResponse(text: string): string {
    const chunks = [
        {
            delta: { role: 'assistant', content: '' },
            finish_reason: null,
        },
        {
            delta: { content: text },
            finish_reason: null,
        },
        {
            delta: {},
            finish_reason: 'stop',
        },
    ].map((choice) => `data: ${JSON.stringify({
        id: 'chatcmpl-meeting-summary',
        object: 'chat.completion.chunk',
        created: 1708124577,
        model: 'gpt-mock',
        choices: [{ index: 0, ...choice }],
    })}`);
    chunks.push('data: [DONE]');
    return `${chunks.join('\n\n')}\n\n`;
}

function postbackPosts(thread: { order: string[]; posts: Record<string, Post> }, summaryText: string): Post[] {
    return thread.order.
        map((postID) => thread.posts[postID]).
        filter((post): post is Post => (
            Boolean(post) &&
            String(post.type) === 'custom_llm_postback' &&
            post.message === summaryText
        ));
}

function getRHSPostByID(rhs: Locator, postID: string): Locator {
    return rhs.locator(`#post_${postID}, #rhsPost_${postID}`);
}

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    test.setTimeout(180000);
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);

    // Bot accounts and tokens are needed to author the Calls posts.
    await mattermost.container.exec(['mmctl', '--local', 'config', 'set', 'ServiceSettings.EnableBotAccountCreation', 'true']);
    await mattermost.container.exec(['mmctl', '--local', 'config', 'set', 'ServiceSettings.EnableUserAccessTokens', 'true']);
});

test.afterAll(async () => {
    await openAIMock?.stop();
    await mattermost?.stop();
});

test.beforeEach(async () => {
    await openAIMock.resetMocks();
});

// Reproduces MM-69476: a meeting summary generated from a call transcription
// must stay visible in the DM thread. The summary post is a legacy bot post
// (no conversation_id), so before the fix it vanished once streaming ended.
test('meeting summary persists in the DM and posts back to the original Calls thread', async ({ page }) => {
    test.setTimeout(180000);

    const adminClient = await mattermost.getAdminClient();
    const userClient = await mattermost.getClient(username, password);
    const requester = await userClient.getMe();
    const agentBot = await userClient.getUserByUsername(agentBotUsername);
    const runMarker = Date.now().toString(36).toUpperCase();
    const transcriptMarker = `CALL_TRANSCRIPT_${runMarker}`;
    const callRootText = `Calls meeting ${runMarker}`;
    const transcriptPostText = `Call transcript ${runMarker}`;
    const summaryMarker = `MEETING_SUMMARY_${runMarker}`;
    const summaryText = `${summaryMarker} The team agreed to ship the feature on Friday and Bob will own the backend.`;
    const attribution = `This summary was created by ${agentBot.username} then edited and posted by @${requester.username}`;

    const fixture = await test.step('Create a Calls transcript thread', async () => {
        const callsBot = await adminClient.createBot({
            username: 'calls',
            display_name: 'Calls',
            description: 'e2e calls bot',
        });
        const callsToken = await adminClient.createUserAccessToken(callsBot.user_id, 'e2e meeting summary');
        if (!callsToken.token) {
            throw new Error('Calls bot token was not returned');
        }

        const teams = await userClient.getMyTeams();
        const team = teams[0];
        await adminClient.addToTeam(team.id, callsBot.user_id);
        const channels = await userClient.getMyChannels(team.id);
        const townSquare = channels.find((channel) => channel.name === 'town-square');
        if (!townSquare) {
            throw new Error('Town Square was not found');
        }
        await adminClient.addToChannel(callsBot.user_id, townSquare.id);

        const callsClient = new Client4();
        callsClient.setUrl(mattermost.url());
        callsClient.setToken(callsToken.token);

        const callRoot = await callsClient.createPost({
            channel_id: townSquare.id,
            message: callRootText,
        });
        const form = new FormData();
        form.append('channel_id', townSquare.id);
        form.append('files', new Blob([sampleVTT(transcriptMarker)], { type: 'text/vtt' }), `transcript-${runMarker}.vtt`);
        const upload = await callsClient.uploadFile(form);
        const fileId = upload.file_infos[0].id;
        const transcriptionPost = await callsClient.createPost({
            channel_id: townSquare.id,
            root_id: callRoot.id,
            message: transcriptPostText,
            file_ids: [fileId],
            props: { captions: [{ file_id: fileId }] },
        });

        expect(callRoot.user_id).toBe(callsBot.user_id);
        expect(transcriptionPost).toEqual(expect.objectContaining({
            user_id: callsBot.user_id,
            channel_id: townSquare.id,
            root_id: callRoot.id,
            file_ids: [fileId],
        }));
        expect(transcriptionPost.props?.captions).toEqual([{ file_id: fileId }]);
        return { team, townSquare, callRoot, transcriptionPost };
    });

    const summary = await test.step('Generate and persist the legacy DM summary', async () => {
        await openAIMock.addMocks([
            buildChatCompletionMockRule(exactTextResponse(summaryText), {
                bodyContains: transcriptMarker,
                times: 1,
            }),
        ]);

        const summarizeUrl = `${mattermost.url()}/plugins/mattermost-ai/post/${fixture.transcriptionPost.id}/summarize_transcription?botUsername=${agentBotUsername}`;
        const summarizeResp = await fetch(summarizeUrl, {
            method: 'POST',
            headers: { Authorization: `Bearer ${userClient.getToken()}` },
        });
        const summarizeBody = await summarizeResp.text();
        expect(summarizeResp.status, summarizeBody).toBe(200);
        const { postid: dmRootPostId, channelid: dmChannelId } = JSON.parse(summarizeBody);
        expect(dmRootPostId).toBeTruthy();

        let persistedSummary: Post | undefined;
        await expect.poll(async () => {
            const postsResp = await userClient.getPosts(dmChannelId, 0, 200);
            persistedSummary = Object.values(postsResp.posts).find((post) => (
                post.root_id === dmRootPostId &&
                String(post.type) === 'custom_llmbot' &&
                post.message === summaryText
            ));
            return persistedSummary?.id ?? '';
        }, {
            message: 'summary reply was never persisted with exact content',
            timeout: 60000,
            intervals: [250, 500, 1000],
        }).not.toBe('');

        if (!persistedSummary) {
            throw new Error('Persisted summary was not returned by the DM API');
        }
        expect(persistedSummary.props?.conversation_id ?? '').toBe('');
        expect(persistedSummary.props?.llm_requester_user_id).toBe(requester.id);
        return { dmRootPostId, post: persistedSummary };
    });

    const persistedPostback = await test.step('Render the DM summary and post it back', async () => {
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);

        await page.goto(`${mattermost.url()}/${fixture.team.name}/channels/${fixture.townSquare.name}`);
        await expect(page.getByTestId('channel_view')).toBeVisible({ timeout: 60000 });
        await expect(
            page.getByTestId('channel_view').locator(`#post_${fixture.callRoot.id}`),
        ).toBeVisible({ timeout: 30000 });

        const dmLink = page.locator(`a[href="/${fixture.team.name}/messages/@${agentBotUsername}"]`);
        await expect(dmLink).toBeVisible();
        await dmLink.click();
        await page.waitForURL(new RegExp(`/${fixture.team.name}/messages/@${agentBotUsername}$`));
        await expect(page.getByTestId('channel_view')).toBeVisible({ timeout: 60000 });

        const dmRoot = page.getByTestId('channel_view').locator(`#post_${summary.dmRootPostId}`);
        await expect(dmRoot).toBeVisible({ timeout: 30000 });
        const openDMThreadButton = dmRoot.getByRole('button', { name: /\d+ repl/i });
        await expect(openDMThreadButton).toBeVisible();
        await openDMThreadButton.click();

        const dmThread = page.locator('#rhsContainer');
        await expect(dmThread).toBeVisible({ timeout: 30000 });
        const summaryPost = getRHSPostByID(dmThread, summary.post.id);
        await expect(summaryPost).toBeVisible({ timeout: 30000 });
        await expect(summaryPost.getByTestId('posttext')).toHaveText(summaryText);
        await expect(summaryPost.getByTestId('llm-bot-post-summary-help')).toBeVisible();

        const postSummaryButton = summaryPost.getByTestId('llm-bot-post-summary');
        await expect(postSummaryButton).toBeVisible();
        const postbackResponsePromise = page.waitForResponse((response) => (
            response.request().method() === 'POST' &&
            response.url().endsWith(`/plugins/mattermost-ai/post/${summary.post.id}/postback_summary`)
        ));
        await postSummaryButton.click();
        const postbackResponse = await postbackResponsePromise;
        const postbackBody = await postbackResponse.text();
        expect(postbackResponse.status(), postbackBody).toBe(200);
        expect(JSON.parse(postbackBody)).toEqual({
            rootid: fixture.callRoot.id,
            channelid: fixture.townSquare.id,
        });

        let postback: Post | undefined;
        await expect.poll(async () => {
            const matchingPosts = postbackPosts(
                await userClient.getPostThread(fixture.callRoot.id),
                summaryText,
            );
            postback = matchingPosts[0];
            return matchingPosts.length;
        }, {
            message: 'expected exactly one persisted postback in the original Calls thread',
            timeout: 30000,
            intervals: [250, 500, 1000],
        }).toBe(1);

        if (!postback) {
            throw new Error('Postback was not returned by the Calls thread API');
        }
        expect(postback).toEqual(expect.objectContaining({
            user_id: agentBot.id,
            channel_id: fixture.townSquare.id,
            root_id: fixture.callRoot.id,
            message: summaryText,
            type: 'custom_llm_postback',
        }));
        expect(postback.props?.userid).toBe(requester.id);

        const originalThread = page.locator('#rhsContainer');
        const renderedPostback = getRHSPostByID(originalThread, postback.id);
        await expect(renderedPostback).toBeVisible({ timeout: 30000 });
        await expect(renderedPostback.getByTestId('posttext')).toHaveText(summaryText);
        await expect(renderedPostback.getByText(attribution, { exact: true })).toBeVisible();
        return postback;
    });

    await test.step('Reload and reopen the original Calls thread', async () => {
        await page.reload({ waitUntil: 'domcontentloaded' });
        await expect(page.getByTestId('channel_view')).toBeVisible({ timeout: 60000 });
        await page.goto(`${mattermost.url()}/${fixture.team.name}/channels/${fixture.townSquare.name}`);
        await expect(page.getByTestId('channel_view')).toBeVisible({ timeout: 60000 });

        const reloadedCallRoot = page.getByTestId('channel_view').locator(`#post_${fixture.callRoot.id}`);
        await expect(reloadedCallRoot).toBeVisible({ timeout: 30000 });
        const openThreadButton = reloadedCallRoot.getByRole('button', { name: /\d+ repl/i });
        await expect(openThreadButton).toBeVisible();
        await openThreadButton.click();

        const reopenedThread = page.locator('#rhsContainer');
        await expect(reopenedThread).toBeVisible({ timeout: 30000 });
        const restoredPostback = getRHSPostByID(reopenedThread, persistedPostback.id);
        await expect(restoredPostback).toBeVisible({ timeout: 30000 });
        await expect(restoredPostback.getByTestId('posttext')).toHaveText(summaryText);
        await expect(restoredPostback.getByText(attribution, { exact: true })).toBeVisible();

        const finalPostbacks = postbackPosts(
            await userClient.getPostThread(fixture.callRoot.id),
            summaryText,
        );
        expect(finalPostbacks).toHaveLength(1);
    });
});
