import {expect, test} from '@playwright/test';

import type {NotificationLab} from 'helpers/notification-lab';
import {
    collectAndPublish,
    expectNothingAtAll,
    messagePushes,
    placeElsewhere,
    slowAnswer,
    startMatrixLab,
    waitForAgentPost,
    waitForPostState,
    withTitle,
} from 'helpers/notification-lab/matrix';
import type {MmChannel} from 'helpers/notification-lab/rest';

/**
 * Agent DM responses on a server without deferred post notifications (no
 * `DeliverPostNotifications`, e.g. the released image). The plugin suppresses core's
 * notifications for the empty placeholder post and pushes the final text to the requester once
 * the response ends, unless the requester watched it end. No desktop notification or email.
 *
 * Skipped on servers that support deferred notifications (see agent-dm.spec.ts).
 */

const OTHERS = ['member', 'bystander'] as const;

function answer(scenario: string): string {
    return `Answer ${scenario}: the deployment checklist has four steps, and the second one needs a database backup before you continue.`;
}

test.describe('Agent notifications in a DM on a server without deferred notifications', () => {
    let lab: NotificationLab;
    let dm: MmChannel;

    test.beforeAll(async ({browser}) => {
        test.setTimeout(300000);
        lab = await startMatrixLab(browser, {
            users: ['requester', 'member', 'bystander'],
            browsers: ['requester'],
            requires: 'legacy',
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

    test('S12a requester leaves mid-stream: one push with the final text, no desktop notification or email', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s12a legacy leave mid stream';
        const finalText = answer('S12a');
        await lab.script(withTitle(slowAnswer(prompt, finalText)));
        const requester = lab.user('requester');
        await requester.openDirectMessage(lab.bot.username);
        await requester.focus();
        await lab.setStatus('requester', 'away');
        const mark = await lab.mark();

        await requester.typeAndSend(prompt);
        const promptPost = await lab.waitForPost(dm.id, {text: prompt, since: mark.at});
        const agentPost = await waitForAgentPost(lab, dm.id, mark);
        await placeElsewhere(lab, 'requester');

        await waitForPostState(lab, agentPost.id, (p) => p.message === finalText);
        const report = await collectAndPublish(lab, testInfo, mark, {
            label: 'S12a legacy server DM, requester left mid-stream (other channel, blurred, away)',
            channelId: dm.id,
            threadRootId: promptPost.id,
            minWaitMs: 8000,
            until: (rep) => rep.users.requester.push.some((p) => p.type === 'message'),
        });

        const r = report.users.requester;
        expect(messagePushes(r).map((p) => p.message), 'requester: one push showing the final text').toEqual([
            expect.stringContaining(finalText.slice(0, 40)),
        ]);
        expect(r.desktop, 'requester: no desktop notifications').toEqual([]);
        expect(r.email, 'requester: no emails').toEqual([]);
        for (const key of OTHERS) {
            expectNothingAtAll(report, key);
        }
    });

    test('S12b requester watches the answer in the agents RHS until it ends: nothing at all', async ({}, testInfo) => {
        test.setTimeout(180000);
        const prompt = 'nlab-s12b legacy watch in agents rhs';
        const finalText = answer('S12b');
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
            label: 'S12b legacy server DM, requester watches in the agents RHS (focused, away)',
            channelId: dm.id,
            threadRootId: promptPost.id,
            minWaitMs: 8000,
        });

        expect(report.users.requester.viewing.some((v) => v.postId === agentPost.id && v.viewing), 'webapp reported the answer as on screen').toBe(true);
        expectNothingAtAll(report, 'requester');
        for (const key of OTHERS) {
            expectNothingAtAll(report, key);
        }
    });
});
