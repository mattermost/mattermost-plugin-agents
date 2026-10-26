import {expect, test} from '@playwright/test';

import type {NotificationLab} from 'helpers/notification-lab';
import {
    collectAndPublish,
    expectNothingAtAll,
    expectNotifiedOnce,
    placeElsewhere,
    settlePost,
    slowAnswer,
    slowAnswerAfterTool,
    startMatrixLab,
    toolCallFor,
    waitForAgentPost,
    waitForPostState,
    withTitle,
} from 'helpers/notification-lab/matrix';
import type {MmChannel} from 'helpers/notification-lab/rest';

/**
 * Agent responses in the requester's DM with the agent, on a server that defers agent post
 * notifications until the response ends: the requester is notified once, with the final text,
 * unless the response was on their screen in a focused window when it ended.
 *
 * Needs MM_IMAGE with DeliverPostNotifications; skipped otherwise (see agent-legacy-server.spec.ts).
 */

const OTHERS = ['member', 'bystander'] as const;

function answer(scenario: string): string {
    return `Answer ${scenario}: the deployment checklist has four steps, and the second one needs a database backup before you continue.`;
}

test.describe('Agent notifications in a DM', () => {
    let lab: NotificationLab;
    let dm: MmChannel;

    test.beforeAll(async ({browser}) => {
        test.setTimeout(300000);
        lab = await startMatrixLab(browser, {
            users: ['requester', 'member', 'bystander'],
            browsers: ['requester'],
            requires: 'deferred',
        });
        dm = await lab.botDm('requester');
    });

    test.afterAll(async () => {
        await lab?.stop();
    });

    test.beforeEach(async () => {
        for (const key of OTHERS) {
            await lab.setStatus(key, 'away');
        }
        await lab.markChannelRead('requester', dm.id);
    });

    // Status away for every watching scenario: core would push and email an away user, so only
    // the plugin's "on screen" check can be what keeps them quiet.

    test('S1a requester watches the answer in the agents RHS until it ends: nobody is notified', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s1a watch in agents rhs';
        const finalText = answer('S1a');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const requester = lab.user('requester');
        await requester.openChannel('town-square');
        await requester.focus();
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await requester.askAgentInRhs(prompt);
        const promptPost = await lab.waitForPost(dm.id, {text: prompt, since: mark.at});
        const agentPost = await waitForAgentPost(lab, dm.id, mark);
        await expect(requester.requirePage().getByTestId('mattermost-ai-rhs').getByText(finalText)).toBeVisible({timeout: 60000});
        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);

        const report = await collectAndPublish(lab, testInfo, mark, {
            label: 'S1a DM, requester watches in the agents RHS (focused, away)',
            channelId: dm.id,
            threadRootId: promptPost.id,
            minWaitMs: 5000,
        });

        const r = report.users.requester;
        expect(r.viewing.some((v) => v.postId === agentPost.id && v.viewing), 'webapp reported the answer as on screen').toBe(true);
        expectNothingAtAll(report, 'requester');
        expect(r.thread, 'DM thread stays read').toMatchObject({unreadReplies: 0, unreadMentions: 0, unread: false});
        expect(r.channelUnread).toMatchObject({unreadMsgsRoot: 0, mentionCountRoot: 0});
        for (const key of OTHERS) {
            expectNothingAtAll(report, key);
        }
    });

    test('S1b requester watches the answer in the DM thread (core RHS) until it ends: nobody is notified', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s1b watch in core thread';
        const finalText = answer('S1b');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const requester = lab.user('requester');
        await requester.openDirectMessage(lab.bot.username);
        await requester.focus();
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await requester.typeAndSend(prompt);
        const promptPost = await lab.waitForPost(dm.id, {text: prompt, since: mark.at});
        await requester.openThreadInRhs(promptPost.id);
        const agentPost = await waitForAgentPost(lab, dm.id, mark);
        await expect(requester.requirePage().locator('#rhsContainer').getByText(finalText)).toBeVisible({timeout: 60000});
        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);

        const report = await collectAndPublish(lab, testInfo, mark, {
            label: 'S1b DM, requester watches the thread in the core RHS (focused, away)',
            channelId: dm.id,
            threadRootId: promptPost.id,
            minWaitMs: 5000,
        });

        const r = report.users.requester;
        expect(r.viewing.some((v) => v.postId === agentPost.id && v.viewing), 'webapp reported the answer as on screen').toBe(true);
        expectNothingAtAll(report, 'requester');
        expect(r.thread, 'DM thread stays read').toMatchObject({unreadReplies: 0, unreadMentions: 0, unread: false});
        for (const key of OTHERS) {
            expectNothingAtAll(report, key);
        }
    });

    test('S1c requester keeps the thread open but switches to another app: notified once with the final text', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s1c thread open window blurred';
        const finalText = answer('S1c');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const requester = lab.user('requester');
        await requester.openDirectMessage(lab.bot.username);
        await requester.focus();
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await requester.typeAndSend(prompt);
        const promptPost = await lab.waitForPost(dm.id, {text: prompt, since: mark.at});
        await requester.openThreadInRhs(promptPost.id);
        const agentPost = await waitForAgentPost(lab, dm.id, mark);
        await requester.blur();
        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);

        const report = await collectAndPublish(lab, testInfo, mark, {
            label: 'S1c DM, thread open in the core RHS but window blurred (away)',
            channelId: dm.id,
            threadRootId: promptPost.id,
            until: (rep) => rep.users.requester.push.some((p) => p.type === 'message'),
        });

        expectNotifiedOnce(report, 'requester', agentPost.id, finalText.slice(0, 40));
        for (const key of OTHERS) {
            expectNothingAtAll(report, key);
        }
    });

    test('S2 requester leaves mid-stream: nothing while it streams, then one desktop, push and email with the final text', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s2 leave mid stream';
        const finalText = answer('S2');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const requester = lab.user('requester');
        await requester.openDirectMessage(lab.bot.username);
        await requester.focus();
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await requester.typeAndSend(prompt);
        const promptPost = await lab.waitForPost(dm.id, {text: prompt, since: mark.at});
        const agentPost = await waitForAgentPost(lab, dm.id, mark);
        await requester.openChannel('town-square');
        await requester.blur();

        const midStream = await lab.snapshot(mark, {
            label: 'S2 DM, requester left: mid-stream',
            channelId: dm.id,
            threadRootId: promptPost.id,
        });
        const stillStreaming = (await lab.admin.get<{message: string}>(`/posts/${agentPost.id}`)).message === '';
        await lab.publish(testInfo, midStream);
        expect(stillStreaming, 'the mid-stream snapshot was taken before the answer was saved').toBe(true);
        expectNothingAtAll(midStream, 'requester');
        expect(midStream.users.requester.thread, 'no unread before the answer is final').toMatchObject({unreadReplies: 0, unreadMentions: 0});

        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);
        const report = await collectAndPublish(lab, testInfo, mark, {
            label: 'S2 DM, requester left mid-stream (other channel, blurred, away): final',
            channelId: dm.id,
            threadRootId: promptPost.id,
            until: (rep) => rep.users.requester.push.some((p) => p.type === 'message') && rep.users.requester.email.length > 0,
        });

        expectNotifiedOnce(report, 'requester', agentPost.id, finalText.slice(0, 40));
        expect(report.users.requester.thread, 'DM thread unread with the answer').toMatchObject({unreadReplies: 1, unread: true});
        for (const key of OTHERS) {
            expectNothingAtAll(report, key);
        }
    });

    test('S7 ask-user question while the requester is away: requester notified at the pause, not again after answering on screen', async ({}, testInfo) => {
        test.setTimeout(240000);
        const prompt = 'nlab-s7 ask me which environment';
        const question = 'Which environment should I deploy to?';
        const finalText = answer('S7 after the question');
        const callId = 'call_nlab_s7_question';
        await lab.script(withTitle(
            toolCallFor(prompt, {
                id: callId,
                name: 'AskUserQuestion',
                arguments: {question, options: [{label: 'Staging'}, {label: 'Production'}]},
            }, 1000),
            slowAnswerAfterTool(callId, finalText),
        ));
        const requester = lab.user('requester');
        await requester.openDirectMessage(lab.bot.username);
        await requester.focus();
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await requester.typeAndSend(prompt);
        const promptPost = await lab.waitForPost(dm.id, {text: prompt, since: mark.at});
        const agentPost = await waitForAgentPost(lab, dm.id, mark);
        await placeElsewhere(lab, 'requester');

        const paused = await collectAndPublish(lab, testInfo, mark, {
            label: 'S7 DM, agent asks the requester a question (requester away)',
            channelId: dm.id,
            threadRootId: promptPost.id,
            until: (rep) => rep.users.requester.push.some((p) => p.type === 'message'),
        });
        const atPause = paused.users.requester;
        expect.soft(atPause.desktop.filter((d) => d.tag === agentPost.id), 'requester: one desktop notification at the pause').toHaveLength(1);
        expect.soft(atPause.push.filter((p) => p.type === 'message' && p.postId === agentPost.id), 'requester: one push at the pause').toHaveLength(1);
        expect.soft(atPause.email, 'requester: one email at the pause').toHaveLength(1);
        for (const key of OTHERS) {
            expectNothingAtAll(paused, key, {soft: true});
        }

        await requester.openDirectMessage(lab.bot.username);
        await requester.openThreadInRhs(promptPost.id);
        await requester.focus();
        const card = requester.requirePage().locator('#rhsContainer [data-testid="llm-bot-post"]').last();
        await expect(card.getByText(question, {exact: true})).toBeVisible({timeout: 30000});
        const answered = await lab.mark();
        await card.getByText('Staging', {exact: true}).click();
        await card.getByRole('button', {name: /^accept$/i}).click();
        const afterAnswer = await settlePost(lab, agentPost.id, (p) => p.message.includes(finalText));

        const report = await collectAndPublish(lab, testInfo, answered, {
            label: 'S7 DM, requester answered on screen and watched the follow-up',
            channelId: dm.id,
            threadRootId: promptPost.id,
            minWaitMs: 5000,
        });
        expect(afterAnswer.message, 'the follow-up after the answer was streamed and saved').toContain(finalText);
        expectNothingAtAll(report, 'requester');
        for (const key of OTHERS) {
            expectNothingAtAll(report, key);
        }
    });
});
