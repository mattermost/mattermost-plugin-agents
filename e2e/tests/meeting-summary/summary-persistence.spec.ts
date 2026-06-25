import { test, expect } from '@playwright/test';
import { Client4 } from '@mattermost/client';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { LLMBotPostHelper } from 'helpers/llmbot-post';
import { OpenAIMockContainer, RunOpenAIMocks, buildTextResponse } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';
const agentBotUsername = 'mock';

// Unique marker so the assertion can't accidentally match other UI text.
const SUMMARY_MARKER = 'MEETINGSUMMARYMARKER42';
const SUMMARY_TEXT = `${SUMMARY_MARKER} The team agreed to ship the feature on Friday and Bob will own the backend.`;

// Minimal WebVTT transcript parsed by subtitles.NewSubtitlesFromVTT on the server.
const SAMPLE_VTT = `WEBVTT

00:00:00.000 --> 00:00:05.000
Alice: We should ship the feature by Friday.

00:00:05.000 --> 00:00:10.000
Bob: Agreed, I'll handle the backend work.
`;

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    test.setTimeout(180000);
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);

    // Bot accounts + tokens are needed to author the calls transcription post,
    // and inline replies (CRT off) keep the streamed summary in the center
    // channel where the helper can find it by post id.
    await mattermost.container.exec(['mmctl', '--local', 'config', 'set', 'ServiceSettings.EnableBotAccountCreation', 'true']);
    await mattermost.container.exec(['mmctl', '--local', 'config', 'set', 'ServiceSettings.EnableUserAccessTokens', 'true']);
    await mattermost.container.exec(['mmctl', '--local', 'config', 'set', 'ServiceSettings.CollapsedThreads', 'disabled']);
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
test('meeting summary stays visible in the DM after it is generated', async ({ page }) => {
    test.setTimeout(120000);

    const adminClient = await mattermost.getAdminClient();
    const userClient = await mattermost.getClient(username, password);

    // 1) A "calls" bot must author the transcription post (server validation).
    const callsBot = await adminClient.createBot({
        username: 'calls',
        display_name: 'Calls',
        description: 'e2e calls bot',
    });
    const callsToken = await adminClient.createUserAccessToken(callsBot.user_id, 'e2e meeting summary');

    const teams = await userClient.getMyTeams();
    const team = teams[0];
    await adminClient.addToTeam(team.id, callsBot.user_id);
    const channels = await userClient.getMyChannels(team.id);
    const townSquare = channels.find((c) => c.name === 'town-square');
    expect(townSquare).toBeTruthy();
    await adminClient.addToChannel(callsBot.user_id, townSquare!.id);

    const callsClient = new Client4();
    callsClient.setUrl(mattermost.url());
    callsClient.setToken(callsToken.token);

    // 2) Upload the VTT transcript and attach it to the calls transcription post.
    const form = new FormData();
    form.append('channel_id', townSquare!.id);
    form.append('files', new Blob([SAMPLE_VTT], { type: 'text/vtt' }), 'transcript.vtt');
    const upload = await callsClient.uploadFile(form);
    const fileId = upload.file_infos[0].id;

    const transcriptionPost = await callsClient.createPost({
        channel_id: townSquare!.id,
        message: 'Call transcription',
        file_ids: [fileId],
        props: { captions: [{ file_id: fileId }] },
    });

    // 3) The agent's summary completion is served by the mock LLM.
    await openAIMock.addCompletionMock(buildTextResponse(SUMMARY_TEXT));

    // 4) Trigger "Create meeting summary" exactly as the Calls button does.
    const summarizeUrl = `${mattermost.url()}/plugins/mattermost-ai/post/${transcriptionPost.id}/summarize_transcription?botUsername=${agentBotUsername}`;
    const summarizeResp = await fetch(summarizeUrl, {
        method: 'POST',
        headers: { Authorization: `Bearer ${userClient.getToken()}` },
    });
    expect(summarizeResp.status, await summarizeResp.text()).toBe(200);
    const { postid: dmRootPostId, channelid: dmChannelId } = await summarizeResp.json();
    expect(dmRootPostId).toBeTruthy();

    // 5) Wait for the streamed summary reply to be persisted server-side.
    let summaryPostId = '';
    await expect.poll(async () => {
        const postsResp = await userClient.getPosts(dmChannelId, 0, 200);
        const summary = Object.values(postsResp.posts).find(
            (p) => p.root_id === dmRootPostId && p.type === 'custom_llmbot',
        );
        if (summary && summary.message.includes(SUMMARY_MARKER)) {
            summaryPostId = summary.id;
            return true;
        }
        return false;
    }, { timeout: 60000, message: 'summary reply was never persisted with content' }).toBe(true);

    // 6) Open the DM and confirm the summary is actually rendered. Before the
    // fix the post.message was persisted but the UI dropped it once streaming
    // finished, leaving only the follow-up prompt.
    const mmPage = new MattermostPage(page);
    const llmBotHelper = new LLMBotPostHelper(page);
    await mmPage.login(mattermost.url(), username, password);
    await mmPage.createAndNavigateToDMWithBot(mattermost, username, password, agentBotUsername);

    const summaryPost = llmBotHelper.getLLMBotPost(summaryPostId);
    await expect(summaryPost).toBeVisible({ timeout: 30000 });
    await llmBotHelper.waitForStreamingComplete(60000);

    // The follow-up prompt renders independently of the summary content, so it
    // is the prompt — not the summary — that confirmed the bug originally.
    await expect(page.getByText('Would you like to post this summary')).toBeVisible({ timeout: 10000 });

    const postText = llmBotHelper.getPostText(summaryPostId);
    await expect(postText).toContainText(SUMMARY_MARKER, { timeout: 10000 });
});
