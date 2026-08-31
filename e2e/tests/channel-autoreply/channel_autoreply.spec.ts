// spec: .planning/phase-4/PLAN.md — per-channel agent auto-reply (API -> trigger -> streamed reply)
// seed: tests/seed.spec.ts
//
// Auto-reply is CONFIGURED VIA THE REST API rather than the channel-settings tab UI:
// the e2e harness runs mattermost-enterprise-edition:release-11.9 (helpers/mmcontainer.ts),
// and registerChannelSettingsTab only exists on servers >= 11.10, so the phase-3 tab does
// not register here. The tab component is covered by webapp unit tests; this spec covers
// the end-to-end contract. Run locally with MM_IMAGE=...release-11.10 to see the tab.

import { test, expect } from '@playwright/test';
import type { Client4 } from '@mattermost/client';
import type { Post } from '@mattermost/types/posts';
import type { UserProfile } from '@mattermost/types/users';
import type { Channel } from '@mattermost/types/channels';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { mattermostAIPluginRoutes } from 'helpers/plugin-http';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.beforeEach(async () => {
    await openAIMock.resetMocks();
    await openAIMock.addCompletionMock(responseTest);
});

test.afterAll(async () => {
    await openAIMock?.stop();
    await mattermost?.stop();
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

async function putAutoReply(client: Client4, channelId: string, botId: string, mode: string): Promise<void> {
    const routes = mattermostAIPluginRoutes(mattermost.url());
    await routes.putJson(`channel/${channelId}/autoreply`, client.getToken(), {
        bot_id: botId,
        mode,
    });
    // Round-trip sanity check: GET must return what was stored.
    const setting = await routes.getJson(`channel/${channelId}/autoreply`, client.getToken());
    expect(setting).toMatchObject({ bot_id: botId, mode });
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
    const deadline = Date.now() + 30000;
    while (Date.now() <= earlierBotPost.create_at + 6000) {
        if (Date.now() > deadline) {
            throw new Error(
                `waitOutSkewWindow: still inside the skew window after 30 s — server clock (create_at=${earlierBotPost.create_at}) is far ahead of the runner (now=${Date.now()}).`,
            );
        }
        await new Promise((resolve) => setTimeout(resolve, 250));
    }
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

        await page.getByText('1 reply').click();
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
        await page.getByText('1 reply').click();
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
});
