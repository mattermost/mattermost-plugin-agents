// spec: tests/multiplayer-tool-calling/multiplayer-tool-calling.plan.md
// seed: tests/seed.spec.ts

import {test, expect, Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {
    buildPostToolSequence,
    buildRejectAfterFirstToolSequence,
    buildTitleFixture,
    EMBEDDED_CREATE_POST_TOOL,
    mergeFixtureFiles,
} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {
    MULTIPLAYER_ASK_TOOL_CONFIGS,
    RunToolConfigAIMockContainer,
} from 'helpers/tool-config-container';
import {adminUsername, adminPassword} from 'helpers/system-console-container';

/**
 * Multiplayer tool calling with deterministic aimock fixtures.
 * Invoker @mentions the bot; onlooker sees redacted tool info only.
 */

const invokerUsername = 'regularuser';
const invokerPassword = 'regularuser';
const onlookerUsername = 'seconduser';
const onlookerPassword = 'seconduser';
const botUsername = 'toolbot';
const createPostToolLabel = 'Create Post';

const multiplayerCustomInstructions = [
    'You have access to Mattermost tools including create_post and get_channel_info.',
    'When a user asks you to post a message, call get_channel_info first, then create_post.',
].join(' ');

let mattermost: MattermostContainer;
let aimock: AIMockContainer;

type TownSquareChannel = {
    id: string;
    displayName: string;
    teamDisplayName: string;
};

async function setupMultiplayerUsers(mattermostInstance: MattermostContainer): Promise<void> {
    await mattermostInstance.createUser('regularuser@sample.com', invokerUsername, invokerPassword);
    await mattermostInstance.addUserToTeam(invokerUsername, 'test');
    await mattermostInstance.createUser('seconduser@sample.com', onlookerUsername, onlookerPassword);
    await mattermostInstance.addUserToTeam(onlookerUsername, 'test');

    for (const [username, password] of [[invokerUsername, invokerPassword], [onlookerUsername, onlookerPassword], [adminUsername, adminPassword]]) {
        const client = await mattermostInstance.getClient(username, password);
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

    const adminClient = await mattermostInstance.getAdminClient();
    await adminClient.completeSetup({organization: 'test', install_plugins: []});
}

async function getTownSquareChannel(mattermostInstance: MattermostContainer): Promise<TownSquareChannel> {
    const client = await mattermostInstance.getClient(invokerUsername, invokerPassword);
    const teams = await client.getMyTeams();
    const team = teams[0];
    const channels = await client.getMyChannels(team.id);
    const townSquare = channels.find((channel: {name: string}) => channel.name === 'town-square');

    if (!townSquare) {
        throw new Error('Could not find town-square channel');
    }

    return {
        id: townSquare.id,
        displayName: townSquare.display_name || 'Town Square',
        teamDisplayName: team.display_name || 'Test',
    };
}

function titleFixtures(title: string) {
    return {fixtures: [buildTitleFixture(title)]};
}

async function captureReplyCount(page: Page): Promise<number> {
    return page.getByText(/\d+ repl/).count().catch(() => 0);
}

async function waitForReplyAndOpenThread(page: Page, countBeforeSend: number, timeout: number = 120000): Promise<void> {
    const replyIndicator = page.getByText(/\d+ repl/);
    const startTime = Date.now();

    while (Date.now() - startTime < timeout) {
        const currentCount = await replyIndicator.count().catch(() => 0);
        if (currentCount > countBeforeSend) {
            const lastIndicator = replyIndicator.last();
            if (await lastIndicator.isVisible().catch(() => false)) {
                await lastIndicator.click();
                await page.locator('#rhsContainer').waitFor({state: 'visible', timeout: 10000});
                await page.waitForTimeout(1000);
                return;
            }
        }
        await page.waitForTimeout(1000);
    }

    throw new Error('Timeout waiting for bot reply in thread');
}

async function openLatestThread(page: Page, timeout: number = 30000): Promise<void> {
    const replyIndicator = page.getByText(/\d+ repl/);
    await expect(replyIndicator.last()).toBeVisible({timeout});
    await replyIndicator.last().click();
    await page.locator('#rhsContainer').waitFor({state: 'visible', timeout: 10000});
    await page.waitForTimeout(1000);
}

async function waitForButtonInThread(page: Page, buttonName: string, timeout: number = 120000, throwOnTimeout: boolean = true): Promise<boolean> {
    const startTime = Date.now();
    const rhs = page.locator('#rhsContainer');

    while (Date.now() - startTime < timeout) {
        const button = rhs.getByRole('button', {name: buttonName, exact: true});
        if (await button.first().isVisible().catch(() => false)) {
            await page.waitForTimeout(500);
            return true;
        }
        await page.waitForTimeout(1000);
    }

    if (throwOnTimeout) {
        throw new Error(`Timeout waiting for '${buttonName}' button in thread`);
    }

    return false;
}

async function waitForAnyButtonInThread(
    page: Page,
    buttonNames: string[],
    timeout: number = 120000,
    throwOnTimeout: boolean = true,
): Promise<string | null> {
    const startTime = Date.now();
    const rhs = page.locator('#rhsContainer');

    while (Date.now() - startTime < timeout) {
        for (const buttonName of buttonNames) {
            const button = rhs.getByRole('button', {name: buttonName, exact: true});
            if (await button.first().isVisible().catch(() => false)) {
                await page.waitForTimeout(500);
                return buttonName;
            }
        }
        await page.waitForTimeout(1000);
    }

    if (throwOnTimeout) {
        throw new Error(`Timeout waiting for one of buttons in thread: ${buttonNames.join(', ')}`);
    }

    return null;
}

async function clickAllButtonsInThread(page: Page, buttonName: string): Promise<number> {
    const rhs = page.locator('#rhsContainer');
    let clicked = 0;

    while (true) {
        const button = rhs.getByRole('button', {name: buttonName, exact: true}).first();
        if (!(await button.isVisible().catch(() => false))) {
            return clicked;
        }

        await button.click();
        clicked++;
        await page.waitForTimeout(500);
    }
}

async function completeOneToolCallRound(page: Page, action: 'accept-share' | 'accept-keep-private' | 'reject'): Promise<boolean> {
    const firstDecisionButton = await waitForAnyButtonInThread(
        page,
        ['Accept', 'Share', 'Keep private'],
        120000,
        false,
    );

    if (!firstDecisionButton) {
        return false;
    }

    if (firstDecisionButton === 'Accept') {
        if (action === 'reject') {
            await clickAllButtonsInThread(page, 'Reject');
            await page.waitForTimeout(2000);
            return true;
        }

        await clickAllButtonsInThread(page, 'Accept');
        await waitForButtonInThread(page, 'Share', 120000);
    }

    if (action === 'accept-share' || action === 'reject') {
        await clickAllButtonsInThread(page, 'Share');
    } else {
        await clickAllButtonsInThread(page, 'Keep private');
    }

    await page.waitForTimeout(2000);
    return true;
}

async function completeAllToolCallRounds(page: Page, action: 'accept-share' | 'accept-keep-private' | 'reject'): Promise<number> {
    let rounds = 0;
    const maxRounds = 3;

    while (rounds < maxRounds) {
        const completed = await completeOneToolCallRound(page, action);
        if (!completed) {
            break;
        }
        rounds++;
    }

    return rounds;
}

async function waitForApprovalFlowToSettle(page: Page, timeout: number = 30000): Promise<void> {
    const startTime = Date.now();
    const rhs = page.locator('#rhsContainer');

    while (Date.now() - startTime < timeout) {
        const hasAccept = await rhs.getByRole('button', {name: 'Accept'}).first().isVisible().catch(() => false);
        const hasReject = await rhs.getByRole('button', {name: 'Reject'}).first().isVisible().catch(() => false);
        const hasShare = await rhs.getByRole('button', {name: 'Share'}).first().isVisible().catch(() => false);
        const hasKeepPrivate = await rhs.getByRole('button', {name: 'Keep private'}).first().isVisible().catch(() => false);

        if (!hasAccept && !hasReject && !hasShare && !hasKeepPrivate) {
            await page.waitForTimeout(2000);
            return;
        }
        await page.waitForTimeout(1000);
    }

    throw new Error('Timeout waiting for tool approval flow to settle');
}

async function waitForPageReady(page: Page): Promise<void> {
    await page.waitForSelector('[class*="channel-header"], #channelHeaderInfo', {timeout: 30000});
    await page.waitForTimeout(2000);
}

async function navigateToChannel(page: Page, baseUrl: string, channelName: string = 'off-topic'): Promise<void> {
    await page.goto(`${baseUrl}/test/channels/${channelName}`);
    await waitForPageReady(page);
}

test.describe('Multiplayer Tool Calling (Aimock)', () => {
    // Serial: one shared Mattermost + aimock sidecar; each test reloads fixtures via setFixtures/restart.
    // Keep-private runs first so a prior happy-path create_post does not leave Town Square posts that
    // could confuse later reject-path assertions (reject verifies the post text stays absent).
    test.describe.configure({mode: 'serial'});

    let townSquare: TownSquareChannel;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunToolConfigAIMockContainer({
            toolConfigs: MULTIPLAYER_ASK_TOOL_CONFIGS,
            customInstructions: multiplayerCustomInstructions,
            enableVectorIndex: true,
            defaultBotName: 'toolbot',
            botId: 'tool-test-bot',
            botDisplayName: 'Tool Test Bot',
        });
        await setupMultiplayerUsers(mattermost);
        townSquare = await getTownSquareChannel(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: {fixtures: [buildTitleFixture('Multiplayer bootstrap')]},
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        if (mattermost) {
            await mattermost.stop();
        }
    });

    test('Accept tool calls then Keep Private', async ({browser}) => {
        test.setTimeout(480000);

        const privatePrompt = `multiplayer keep private ${Date.now()}`;
        const privatePostText = `This result stays private ${Date.now()}`;
        const privateFinalText = `PRIVATE_FINAL_${Date.now()}`;
        const privateInfoCallId = `call_private_info_${Date.now()}`;
        const privateCreateCallId = `call_private_create_${Date.now()}`;

        await aimock.setFixtures(mergeFixtureFiles(
            titleFixtures('Multiplayer keep private'),
            buildPostToolSequence({
                userPromptMarker: privatePrompt,
                infoCallId: privateInfoCallId,
                createCallId: privateCreateCallId,
                channelId: townSquare.id,
                channelDisplayName: townSquare.displayName,
                teamDisplayName: townSquare.teamDisplayName,
                postText: privatePostText,
                finalText: privateFinalText,
            }),
        ));

        const invokerContext = await browser.newContext();
        const onlookerContext = await browser.newContext();
        const invokerPage = await invokerContext.newPage();
        const onlookerPage = await onlookerContext.newPage();

        try {
            const invokerMM = new MattermostPage(invokerPage);
            const onlookerMM = new MattermostPage(onlookerPage);
            const baseUrl = mattermost.url();

            await invokerMM.login(baseUrl, invokerUsername, invokerPassword);
            await onlookerMM.login(baseUrl, onlookerUsername, onlookerPassword);
            await navigateToChannel(invokerPage, baseUrl, 'off-topic');
            await navigateToChannel(onlookerPage, baseUrl, 'off-topic');

            const replyCountBeforeSend = await captureReplyCount(invokerPage);
            await invokerMM.mentionBot(botUsername, privatePrompt);

            await waitForReplyAndOpenThread(invokerPage, replyCountBeforeSend);
            await openLatestThread(onlookerPage);

            const rhs = invokerPage.locator('#rhsContainer');
            await waitForAnyButtonInThread(invokerPage, ['Accept', 'Share', 'Keep private']);

            expect(await completeOneToolCallRound(invokerPage, 'accept-share')).toBe(true);
            await expect(rhs.getByText(createPostToolLabel, {exact: true})).toBeVisible({timeout: 45000});
            expect(await completeOneToolCallRound(invokerPage, 'accept-keep-private')).toBe(true);

            await waitForApprovalFlowToSettle(invokerPage);

            const onlookerRhs = onlookerPage.locator('#rhsContainer');
            await expect(onlookerRhs.getByRole('button', {name: 'Share'})).not.toBeVisible();
            await expect(onlookerRhs.getByRole('button', {name: 'Keep private'})).not.toBeVisible();
            await expect(onlookerRhs.getByRole('button', {name: 'Accept'})).not.toBeVisible();
            await expect(rhs.locator('[data-testid="llm-bot-post"]').last()).toBeVisible();

            await navigateToChannel(invokerPage, baseUrl, 'town-square');
            await expect(invokerPage.locator('.post-message__text').getByText(privatePostText).first()).toBeVisible({timeout: 30000});
        } finally {
            await invokerContext.close();
            await onlookerContext.close();
        }
    });

    test('Happy path: Accept and Share all tool calls (invoker + onlooker)', async ({browser}) => {
        test.setTimeout(480000);

        const happyPrompt = `multiplayer happy path ${Date.now()}`;
        const happyPostText = `Hello from the AI agent - happy path test ${Date.now()}`;
        const happyFinalText = `HAPPY_FINAL_${Date.now()}`;
        const happyInfoCallId = `call_happy_info_${Date.now()}`;
        const happyCreateCallId = `call_happy_create_${Date.now()}`;

        await aimock.setFixtures(mergeFixtureFiles(
            titleFixtures('Multiplayer happy path'),
            buildPostToolSequence({
                userPromptMarker: happyPrompt,
                infoCallId: happyInfoCallId,
                createCallId: happyCreateCallId,
                channelId: townSquare.id,
                channelDisplayName: townSquare.displayName,
                teamDisplayName: townSquare.teamDisplayName,
                postText: happyPostText,
                finalText: happyFinalText,
            }),
        ));

        const invokerContext = await browser.newContext();
        const onlookerContext = await browser.newContext();
        const invokerPage = await invokerContext.newPage();
        const onlookerPage = await onlookerContext.newPage();

        try {
            const invokerMM = new MattermostPage(invokerPage);
            const onlookerMM = new MattermostPage(onlookerPage);
            const baseUrl = mattermost.url();

            await invokerMM.login(baseUrl, invokerUsername, invokerPassword);
            await onlookerMM.login(baseUrl, onlookerUsername, onlookerPassword);
            await navigateToChannel(invokerPage, baseUrl, 'off-topic');
            await navigateToChannel(onlookerPage, baseUrl, 'off-topic');

            const replyCountBeforeSend = await captureReplyCount(invokerPage);
            await invokerMM.mentionBot(botUsername, happyPrompt);

            await waitForReplyAndOpenThread(invokerPage, replyCountBeforeSend);
            await openLatestThread(onlookerPage);
            await waitForAnyButtonInThread(invokerPage, ['Accept', 'Share', 'Keep private']);

            const onlookerRhs = onlookerPage.locator('#rhsContainer');
            await expect(onlookerRhs.locator('[data-testid="llm-bot-post"]').last()).toBeVisible({timeout: 30000});
            await expect(onlookerRhs.getByRole('button', {name: 'Accept'})).not.toBeVisible();
            await expect(onlookerRhs.getByRole('button', {name: 'Reject'})).not.toBeVisible();

            const rounds = await completeAllToolCallRounds(invokerPage, 'accept-share');
            expect(rounds).toBe(2);

            await waitForApprovalFlowToSettle(invokerPage);

            await onlookerPage.waitForTimeout(3000);
            await expect(onlookerRhs.getByRole('button', {name: 'Accept'})).not.toBeVisible();
            await expect(onlookerRhs.getByRole('button', {name: 'Share'})).not.toBeVisible();

            await navigateToChannel(invokerPage, baseUrl, 'town-square');
            await expect(invokerPage.locator('.post-message__text').getByText(happyPostText).first()).toBeVisible({timeout: 10000});
        } finally {
            await invokerContext.close();
            await onlookerContext.close();
        }
    });

    test('Reject tool calls at call stage', async ({browser}) => {
        test.setTimeout(480000);

        const rejectPrompt = `multiplayer reject path ${Date.now()}`;
        const rejectPostText = `This should be rejected ${Date.now()}`;
        const rejectCreateCallId = `call_reject_create_${Date.now()}`;
        const rejectFinalText = `REJECT_FINAL_${Date.now()}`;

        await aimock.setFixtures(mergeFixtureFiles(
            titleFixtures('Multiplayer reject path'),
            buildRejectAfterFirstToolSequence({
                userPromptMarker: rejectPrompt,
                toolCallId: rejectCreateCallId,
                toolName: EMBEDDED_CREATE_POST_TOOL,
                toolArguments: {
                    channel_id: townSquare.id,
                    channel_display_name: townSquare.displayName,
                    team_display_name: townSquare.teamDisplayName,
                    message: rejectPostText,
                },
                finalContent: rejectFinalText,
            }),
        ));

        const context = await browser.newContext();
        const page = await context.newPage();

        try {
            const mmPage = new MattermostPage(page);
            const baseUrl = mattermost.url();

            await mmPage.login(baseUrl, invokerUsername, invokerPassword);
            await navigateToChannel(page, baseUrl, 'off-topic');

            const replyCountBeforeSend = await captureReplyCount(page);
            await mmPage.mentionBot(botUsername, rejectPrompt);
            await waitForReplyAndOpenThread(page, replyCountBeforeSend);

            await waitForAnyButtonInThread(page, ['Accept', 'Share', 'Keep private']);
            await clickAllButtonsInThread(page, 'Reject');

            const rhs = page.locator('#rhsContainer');
            await expect.poll(async () => {
                const rejectedLabelVisible = await rhs.getByText('Rejected').first().isVisible().catch(() => false);
                const acceptVisible = await rhs.getByRole('button', {name: 'Accept'}).isVisible().catch(() => false);
                return rejectedLabelVisible || !acceptVisible;
            }, {timeout: 30000}).toBe(true);

            await waitForApprovalFlowToSettle(page, 60000);
            await expect(rhs.getByRole('button', {name: 'Share'})).not.toBeVisible();
            await expect(rhs.getByRole('button', {name: 'Keep private'})).not.toBeVisible();
            await expect(rhs.getByText(createPostToolLabel, {exact: true})).toBeVisible({timeout: 30000});

            await navigateToChannel(page, baseUrl, 'town-square');
            await expect(page.getByText(rejectPostText)).not.toBeVisible();
        } finally {
            await context.close();
        }
    });
});
