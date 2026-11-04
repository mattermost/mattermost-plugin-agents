// spec: .planning/phase-4/PLAN.md — per-channel agent auto-reply (API -> trigger -> streamed reply)
// seed: tests/seed.spec.ts
//
// Auto-reply is configured via the REST API rather than the channel-settings tab UI.
// The tab component is covered by webapp unit tests; this spec covers the
// end-to-end contract. On servers < 11.10 the tab does not register
// (registerChannelSettingsTab is missing); REST endpoints remain available.

import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import type { Client4 } from '@mattermost/client';
import type { Post } from '@mattermost/types/posts';
import type { UserProfile } from '@mattermost/types/users';
import type { Channel } from '@mattermost/types/channels';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { mattermostAIAdminConfigApiFromClient, mattermostAIPluginRoutes } from 'helpers/plugin-http';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    responseTest,
    responseTestText,
} from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

const test = containerTest(async (own) => {
    const mattermost = own(await RunContainer());
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async ({ containers }) => {
    ({ mattermost, openAIMock } = containers);
    await setAmbientAutoReplyEnabled(true);
});

test.beforeEach(async () => {
    await openAIMock.resetMocks();
    await openAIMock.addCompletionMock(responseTest);
});

/**
 * Log in regularuser via the API and create a fresh public channel for the test.
 * Never reuse town-square: an auto-reply setting there would fire on every other
 * post the file makes and cross-contaminate assertions.
 *
 * regularuser creates the channel, so PUTs to the autoreply endpoint run as a
 * regular member and implicitly exercise the phase-2 channel-management
 * permission path (the Mattermost default scheme grants
 * manage_public_channel_properties to channel members). If the PUT ever 403s
 * under a future default-scheme change, fall back to mattermost.getAdminClient()
 * for the PUT only.
 */
async function setupClientAndChannel(testTag: string): Promise<{
    client: Client4;
    botUser: UserProfile;
    channel: Channel;
}> {
    const client = await mattermost.getClient(username, password);
    const teams = await client.getMyTeams();
    const team = teams[0];
    // bot_id in the autoreply payload is the bot's Mattermost user id
    // (bots.GetBotByID matches on it), not the plugin-config id.
    const botUser = await client.getUserByUsername('mock');
    const channel = await client.createChannel({
        team_id: team.id,
        name: `autoreply-${testTag}-${Date.now()}`,
        display_name: `Autoreply ${testTag}`,
        type: 'O',
    } as Channel);
    return { client, botUser, channel };
}

async function setAmbientAutoReplyEnabled(enabled: boolean): Promise<void> {
    const adminClient = await mattermost.getAdminClient();
    await mattermostAIAdminConfigApiFromClient(adminClient, mattermost.url()).patch({enableAmbientAutoReply: enabled}, {settleMs: 2000});
}

async function putAutoReply(
    client: Client4,
    channelId: string,
    botId: string,
    mode: string,
    extras?: { instructions?: string },
): Promise<void> {
    const routes = mattermostAIPluginRoutes(mattermost.url());
    const payload = { bot_id: botId, mode, ...extras };
    await routes.putJson(`channel/${channelId}/autoreply`, client.getToken(), payload);
    // Round-trip sanity check: GET must return what was stored.
    const setting = await routes.getJson(`channel/${channelId}/autoreply`, client.getToken());
    expect(setting).toMatchObject(payload);
}

// Distinctive substring from prompts/ambient_classifier_system.tmpl. Smocker matches
// the classifier completion (body contains this) separately from the reply completion.
const ambientClassifierPrompt = 'You classify whether an agent should automatically reply in a Mattermost channel.';

async function mockAmbientClassifier(shouldReply: boolean): Promise<void> {
    const classifierRule = buildChatCompletionMockRule(buildTextResponse(JSON.stringify({should_reply: shouldReply})), {
        bodyContains: ambientClassifierPrompt,
        times: 1,
    });
    const catchAllRule = buildChatCompletionMockRule(responseTest);
    // Smocker last-registered = highest priority. Catch-all must come first so the
    // classifier JSON rule actually matches the classification request.
    await openAIMock.addMocks([catchAllRule, classifierRule]);
}

/** Fail unless the classifier mock answered and its false verdict did not fall through to the catch-all SSE. */
async function expectClassifierDeclined(): Promise<void> {
    const mocks = await openAIMock.getMocks();
    const classifier = mocks.find((m) => m.request?.body?.value === ambientClassifierPrompt);
    const catchAll = mocks.find((m) => !m.request?.body);
    expect(classifier?.state?.times_count ?? 0, 'classifier Smocker rule must have been matched').toBeGreaterThan(0);
    expect(catchAll?.state?.times_count ?? 0, 'classifier false must not fall through to the catch-all SSE').toBe(0);
}

/** Poll the channel posts API until the given user's post with this exact message appears. */
async function waitForUserPost(client: Client4, channelId: string, userId: string, message: string): Promise<Post> {
    let match: Post | undefined;
    await expect.poll(async () => {
        const postsResponse = await client.getPosts(channelId, 0, 200);
        match = Object.values(postsResponse.posts || {}).find(
            (p) => p.user_id === userId && p.message === message,
        );
        return Boolean(match);
    }, { timeout: 15000, intervals: [500, 1000, 2000] }).toBe(true);
    return match!;
}

/**
 * expectNoBotDmReplyFromApi tolerates 5 s of clock skew (create_at >= sinceMs - 5000,
 * inclusive), so a bot reply that finished streaming moments ago would be flagged as a
 * "new" bot post if sinceMs is taken immediately after it. Wait until the skew window
 * around the earlier reply has passed (with a 1 s margin — setTimeout can fire a hair
 * early) before taking a fresh sinceMs for a time-bounded assertion.
 *
 * The wait normally completes within ~6 s of the reply's creation; it can only run
 * longer if the server clock is ahead of the runner, so cap it to make such a clock
 * fault fail legibly instead of eating the whole test timeout.
 */
async function waitOutSkewWindow(earlierBotPost: Post): Promise<void> {
    await expect.poll(() => Date.now() > earlierBotPost.create_at + 6000, {
        timeout: 30000,
        intervals: [250],
        message: `waitOutSkewWindow: still inside the skew window after 30 s — server clock (create_at=${earlierBotPost.create_at}) is far ahead of the runner.`,
    }).toBe(true);
}

test.describe('Per-channel agent auto-reply', () => {
    test('root_posts mode: agent auto-replies in a thread to a new root post, but not to thread replies', async ({ page }) => {
        test.setTimeout(120000);

        const { client, botUser, channel } = await setupClientAndChannel('rootposts');
        await putAutoReply(client, channel.id, botUser.id, 'root_posts');

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.goto(`${mattermost.url()}/test/channels/${channel.name}`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        // Root post without any @mention must trigger an in-thread auto-reply.
        const me = await client.getMe();
        const rootMessage = 'Auto-reply root post test';
        await mmPage.sendChannelMessage(rootMessage);
        const rootPost = await waitForUserPost(client, channel.id, me.id, rootMessage);

        const botReply = await mmPage.expectBotThreadReplyFromApi(
            client, channel.id, botUser.id, rootPost.id, responseTestText,
        );
        await mmPage.waitForReply();

        // Negative half: in root_posts mode a thread reply must NOT trigger another
        // auto-reply. The user gets the ephemeral @mention reminder instead, but
        // ephemerals are not persisted, so the API-level assertion here stays
        // "no bot post".
        await waitOutSkewWindow(botReply);
        const sinceMs2 = Date.now();
        await client.createPost({
            channel_id: channel.id,
            root_id: rootPost.id,
            message: 'thread reply without mention',
        });
        await mmPage.expectNoBotDmReplyFromApi(client, channel.id, botUser.id, sinceMs2, { observeDurationMs: 15000 });
    });

    test('threads mode: agent auto-replies to thread replies too', async ({ page }) => {
        test.setTimeout(120000);

        const { client, botUser, channel } = await setupClientAndChannel('threads');
        await putAutoReply(client, channel.id, botUser.id, 'threads');

        const mmPage = new MattermostPage(page);

        // API-only for speed; the UI path is exercised by the root_posts test.
        const rootPost = await client.createPost({
            channel_id: channel.id,
            message: 'Auto-reply threads mode root post',
        });

        const firstReply = await mmPage.expectBotThreadReplyFromApi(
            client, channel.id, botUser.id, rootPost.id, responseTestText,
        );

        // A thread reply without a mention must trigger a SECOND auto-reply. The
        // catch-all mock returns identical text for every completion, so reply #2
        // is distinguished from reply #1 by post id AND a create_at lower bound
        // taken after reply #1's skew window — the time bound anchors causality to
        // the thread reply (a bug that double-replied to the root post would
        // otherwise satisfy a pure id-exclusion filter).
        await waitOutSkewWindow(firstReply);
        const sinceMs2 = Date.now();
        await client.createPost({
            channel_id: channel.id,
            root_id: rootPost.id,
            message: 'thread reply without mention',
        });

        await expect.poll(async () => {
            const postsResponse = await client.getPosts(channel.id, 0, 200);
            const secondReplies = Object.values(postsResponse.posts || {}).filter(
                (p) => p.user_id === botUser.id &&
                    p.root_id === rootPost.id &&
                    p.id !== firstReply.id &&
                    p.create_at >= sinceMs2 - 5000 &&
                    p.message.includes(responseTestText),
            );
            return secondReplies.length;
        }, { timeout: 45000, intervals: [500, 1000, 2000] }).toBeGreaterThan(0);
    });

    test('off/unset: mixed-case @mention responds without an ephemeral reminder', async ({ page }) => {
        test.setTimeout(120000);

        const { client, botUser, channel } = await setupClientAndChannel('mixedcase');
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.goto(`${mattermost.url()}/test/channels/${channel.name}`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        const rootPost = await client.createPost({
            channel_id: channel.id,
            message: '@mock mixed-case mention test',
        });
        const firstReply = await mmPage.expectBotThreadReplyFromApi(
            client, channel.id, botUser.id, rootPost.id, responseTestText,
        );

        // The root post was created via API, so the channel view may not have
        // the websocket thread footer yet. Reload, then wait for the indicator
        // instead of clicking '1 reply' against the remaining test timeout.
        await page.goto(`${mattermost.url()}/test/channels/${channel.name}`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });
        const mixedCaseReplyIndicator = page.getByText(/\d+ repl/i);
        await expect(mixedCaseReplyIndicator).toBeVisible({ timeout: 30000 });
        await mixedCaseReplyIndicator.click();
        const replyBox = page.getByTestId('reply_textbox');
        await replyBox.waitFor({ state: 'visible', timeout: 15000 });
        await waitOutSkewWindow(firstReply);
        const sinceMs = Date.now();
        await replyBox.fill('@Mock try again');
        await replyBox.press('Enter');

        await expect.poll(async () => {
            const postsResponse = await client.getPosts(channel.id, 0, 200);
            return Object.values(postsResponse.posts || {}).filter(
                (p) => p.user_id === botUser.id &&
                    p.root_id === rootPost.id &&
                    p.id !== firstReply.id &&
                    p.create_at >= sinceMs - 5000 &&
                    p.message.includes(responseTestText),
            ).length;
        }, { timeout: 45000, intervals: [500, 1000, 2000] }).toBeGreaterThan(0);
        await expect(
            page.locator('#rhsContainer').getByText('To respond to an agent you must @mention them.'),
        ).not.toBeVisible();
    });

    test('off/unset: no auto-reply, and the @mention ephemeral reminder is unchanged', async ({ page }) => {
        test.setTimeout(120000);

        // No PUT — the channel stays on the default (unset/off) path.
        const { client, botUser, channel } = await setupClientAndChannel('off');

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.goto(`${mattermost.url()}/test/channels/${channel.name}`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        // A root post without a mention must not get any reply.
        const sinceMs = Date.now();
        await mmPage.sendChannelMessage('No auto-reply expected');
        await mmPage.expectNoBotDmReplyFromApi(client, channel.id, botUser.id, sinceMs, { observeDurationMs: 15000 });

        // Reminder baseline: proves phase 2 did not regress maybeNotifyAgentMentionNeeded
        // in non-auto-reply channels.
        const me = await client.getMe();
        const mentionMessage = 'please reply';
        await mmPage.mentionBot('mock', mentionMessage);
        const mentionPost = await waitForUserPost(client, channel.id, me.id, `@mock ${mentionMessage}`);
        const botReply = await mmPage.expectBotThreadReplyFromApi(
            client, channel.id, botUser.id, mentionPost.id, responseTestText,
        );

        // Reply in the thread through the UI without a mention — ephemeral posts are
        // only rendered for the posting user's live session, so this must be UI-driven.
        const offReplyIndicator = page.getByText(/\d+ repl/i);
        await expect(offReplyIndicator).toBeVisible({ timeout: 30000 });
        await offReplyIndicator.click();
        const replyBox = page.getByTestId('reply_textbox');
        await replyBox.waitFor({ state: 'visible', timeout: 15000 });
        await waitOutSkewWindow(botReply);
        const sinceMs2 = Date.now();
        await replyBox.click();
        await replyBox.fill('follow-up without mention');
        await replyBox.press('Enter');

        // The ephemeral "you must @mention" reminder renders in the RHS (partial match
        // of agent_mention_reminder_post.tsx's FormattedMessage; the trailing
        // "Click here to loop in @Mock Bot" link renders separately).
        await expect(
            page.locator('#rhsContainer').getByText('To respond to an agent you must @mention them.'),
        ).toBeVisible({ timeout: 15000 });

        // ...and no bot post follows the un-mentioned reply.
        await mmPage.expectNoBotDmReplyFromApi(client, channel.id, botUser.id, sinceMs2, { observeDurationMs: 10000 });
    });

    test('ambient mode: classifier true produces a normal auto-reply', async ({ page }) => {
        test.setTimeout(120000);

        const { client, botUser, channel } = await setupClientAndChannel('ambienttrue');
        await putAutoReply(client, channel.id, botUser.id, 'ambient', {
            instructions: 'only billing questions',
        });
        await mockAmbientClassifier(true);

        const mmPage = new MattermostPage(page);
        const rootPost = await client.createPost({
            channel_id: channel.id,
            message: 'Ambient true should reply',
        });
        await mmPage.expectBotThreadReplyFromApi(
            client, channel.id, botUser.id, rootPost.id, responseTestText,
        );
    });

    test('ambient mode: classifier false produces no bot post', async ({ page }) => {
        test.setTimeout(120000);

        const { client, botUser, channel } = await setupClientAndChannel('ambientfalse');
        await putAutoReply(client, channel.id, botUser.id, 'ambient', {
            instructions: 'only billing questions',
        });
        await mockAmbientClassifier(false);

        const mmPage = new MattermostPage(page);
        const sinceMs = Date.now();
        await client.createPost({
            channel_id: channel.id,
            message: 'Ambient false should stay silent',
        });
        await mmPage.expectNoBotDmReplyFromApi(client, channel.id, botUser.id, sinceMs, { observeDurationMs: 15000 });
        await expectClassifierDeclined();
    });

    test('ambient_threads mode: replies without a mention only in threads the agent was @mentioned into', async ({ page }) => {
        test.setTimeout(150000);

        const { client, botUser, channel } = await setupClientAndChannel('ambientthreads');
        await putAutoReply(client, channel.id, botUser.id, 'ambient_threads', {
            instructions: 'only billing questions',
        });
        await mockAmbientClassifier(true);
        const mmPage = new MattermostPage(page);

        // Until the agent is brought into a thread, neither roots nor replies are classified.
        const sinceMs = Date.now();
        const unmentionedRoot = await client.createPost({
            channel_id: channel.id,
            message: 'Ambient threads ignores this root',
        });
        await client.createPost({
            channel_id: channel.id,
            root_id: unmentionedRoot.id,
            message: 'And this reply in a thread without the agent',
        });
        await mmPage.expectNoBotDmReplyFromApi(client, channel.id, botUser.id, sinceMs, { observeDurationMs: 10000 });

        const mentionedRoot = await client.createPost({
            channel_id: channel.id,
            message: `@${botUser.username} can you help with billing?`,
        });
        await mmPage.expectBotThreadReplyFromApi(
            client, channel.id, botUser.id, mentionedRoot.id, responseTestText,
        );

        const followUp = await client.createPost({
            channel_id: channel.id,
            root_id: mentionedRoot.id,
            message: 'And what about last month\'s invoice?',
        });
        await expect.poll(async () => {
            const thread = await client.getPostThread(mentionedRoot.id);
            return Object.values(thread.posts).some(
                (p) => p.user_id === botUser.id && p.create_at > followUp.create_at && p.message.includes(responseTestText),
            );
        }, { timeout: 45000, intervals: [500, 1000, 2000] }).toBe(true);
    });
});
