import {expect, test} from '@playwright/test';

import {NotificationLab, slowReply, slowReplyDurationMs} from 'helpers/notification-lab';

/**
 * Baseline for the notification lab: proves that every capture channel (desktop, push, email,
 * unread/mention state) records something real against the current server image, then prints what
 * each user receives when an agent answers slowly and the requester navigates away mid-stream.
 *
 * Run against a locally built server with `MM_IMAGE=<tag from e2e/scripts/build-local-mm-image.sh>`.
 */

const AGENT_PROMPT = 'notification-lab-baseline-prompt-001';
const AGENT_REPLY =
    'Notification lab baseline reply. This answer streams slowly so the requester can leave the conversation before it is finished.';
const AGENT_STREAM = {chunkSize: 12, chunkDelayMs: 400};

test.describe('Notification lab baseline', () => {
    test.describe.configure({mode: 'serial'});

    let lab: NotificationLab;

    test.beforeAll(async ({browser}) => {
        test.setTimeout(300000);
        lab = await NotificationLab.start({
            fixtures: slowReply({prompt: AGENT_PROMPT, content: AGENT_REPLY, ...AGENT_STREAM}),
        });
        await lab.openBrowsers(browser, ['requester', 'member']);
    });

    test.afterAll(async () => {
        await lab?.stop();
    });

    test('regular DM reaches an away user on desktop, push and email and leaves unread state', async ({}, testInfo) => {
        test.setTimeout(120000);
        const sender = lab.user('requester');
        const recipient = lab.user('member');
        const text = 'baseline direct message from a regular user';

        const dm = await lab.directChannel('requester', 'member');
        // Push skips a user who is active in the very channel (push_status defaults to "online" and
        // the server tracks the active channel for 20 s), so the recipient looks at another channel.
        await recipient.openChannel('town-square');
        await recipient.blur();
        await lab.setStatus('member', 'away');
        const mark = await lab.mark();

        await lab.postAs('requester', dm.id, text);
        const report = await lab.collect(mark, {
            label: 'regular DM to an away user (window blurred, other channel open)',
            channelId: dm.id,
            until: (r) => r.users.member.desktop.length > 0 && r.users.member.push.length > 0 && r.users.member.email.length > 0,
        });
        await lab.publish(testInfo, report);

        const member = report.users.member;
        expect(member.desktop.map((n) => n.body)).toEqual([expect.stringContaining(text)]);
        expect(member.push.filter((p) => p.type === 'message').map((p) => p.message)).toEqual([expect.stringContaining(text)]);
        expect(member.push[0].channelId).toBe(dm.id);
        expect(member.push[0].senderName).toContain(sender.username);
        expect(member.email).toHaveLength(1);
        expect(member.email[0].subject).not.toBe('');
        expect(member.email[0].text).toContain(text);
        expect(member.channelUnread?.unreadMsgs).toBe(1);
        expect(member.mentionCount).toBe(1);

        // The sender and a user outside the conversation are not notified. (The sender's own mobile
        // session gets a silent "clear" push because posting marks the channel read.)
        expect(report.users.requester.push.filter((p) => p.type === 'message')).toEqual([]);
        expect(report.users.requester.email).toEqual([]);
        expect(report.users.bystander.push).toEqual([]);
        expect(report.users.bystander.email).toEqual([]);
        expect(report.unattributed.push).toEqual([]);
        expect(report.unattributed.email).toEqual([]);
    });

    test('online user with a focused window gets no push or email and no desktop notification for the open channel', async ({}, testInfo) => {
        test.setTimeout(120000);
        const recipient = lab.user('member');
        const sender = lab.user('requester');
        const text = 'baseline message to an online user who is looking at the channel';

        const dm = await lab.directChannel('requester', 'member');
        await recipient.openDirectMessage(sender.username);
        await recipient.focus();
        await lab.setStatus('member', 'online');
        await lab.markChannelRead('member', dm.id);
        expect(await recipient.isFocused()).toBe(true);
        const mark = await lab.mark();

        await lab.postAs('requester', dm.id, text);
        const report = await lab.collect(mark, {
            label: 'regular DM to an online user (window focused, channel open)',
            channelId: dm.id,
            minWaitMs: 4000,
        });
        await lab.publish(testInfo, report);

        const member = report.users.member;
        expect(member.desktop).toEqual([]);
        // Seeing the post marks the channel read, which sends a silent "clear" push; no "message" push.
        expect(member.push.filter((p) => p.type === 'message')).toEqual([]);
        expect(member.email).toEqual([]);
    });

    test('online user whose window is blurred still gets a desktop notification for the open channel', async ({}, testInfo) => {
        test.setTimeout(120000);
        const recipient = lab.user('member');
        const sender = lab.user('requester');
        const text = 'baseline message to a user whose window is in the background';

        const dm = await lab.directChannel('requester', 'member');
        await recipient.openDirectMessage(sender.username);
        await lab.setStatus('member', 'online');
        await recipient.blur();
        expect(await recipient.isFocused()).toBe(false);
        const mark = await lab.mark();

        await lab.postAs('requester', dm.id, text);
        const report = await lab.collect(mark, {
            label: 'regular DM to an online user (window blurred, channel open)',
            channelId: dm.id,
            until: (r) => r.users.member.desktop.length > 0,
        });
        await lab.publish(testInfo, report);

        expect(report.users.member.desktop.map((n) => n.body)).toEqual([expect.stringContaining(text)]);
        expect(report.users.member.email).toEqual([]);
    });

    test('agent DM: requester leaves while the answer streams (current plugin behaviour)', async ({}, testInfo) => {
        test.setTimeout(180000);
        const requester = lab.user('requester');

        const dm = await lab.botDm('requester');
        await requester.openDirectMessage(lab.bot.username);
        await requester.focus();
        await lab.setStatus('requester', 'away');
        await lab.markChannelRead('requester', dm.id);
        const mark = await lab.mark();

        await requester.typeAndSend(AGENT_PROMPT);
        // The agent post exists as soon as streaming starts; its text is saved only when the stream ends.
        const streaming = await lab.waitForPost(dm.id, {byBot: true, since: mark.at});
        expect(streaming.message).not.toBe(AGENT_REPLY);
        const requestPost = await lab.waitForPost(dm.id, {text: AGENT_PROMPT, since: mark.at});

        await requester.openChannel('town-square');
        await requester.blur();

        const report = await lab.collect(mark, {
            label: 'agent DM, requester navigated away mid-stream',
            channelId: dm.id,
            threadRootId: requestPost.id,
            timeoutMs: slowReplyDurationMs({content: AGENT_REPLY, ...AGENT_STREAM}) + 60000,
            until: (r) => r.posts.some((p) => p.message === AGENT_REPLY),
        });
        await lab.publish(testInfo, report);

        const finalPost = report.posts.find((p) => p.message === AGENT_REPLY);
        expect(finalPost, 'the agent finished streaming the full reply').toBeDefined();

        // Only stable facts are asserted: people who are not part of the conversation get nothing.
        for (const key of ['member', 'follower', 'bystander']) {
            expect(report.users[key].desktop, `${key} desktop`).toEqual([]);
            expect(report.users[key].push, `${key} push`).toEqual([]);
            expect(report.users[key].email, `${key} email`).toEqual([]);
        }
    });
});
