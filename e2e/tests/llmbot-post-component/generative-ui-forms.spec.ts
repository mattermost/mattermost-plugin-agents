// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Generative UI forms: a button in an agent reply's generated UI posts the
// requester's submission in the thread as them and the agent answers it like
// a typed message. Only the requester can submit, only from the latest
// finished reply, and only once.

import { expect, Locator, Page } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import { withDeadline } from 'helpers/deadline';

import { AIMockHarness, RunAIMockHarness, setupAimockTestPage } from 'helpers/aimock-harness';
import { buildTitleFixture } from 'helpers/aimock-fixtures';
import { MattermostPage } from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import { AIMOCK_BOT_NAME } from 'helpers/plugincontainer';

const HARNESS_START_MS = 300000;

const REQUESTER_USERNAME = 'regularuser';
const ONLOOKER_USERNAME = 'seconduser';

const SUBMIT_PROMPT = 'genui-form-submit-001';
const STALE_PROMPT = 'genui-form-stale-001';
const CHANNEL_PROMPT = 'genui-form-channel-001';
const FOLLOW_UP = 'genui-form-never-mind';

const FORM_REPLY = [
    'Pick where to deploy the fix.',
    '',
    '```openui-lang',
    'root = Stack([form])',
    'form = Form("deploy", [env, reason], [Button("Deploy"), Button("Cancel", "secondary")])',
    'env = RadioGroup("env", "Environment", ["staging", "production"], "staging")',
    'reason = TextInput("reason", "Reason", "", "", true)',
    '```',
].join('\n');

const SUBMISSION_ANSWER = 'Deploying the fix to production now.';
const FOLLOW_UP_ANSWER = 'No problem, nothing was deployed.';

const fixtures = {
    fixtures: [
        buildTitleFixture('Deploy form'),
        { match: { userMessage: 'Submitted **Deploy**' }, response: { content: SUBMISSION_ANSWER } },
        { match: { userMessage: FOLLOW_UP }, response: { content: FOLLOW_UP_ANSWER } },
        { match: { userMessage: SUBMIT_PROMPT }, response: { content: FORM_REPLY } },
        { match: { userMessage: STALE_PROMPT }, response: { content: FORM_REPLY } },
        { match: { userMessage: CHANNEL_PROMPT }, response: { content: FORM_REPLY } },
    ],
};

async function skipOnboarding(mattermost: MattermostContainer, username: string): Promise<void> {
    const client = await mattermost.getClient(username, username);
    const me = await client.getMe();
    await client.savePreferences(me.id, [
        { user_id: me.id, category: 'tutorial_step', name: me.id, value: '999' },
        { user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
        { user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
        { user_id: me.id, category: 'crt_thread_pane_step', name: me.id, value: '999' },
    ]);
}

async function openReplyThread(page: Page): Promise<void> {
    const replyIndicator = page.getByText(/\d+ repl/).last();
    await expect(replyIndicator).toBeVisible({ timeout: 60000 });
    await replyIndicator.click();
    await expect(page.locator('#rhsContainer')).toBeVisible({ timeout: 10000 });
}

function formIn(scope: Locator): Locator {
    return scope.getByTestId('generative-ui-form');
}

const formTest = containerTest(async (own) => {
    const harness = own(await withDeadline('Starting the aimock harness', HARNESS_START_MS, () => RunAIMockHarness({
        fixtures,
        bot: { skills: { generativeUI: true } },
        config: { enableGenerativeUI: true },
    })));
    return { harness };
});

formTest.describe('Generative UI forms - aimock', () => {
    formTest.describe.configure({ mode: 'serial' });

    let harness: AIMockHarness;

    formTest.beforeAll(async ({ containers }) => {
        ({ harness } = containers);
        await skipOnboarding(harness.mattermost, ONLOOKER_USERNAME);
    });

    formTest('the requester submits once and the agent answers the submission', async ({ page }) => {
        formTest.setTimeout(120000);
        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());
        const rhs = page.getByTestId('mattermost-ai-rhs');

        await aiPlugin.sendMessage(SUBMIT_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        const reply = rhs.locator('[data-testid="llm-bot-post"]').first();
        const form = formIn(reply);
        const deploy = form.getByRole('button', { name: 'Deploy' });
        await expect(deploy).toBeEnabled({ timeout: 30000 });

        // A required field left empty is flagged instead of submitted.
        await deploy.click();
        await expect(form.getByText('Required')).toBeVisible();

        await form.getByLabel('production').check();
        await form.getByLabel('Reason').fill('Hotfix for login');
        await deploy.click();
        await deploy.click({ force: true });

        const submission = rhs.getByText('Submitted Deploy from the form in the previous reply');
        await expect(submission).toBeVisible({ timeout: 30000 });
        await expect(rhs.getByText('Hotfix for login', { exact: true })).toBeVisible();
        await expect(rhs.locator('[data-testid="llm-bot-post"]').last()).toContainText(SUBMISSION_ANSWER, { timeout: 60000 });

        await expect(form.getByText('Submitted', { exact: true })).toBeVisible();
        await expect(deploy).toHaveAttribute('aria-pressed', 'true');
        await expect(deploy).toBeDisabled();
        await expect(form.getByLabel('Reason')).toHaveValue('Hotfix for login');
        await expect(submission).toHaveCount(1);
    });

    formTest('a reply the user answered by typing can no longer be submitted', async ({ page }) => {
        formTest.setTimeout(120000);
        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());
        const rhs = page.getByTestId('mattermost-ai-rhs');

        await aiPlugin.sendMessage(STALE_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        const form = formIn(rhs.locator('[data-testid="llm-bot-post"]').first());
        await expect(form.getByRole('button', { name: 'Deploy' })).toBeEnabled({ timeout: 30000 });

        await aiPlugin.sendMessage(FOLLOW_UP);
        await expect(rhs.locator('[data-testid="llm-bot-post"]').last()).toContainText(FOLLOW_UP_ANSWER, { timeout: 60000 });

        await expect(form.getByText('This form is no longer active')).toBeVisible({ timeout: 30000 });
        await expect(form.getByRole('button', { name: 'Deploy' })).toBeDisabled();
        await expect(form.getByLabel('Reason')).toBeDisabled();
    });

    formTest('in a channel only the requester can submit', async ({ browser }) => {
        formTest.setTimeout(180000);
        const baseUrl = harness.mattermost.url();
        const channelUrl = `${baseUrl}/test/channels/off-topic`;
        const requesterContext = await browser.newContext();
        const onlookerContext = await browser.newContext();

        try {
            const requesterPage = await requesterContext.newPage();
            const onlookerPage = await onlookerContext.newPage();
            const requester = new MattermostPage(requesterPage);
            await requester.login(baseUrl, REQUESTER_USERNAME, REQUESTER_USERNAME);
            await new MattermostPage(onlookerPage).login(baseUrl, ONLOOKER_USERNAME, ONLOOKER_USERNAME);
            await requesterPage.goto(channelUrl);
            await onlookerPage.goto(channelUrl);

            await requester.mentionBot(AIMOCK_BOT_NAME, CHANNEL_PROMPT);
            await openReplyThread(requesterPage);
            await openReplyThread(onlookerPage);

            const requesterForm = formIn(requesterPage.locator('#rhsContainer [data-testid="llm-bot-post"]').first());
            const onlookerForm = formIn(onlookerPage.locator('#rhsContainer [data-testid="llm-bot-post"]').first());
            await expect(requesterForm.getByRole('button', { name: 'Deploy' })).toBeEnabled({ timeout: 60000 });
            await expect(onlookerForm.getByText(`Only @${REQUESTER_USERNAME} can respond`)).toBeVisible({ timeout: 30000 });
            await expect(onlookerForm.getByRole('button', { name: 'Deploy' })).toBeDisabled();

            await requesterForm.getByLabel('Reason').fill('Channel hotfix');
            await requesterForm.getByRole('button', { name: 'Deploy' }).click();

            const thread = onlookerPage.locator('#rhsContainer');
            await expect(thread.getByText('Submitted Deploy from the form in the previous reply')).toBeVisible({ timeout: 30000 });
            await expect(thread.locator('[data-testid="llm-bot-post"]').last()).toContainText(SUBMISSION_ANSWER, { timeout: 60000 });
            await expect(onlookerForm.getByText('Submitted', { exact: true })).toBeVisible();
        } finally {
            await requesterContext.close();
            await onlookerContext.close();
        }
    });
});
