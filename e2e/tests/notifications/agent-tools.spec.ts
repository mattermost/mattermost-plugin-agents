import {expect, test} from '@playwright/test';

import {EMBEDDED_GET_CHANNEL_INFO_TOOL, EMBEDDED_READ_CHANNEL_TOOL} from 'helpers/aimock-fixtures';
import type {NotificationLab} from 'helpers/notification-lab';
import {
    collectAndPublish,
    expectNothingAtAll,
    expectNotifiedOnce,
    expectNotNotified,
    placeElsewhere,
    pushedTo,
    settlePost,
    slowAnswerAfterTool,
    startDiscussion,
    startMatrixLab,
    toolCallFor,
    waitForAgentPost,
    waitForPostState,
    withTitle,
} from 'helpers/notification-lab/matrix';
import type {MatrixUserKey} from 'helpers/notification-lab/matrix';
import type {MmPost} from 'helpers/notification-lab/rest';

/**
 * Agent responses that call tools, in a channel thread started by `member`, on a server that
 * defers agent post notifications. A pause for the requester's approval notifies the requester
 * only; the answer that completes the run notifies everyone, once. Tool rounds the agent runs on
 * its own are part of one response and notify nobody until it ends.
 *
 * Needs MM_IMAGE with DeliverPostNotifications; skipped otherwise (see agent-legacy-server.spec.ts).
 */

const EVERYONE: MatrixUserKey[] = ['requester', 'member', 'bystander', 'observer'];
const NON_FOLLOWERS: MatrixUserKey[] = ['bystander', 'observer'];

function answer(scenario: string): string {
    return `Answer ${scenario}: the channel has four members, and the pinned checklist lists the migration as the next step.`;
}

test.describe('Agent notifications with tool calls in a channel thread', () => {
    let lab: NotificationLab;

    test.beforeAll(async ({browser}) => {
        test.setTimeout(300000);
        lab = await startMatrixLab(browser, {
            users: EVERYONE,
            browsers: EVERYONE,
            requires: 'deferred',
            enableChannelMentionToolCalling: true,
            bot: {mcpDynamicToolLoading: false},
            mcp: {
                enabled: true,
                enablePluginServer: true,
                embeddedServer: {
                    enabled: true,
                    tool_configs: [
                        {name: 'get_channel_info', policy: 'ask', enabled: true},
                        {name: 'read_channel', policy: 'auto_run_everywhere', enabled: true},
                    ],
                },
                idleTimeoutMinutes: 30,
                servers: [],
            },
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

    test('S6 tool approval: the pause notifies only the requester; the shared answer notifies the thread follower, not the watching requester', async ({}, testInfo) => {
        test.setTimeout(240000);
        const prompt = 'nlab-s6 approve a tool';
        const finalText = answer('S6');
        const callId = 'call_nlab_s6_channel_info';
        const discussion = await startDiscussion(lab, 'nlab-s6');
        await lab.script(withTitle(
            toolCallFor(prompt, {id: callId, name: EMBEDDED_GET_CHANNEL_INFO_TOOL, arguments: {channel_id: discussion.channel.id}}),
            slowAnswerAfterTool(callId, finalText),
        ));
        const options = {channelId: discussion.channel.id, threadRootId: discussion.root.id};
        const mark = await lab.mark();

        await lab.postAs('requester', discussion.channel.id, `@${lab.bot.username} ${prompt}`, discussion.root.id);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        const paused = await collectAndPublish(lab, testInfo, mark, {
            ...options,
            label: 'S6 channel thread, agent waits for tool approval (everyone away)',
            minWaitMs: 5000,
            until: pushedTo('requester', agentPost.id),
        });
        expectNotifiedOnce(paused, 'requester', agentPost.id, '', {soft: true});
        expectNotNotified(paused, 'member', agentPost.id, {otherPostTexts: [prompt], soft: true});
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(paused, key, {soft: true});
        }

        const requester = lab.user('requester');
        await requester.openChannel(discussion.channel.name);
        await requester.openThreadInRhs(discussion.root.id);
        await requester.focus();
        const rhs = requester.requirePage().locator('#rhsContainer');
        const approved = await lab.mark();
        await rhs.getByRole('button', {name: /^accept$/i}).click();
        await rhs.getByRole('button', {name: /^share$/i}).click();
        const afterShare = await settlePost(lab, agentPost.id, (p) => p.message.includes(finalText));
        const report = await collectAndPublish(lab, testInfo, approved, {
            ...options,
            label: 'S6 channel thread, requester accepted and shared on screen, watched the answer',
            postsSince: mark.at,
            minWaitMs: 8000,
        });

        expect(afterShare.message, 'the answer after sharing was streamed and saved').toContain(finalText);
        expectNothingAtAll(report, 'requester');
        expectNotifiedOnce(report, 'member', agentPost.id, finalText.slice(0, 40));
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(report, key);
        }
    });

    test('S11 auto-run tool round then a slow answer: nothing while it runs, one delivery at the end', async ({}, testInfo) => {
        test.setTimeout(240000);
        const prompt = 'nlab-s11 auto run a tool';
        const finalText = answer('S11');
        const callId = 'call_nlab_s11_read_channel';
        const discussion = await startDiscussion(lab, 'nlab-s11');
        await lab.script(withTitle(
            toolCallFor(prompt, {id: callId, name: EMBEDDED_READ_CHANNEL_TOOL, arguments: {channel_id: discussion.channel.id, limit: 5}}),
            slowAnswerAfterTool(callId, finalText),
        ));
        const options = {channelId: discussion.channel.id, threadRootId: discussion.root.id};
        const observer = lab.user('observer');
        await observer.openChannel(discussion.channel.name);
        await observer.openThreadInRhs(discussion.root.id);
        await observer.focus();
        const mark = await lab.mark();

        await lab.postAs('requester', discussion.channel.id, `@${lab.bot.username} ${prompt}`, discussion.root.id);
        const agentPost = await waitForAgentPost(lab, discussion.channel.id, mark);
        await expect(observer.requirePage().locator('#rhsContainer').getByText(finalText.slice(0, 16))).toBeVisible({timeout: 60000});
        const midRun = await lab.snapshot(mark, {...options, label: 'S11 channel thread, tool ran, answer streaming (everyone away)'});
        const stillStreaming = (await lab.admin.get<MmPost>(`/posts/${agentPost.id}`)).message === '';
        await lab.publish(testInfo, midRun);
        expect(stillStreaming, 'the mid-run snapshot was taken before the answer was saved').toBe(true);
        for (const key of EVERYONE) {
            expectNotNotified(midRun, key, agentPost.id, {otherPostTexts: [prompt]});
        }

        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);
        const report = await collectAndPublish(lab, testInfo, mark, {
            ...options,
            label: 'S11 channel thread, auto-run tool round then answer: final',
            minWaitMs: 5000,
            until: (rep) => pushedTo('member', agentPost.id)(rep) && pushedTo('requester', agentPost.id)(rep),
        });

        expectNotifiedOnce(report, 'requester', agentPost.id, finalText.slice(0, 40));
        expectNotifiedOnce(report, 'member', agentPost.id, finalText.slice(0, 40), {otherPostTexts: [prompt]});
        for (const key of NON_FOLLOWERS) {
            expectNothingAtAll(report, key);
        }
    });
});
