import {expect, test} from '@playwright/test';

import type {NotificationLab} from 'helpers/notification-lab';
import {failingReply} from 'helpers/notification-lab';
import {
    collectAndPublish,
    expectNothingAtAll,
    expectNotifiedOnce,
    expectNotNotified,
    placeElsewhere,
    pushedTo,
    settlePost,
    slowAnswer,
    startDiscussion,
    startMatrixLab,
    waitForAgentPost,
    waitForPostState,
    withTitle,
} from 'helpers/notification-lab/matrix';
import type {MatrixUserKey} from 'helpers/notification-lab/matrix';
import type {MmChannel, MmPost} from 'helpers/notification-lab/rest';

/**
 * Agent responses to an @mention in a channel thread, on a server that defers agent post
 * notifications until the response ends. The thread was started by `member` (who follows it);
 * `requester` @mentions the agent in it; `bystander`, `mentioned` and `observer` are channel
 * members who do not follow the thread. A completed answer notifies everyone the post would
 * normally notify, with the final text; anything else notifies the requester only, or nobody when
 * the requester stopped it.
 *
 * Needs MM_IMAGE with DeliverPostNotifications; skipped otherwise (see agent-legacy-server.spec.ts).
 */

const EVERYONE: MatrixUserKey[] = ['requester', 'member', 'bystander', 'mentioned', 'observer'];
const NON_FOLLOWERS: MatrixUserKey[] = ['bystander', 'mentioned', 'observer'];

function answer(scenario: string): string {
    return `Answer ${scenario}: the release train leaves on Thursday, and the migration has to land in staging first.`;
}

type Discussion = {channel: MmChannel; root: MmPost};

test.describe('Agent notifications in a channel thread', () => {
    let lab: NotificationLab;

    test.beforeAll(async ({browser}) => {
        test.setTimeout(300000);
        lab = await startMatrixLab(browser, {
            users: EVERYONE,
            browsers: EVERYONE,
            requires: 'deferred',
        });
    });

    test.afterAll(async () => {
        await lab?.stop();
    });

    test.beforeEach(async () => {
        for (const key of EVERYONE) {
            await placeElsewhere(lab, key);
        }
    });

    /** `requester` @mentions the agent in the discussion thread; returns the prompt post. */
    async function ask(discussion: Discussion, prompt: string): Promise<MmPost> {
        return lab.postAs('requester', discussion.channel.id, `@${lab.bot.username} ${prompt}`, discussion.root.id);
    }

    function collectOptions(label: string, discussion: Discussion) {
        return {label, channelId: discussion.channel.id, threadRootId: discussion.root.id};
    }

    test('S3 requester watches the start in the thread, then leaves: requester and thread follower notified once with the final text', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s3 watch start then leave';
        const finalText = answer('S3');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const discussion = await startDiscussion(lab, 'nlab-s3');
        const requester = lab.user('requester');
        await requester.openChannel(discussion.channel.name);
        await requester.openThreadInRhs(discussion.root.id);
        await requester.focus();
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await ask(discussion, prompt);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        await expect(requester.requirePage().locator('#rhsContainer').getByText(finalText.slice(0, 16))).toBeVisible({timeout: 30000});
        await placeElsewhere(lab, 'requester');
        const leftMidStream = (await lab.admin.get<MmPost>(`/posts/${agentPost.id}`)).message === '';

        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);
        const report = await collectAndPublish(lab, testInfo, mark, {
            ...collectOptions('S3 channel thread, requester watched the start then left (other channel, blurred, away)', discussion),
            until: (rep) => pushedTo('member', agentPost.id)(rep) &&
                pushedTo('requester', agentPost.id)(rep),
        });

        expect(leftMidStream, 'the requester left before the answer was saved').toBe(true);
        expectNotifiedOnce(report, 'requester', agentPost.id, finalText.slice(0, 40));
        expectNotifiedOnce(report, 'member', agentPost.id, finalText.slice(0, 40), {otherPostTexts: [prompt]});
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(report, key);
        }
        expect(report.users.requester.thread, 'requester: thread unread with the answer').toMatchObject({
            isFollowing: true, unreadReplies: 1, unreadMentions: 0, unread: true,
        });
        expect(report.users.member.thread, 'member: prompt and answer unread').toMatchObject({
            isFollowing: true, unreadReplies: 2, unreadMentions: 0, unread: true,
        });
    });

    test('S4a requester watches the answer in the thread (core RHS) until it ends: requester not notified, follower notified', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s4a watch in core rhs';
        const finalText = answer('S4a');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const discussion = await startDiscussion(lab, 'nlab-s4a');
        const requester = lab.user('requester');
        await requester.openChannel(discussion.channel.name);
        await requester.openThreadInRhs(discussion.root.id);
        await requester.focus();
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await ask(discussion, prompt);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        await expect(requester.requirePage().locator('#rhsContainer').getByText(finalText)).toBeVisible({timeout: 60000});
        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);

        const report = await collectAndPublish(lab, testInfo, mark, {
            ...collectOptions('S4a channel thread, requester watches in the core RHS (focused, away)', discussion),
            minWaitMs: 5000,
            until: pushedTo('member', agentPost.id),
        });

        expect(report.users.requester.viewing.some((v) => v.postId === agentPost.id && v.viewing), 'webapp reported the answer as on screen').toBe(true);
        expectNothingAtAll(report, 'requester');
        expect(report.users.requester.thread, 'requester: thread stays read').toMatchObject({
            isFollowing: true, unreadReplies: 0, unreadMentions: 0, unread: false,
        });
        expectNotifiedOnce(report, 'member', agentPost.id, finalText.slice(0, 40), {otherPostTexts: [prompt]});
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(report, key);
        }
    });

    test('S4b requester watches the answer in the Threads view until it ends: requester not notified, follower notified', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s4b watch in threads view';
        const finalText = answer('S4b');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const discussion = await startDiscussion(lab, 'nlab-s4b');
        const requester = lab.user('requester');
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await ask(discussion, prompt);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        await requester.openThread(discussion.root.id);
        await requester.focus();
        await expect(requester.requirePage().locator('.ThreadPane').getByText(finalText)).toBeVisible({timeout: 60000});
        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);

        const report = await collectAndPublish(lab, testInfo, mark, {
            ...collectOptions('S4b channel thread, requester watches in the Threads view (focused, away)', discussion),
            minWaitMs: 5000,
            until: pushedTo('member', agentPost.id),
        });

        expect(report.users.requester.viewing.some((v) => v.postId === agentPost.id && v.viewing), 'webapp reported the answer as on screen').toBe(true);
        expectNothingAtAll(report, 'requester');
        expect(report.users.requester.thread, 'requester: thread stays read').toMatchObject({
            isFollowing: true, unreadReplies: 0, unreadMentions: 0, unread: false,
        });
        expectNotifiedOnce(report, 'member', agentPost.id, finalText.slice(0, 40), {otherPostTexts: [prompt]});
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(report, key);
        }
    });

    test('S5 the final answer @mentions a channel member: that member gets one mention notification', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s5 answer mentions someone';
        const finalText = 'Answer S5: @mentioned please review the migration before it lands in staging on Thursday.';
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const discussion = await startDiscussion(lab, 'nlab-s5');
        const mark = await lab.mark();

        await ask(discussion, prompt);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);
        const report = await collectAndPublish(lab, testInfo, mark, {
            ...collectOptions('S5 channel thread, the answer @mentions "mentioned" (everyone away)', discussion),
            until: (rep) => pushedTo('member', agentPost.id)(rep) &&
                pushedTo('mentioned', agentPost.id)(rep),
        });

        const shown = 'please review the migration';
        expectNotifiedOnce(report, 'mentioned', agentPost.id, shown);
        expect(report.users.mentioned.channelUnread, 'mentioned: one mention in the channel').toMatchObject({mentionCount: 1});
        expect(report.users.mentioned.thread, 'mentioned: follows the thread with one unread mention').toMatchObject({
            isFollowing: true, unreadMentions: 1, unread: true,
        });
        expectNotifiedOnce(report, 'requester', agentPost.id, shown);
        expectNotifiedOnce(report, 'member', agentPost.id, shown, {otherPostTexts: [prompt]});
        expect(report.users.member.channelUnread, 'member: not mentioned').toMatchObject({mentionCount: 0});
        expectNothingAtAll(report, 'bystander');
        expectNothingAtAll(report, 'observer');
    });

    test('S8 the LLM request fails: only the requester is notified, with the error', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s8 llm fails';
        await lab.script(failingReply({prompt, title: 'Notification matrix'}));
        const discussion = await startDiscussion(lab, 'nlab-s8');
        const mark = await lab.mark();

        await ask(discussion, prompt);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        const failed = await waitForPostState(lab, agentPost.id, (p) => p.message !== '');
        const report = await collectAndPublish(lab, testInfo, mark, {
            ...collectOptions('S8 channel thread, LLM request fails (everyone away)', discussion),
            minWaitMs: 8000,
            until: pushedTo('requester', agentPost.id),
        });

        expectNotifiedOnce(report, 'requester', agentPost.id, failed.message.slice(0, 30));
        expectNotNotified(report, 'member', agentPost.id, {otherPostTexts: [prompt]});
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(report, key);
        }
        expect(report.users.member.thread, 'member: the prompt and the error reply are unread, without a mention').toMatchObject({
            isFollowing: true, unreadReplies: 2, unreadMentions: 0,
        });
    });

    test('S9 requester stops the answer mid-stream: nobody is notified about it', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s9 stop mid stream';
        const finalText = answer('S9');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const discussion = await startDiscussion(lab, 'nlab-s9');
        const observer = lab.user('observer');
        await observer.openChannel(discussion.channel.name);
        await observer.openThreadInRhs(discussion.root.id);
        await observer.focus();
        const mark = await lab.mark();

        await ask(discussion, prompt);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        await expect(observer.requirePage().locator('#rhsContainer').getByText(finalText.slice(0, 16))).toBeVisible({timeout: 30000});
        await lab.stopResponse('requester', agentPost.id);
        const stopped = await settlePost(lab, agentPost.id, (p) => p.message !== '', 30000);

        const report = await collectAndPublish(lab, testInfo, mark, {
            ...collectOptions('S9 channel thread, requester stops mid-stream (requester away, observer watching)', discussion),
            minWaitMs: 8000,
            until: (rep) => rep.users.member.push.some((p) => p.type === 'message'),
        });

        expect(stopped.message, 'the partial answer was saved').not.toBe('');
        expect(stopped.message.length, 'the answer was cut short').toBeLessThan(finalText.length);
        expectNothingAtAll(report, 'requester');
        expectNotNotified(report, 'member', agentPost.id, {otherPostTexts: [prompt]});
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(report, key);
        }
        expect(report.users.member.thread, 'member: the prompt and the stopped answer are unread, without a mention').toMatchObject({
            isFollowing: true, unreadReplies: 2, unreadMentions: 0,
        });
    });

    test('S10 requester regenerates a delivered answer: only the requester is notified about the new answer', async ({}, testInfo) => {
        test.setTimeout(240000);
        const prompt = 'nlab-s10 regenerate';
        const firstText = answer('S10 first');
        const regeneratedText = answer('S10 regenerated');
        await lab.script(withTitle(slowAnswer(prompt, firstText)));
        const discussion = await startDiscussion(lab, 'nlab-s10');
        const mark = await lab.mark();

        await ask(discussion, prompt);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        await waitForPostState(lab, agentPost.id, (p) => p.message === firstText);
        const delivered = await collectAndPublish(lab, testInfo, mark, {
            ...collectOptions('S10 channel thread, first answer completes (everyone away)', discussion),
            until: pushedTo('member', agentPost.id),
        });
        expectNotifiedOnce(delivered, 'requester', agentPost.id, firstText.slice(0, 40), {soft: true});
        expectNotifiedOnce(delivered, 'member', agentPost.id, firstText.slice(0, 40), {otherPostTexts: [prompt], soft: true});

        await lab.script(withTitle(slowAnswer(prompt, regeneratedText)));
        const regenerated = await lab.mark();
        await lab.regenerateResponse('requester', agentPost.id);
        const afterRegenerate = await settlePost(lab, agentPost.id, (p) => p.message === regeneratedText);
        const report = await collectAndPublish(lab, testInfo, regenerated, {
            ...collectOptions('S10 channel thread, requester regenerates (everyone away)', discussion),
            postsSince: mark.at,
            minWaitMs: 8000,
        });

        expect(afterRegenerate.message, 'the regenerated answer was streamed and saved').toBe(regeneratedText);
        expectNotifiedOnce(report, 'requester', agentPost.id, regeneratedText.slice(0, 40));
        expectNotNotified(report, 'member', agentPost.id);
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(report, key);
        }
    });
});
