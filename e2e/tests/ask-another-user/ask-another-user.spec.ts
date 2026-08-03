// Two-user AskAnotherUser flow with deterministic aimock fixtures: initiator DMs the
// bot in the RHS, target answers/declines a DM card.

import {test, expect} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {buildTitleFixture, buildToolCallAndTextResponse} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIPlugin} from 'helpers/ai-plugin';
import {RunToolConfigAIMockContainer} from 'helpers/tool-config-container';
import {adminUsername, adminPassword} from 'helpers/system-console-container';

const initiatorUsername = 'regularuser';
const initiatorPassword = 'regularuser';
const targetUsername = 'seconduser';
const targetPassword = 'seconduser';
const botUsername = 'toolbot';

// Exact card-header label: ToolCard title-cases on underscores only, so the
// PascalCase tool name renders verbatim.
const askAnotherUserToolName = 'AskAnotherUser';

// The waiting copy ends in a Unicode ellipsis; match without it.
const waitingForTargetRegex = new RegExp(`Waiting for @${targetUsername} to answer`);

let mattermost: MattermostContainer;
let aimock: AIMockContainer;

async function setupUsers(mattermostInstance: MattermostContainer): Promise<void> {
    await mattermostInstance.createUser('regularuser@sample.com', initiatorUsername, initiatorPassword);
    await mattermostInstance.addUserToTeam(initiatorUsername, 'test');
    await mattermostInstance.createUser('seconduser@sample.com', targetUsername, targetPassword);
    await mattermostInstance.addUserToTeam(targetUsername, 'test');

    for (const [username, password] of [[initiatorUsername, initiatorPassword], [targetUsername, targetPassword], [adminUsername, adminPassword]]) {
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

test.describe('Ask Another User (Aimock)', () => {
    // Serial: one shared Mattermost + aimock sidecar; each test loads its own
    // fixtures via setFixtures. AskAnotherUser is a built-in tool that defaults
    // to the ask policy, so no toolConfigs are needed.
    test.describe.configure({mode: 'serial'});

    test.beforeAll(async () => {
        test.setTimeout(240000);
        mattermost = await RunToolConfigAIMockContainer({
            defaultBotName: botUsername,
            botId: 'ask-user-test-bot',
            botDisplayName: 'Ask User Test Bot',
        });
        await setupUsers(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: {fixtures: [buildTitleFixture('Ask another user bootstrap')]},
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('happy path: target answers and the conversation resumes', async ({browser}) => {
        test.setTimeout(300000);

        const askPrompt = `ask another user happy ${Date.now()}`;
        const askCallId = `call_ask_user_happy_${Date.now()}`;
        const question = `Which release broke the login flow? (${Date.now()})`;
        const optionPlain = '4.2.0';
        const optionAnswer = '4.2.1';
        const contextLine = 'Needed to finish the incident report.';
        const finalText = `ASK_HAPPY_FINAL_${Date.now()}`;

        await aimock.setFixtures(buildToolCallAndTextResponse({
            userMessage: askPrompt,
            toolCallId: askCallId,
            toolName: askAnotherUserToolName,
            toolArguments: {
                username: targetUsername,
                question,
                options: [{label: optionPlain}, {label: optionAnswer, description: 'the hotfix release'}],
                context: contextLine,
            },
            finalContent: finalText,
            title: 'Ask another user happy path',
        }));

        const initiatorContext = await browser.newContext();
        const targetContext = await browser.newContext();
        const initiatorPage = await initiatorContext.newPage();
        const targetPage = await targetContext.newPage();

        try {
            const initiatorMM = new MattermostPage(initiatorPage);
            const targetMM = new MattermostPage(targetPage);
            const aiPlugin = new AIPlugin(initiatorPage);
            const baseUrl = mattermost.url();

            // Initiator: DM the bot from the RHS.
            await initiatorMM.login(baseUrl, initiatorUsername, initiatorPassword);
            await aiPlugin.openRHS();
            await aiPlugin.sendMessage(askPrompt);

            // Pending tool card with Accept/Reject; no continuation yet.
            const rhs = initiatorPage.getByTestId('mattermost-ai-rhs');
            await expect(rhs.locator('[data-testid="llm-bot-post"]').last()).toBeVisible({timeout: 90000});
            await expect(rhs.getByText(askAnotherUserToolName, {exact: true})).toBeVisible({timeout: 90000});

            const acceptButton = rhs.getByRole('button', {name: /^accept$/i});
            const rejectButton = rhs.getByRole('button', {name: /^reject$/i});
            await expect(acceptButton).toBeVisible();
            await expect(rejectButton).toBeVisible();
            await expect(rhs.getByText(finalText)).not.toBeVisible();

            // Accept dispatches the question card and parks the run in waiting.
            await acceptButton.click();
            await expect(rhs.getByText(waitingForTargetRegex)).toBeVisible({timeout: 30000});
            await expect(acceptButton).not.toBeVisible();
            await expect(rhs.getByText(finalText)).not.toBeVisible();

            // Target logs in after the Accept so the card renders on a fresh
            // channel load instead of relying on websocket delivery.
            await targetMM.login(baseUrl, targetUsername, targetPassword);
            await targetPage.goto(`${baseUrl}/test/messages/@${botUsername}`);
            const channelView = targetPage.getByTestId('channel_view');

            // Scope to the card's post body: the channel's sr-only live region
            // also announces the card's plain-text fallback (the question), so
            // unscoped getByText would double-match.
            const askCard = channelView.getByTestId('postContent').filter({hasText: question});
            await expect(askCard).toBeVisible({timeout: 30000});
            await expect(askCard.getByText(contextLine)).toBeVisible();
            await expect(askCard.getByText(`Asked on behalf of @${initiatorUsername}`)).toBeVisible();
            await expect(askCard.getByRole('button', {name: new RegExp(optionAnswer)})).toBeVisible();

            const answerButton = askCard.getByRole('button', {name: 'Answer', exact: true});
            const declineButton = askCard.getByRole('button', {name: 'Decline', exact: true});
            await expect(answerButton).toBeVisible();
            await expect(declineButton).toBeVisible();

            // Permalink back to the initiating conversation (not followable by
            // the target — it points at the initiator's bot DM).
            const permalink = askCard.getByRole('link', {name: 'View conversation'});
            await expect(permalink).toBeVisible();
            expect(await permalink.getAttribute('href')).toContain('/_redirect/pl/');

            // Select an option and answer.
            await askCard.getByRole('button', {name: new RegExp(optionAnswer)}).click();
            await expect(answerButton).toBeEnabled();
            await answerButton.click();

            // Target card resolves to the answered state.
            await expect(askCard.getByText('Answered')).toBeVisible({timeout: 30000});
            await expect(askCard.getByText(optionAnswer, {exact: true})).toBeVisible();
            await expect(answerButton).not.toBeVisible();
            await expect(declineButton).not.toBeVisible();

            // Reload: the resolved state must come from the server-patched
            // card-post props, not the local submit snapshot — a prop-patch
            // regression would revert the card to pending here.
            await targetPage.reload();
            await expect(askCard.getByText('Answered')).toBeVisible({timeout: 30000});
            await expect(askCard.getByText(optionAnswer, {exact: true})).toBeVisible();
            await expect(answerButton).not.toBeVisible();
            await expect(declineButton).not.toBeVisible();

            // Initiator conversation resumes with the scripted continuation.
            await expect(rhs.getByText(finalText)).toBeVisible({timeout: 60000});
            await expect(rhs.getByText(waitingForTargetRegex)).not.toBeVisible();
            await expect(initiatorPage.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});
        } finally {
            await initiatorContext.close();
            await targetContext.close();
        }
    });

    test('decline: target declines and the initiator sees the declined state', async ({browser}) => {
        test.setTimeout(300000);

        const declinePrompt = `ask another user decline ${Date.now()}`;
        const declineCallId = `call_ask_user_decline_${Date.now()}`;
        const declineQuestion = `Can you confirm the deploy window for tonight? (${Date.now()})`;
        const declineFinalText = `ASK_DECLINE_FINAL_${Date.now()} proceeding without confirmation`;

        // Free-form-only question (no options) exercises the textarea path.
        await aimock.setFixtures(buildToolCallAndTextResponse({
            userMessage: declinePrompt,
            toolCallId: declineCallId,
            toolName: askAnotherUserToolName,
            toolArguments: {
                username: targetUsername,
                question: declineQuestion,
            },
            finalContent: declineFinalText,
            title: 'Ask another user decline path',
        }));

        const initiatorContext = await browser.newContext();
        const targetContext = await browser.newContext();
        const initiatorPage = await initiatorContext.newPage();
        const targetPage = await targetContext.newPage();

        try {
            const initiatorMM = new MattermostPage(initiatorPage);
            const targetMM = new MattermostPage(targetPage);
            const aiPlugin = new AIPlugin(initiatorPage);
            const baseUrl = mattermost.url();

            // Fresh conversation: New chat keeps test 1's turns (and its tool
            // call ID) out of this test's completion requests.
            await initiatorMM.login(baseUrl, initiatorUsername, initiatorPassword);
            await aiPlugin.openRHS();
            await aiPlugin.resetState();
            await aiPlugin.sendMessage(declinePrompt);

            const rhs = initiatorPage.getByTestId('mattermost-ai-rhs');
            await expect(rhs.locator('[data-testid="llm-bot-post"]').last()).toBeVisible({timeout: 90000});
            await expect(rhs.getByText(askAnotherUserToolName, {exact: true})).toBeVisible({timeout: 90000});

            const acceptButton = rhs.getByRole('button', {name: /^accept$/i});
            await expect(acceptButton).toBeVisible();

            await acceptButton.click();
            await expect(rhs.getByText(waitingForTargetRegex)).toBeVisible({timeout: 30000});

            // Target: the DM now holds test 1's resolved card plus this pending
            // one — scope to the post body holding this test's unique question
            // (which also avoids the sr-only live-region double match).
            await targetMM.login(baseUrl, targetUsername, targetPassword);
            await targetPage.goto(`${baseUrl}/test/messages/@${botUsername}`);
            const channelView = targetPage.getByTestId('channel_view');

            const declineCard = channelView.getByTestId('postContent').filter({hasText: declineQuestion});
            await expect(declineCard).toBeVisible({timeout: 30000});
            await expect(declineCard.getByPlaceholder(/Type your answer/)).toBeVisible();

            const declineButton = declineCard.getByRole('button', {name: 'Decline', exact: true});
            await expect(declineButton).toBeEnabled();
            await declineButton.click();

            await expect(declineCard.getByText('You declined to answer')).toBeVisible({timeout: 30000});
            await expect(declineCard.getByRole('button', {name: 'Answer', exact: true})).not.toBeVisible();
            await expect(declineButton).not.toBeVisible();

            // Initiator resumes with the scripted continuation.
            await expect(rhs.getByText(declineFinalText)).toBeVisible({timeout: 60000});
            await expect(rhs.getByText(waitingForTargetRegex)).not.toBeVisible();
            await expect(initiatorPage.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});

            // The declined line renders inside the collapsible card body, and a
            // resolved card is collapsed by default — expand via the header.
            await rhs.getByText(askAnotherUserToolName, {exact: true}).last().click();
            await expect(rhs.getByText(`@${targetUsername} declined to answer`)).toBeVisible({timeout: 15000});
        } finally {
            await initiatorContext.close();
            await targetContext.close();
        }
    });
});
