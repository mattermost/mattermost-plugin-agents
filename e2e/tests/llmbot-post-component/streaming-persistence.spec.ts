import { test, expect, Page } from '@playwright/test';

import { AIMockHarness, RunAIMockHarness, setupAimockTestPage } from 'helpers/aimock-harness';
import { MattermostPage } from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import { AIMOCK_BOT_NAME } from 'helpers/plugincontainer';

const PHASE3_STREAMING_CURSOR_PROMPT = 'phase3-streaming-cursor-001';
const PHASE3_STREAMING_CURSOR_MARKER = 'Streaming cursor marker part one two three complete.';

const PHASE3_STREAMING_LIFECYCLE_PROMPT = 'phase3-streaming-lifecycle-001';
const PHASE3_STREAMING_LIFECYCLE_ANSWER = 'Streaming lifecycle answer complete.';

const PHASE3_NAV_PERSISTENCE_PROMPT = 'phase3-nav-persistence-001';
const PHASE3_NAV_PERSISTENCE_MARKER = 'Navigation persistence marker rhs close reopen.';

const PHASE3_THREAD_PERSIST_PROMPT = 'phase3-thread-persist-001';

const PHASE3_STOP_GENERATING_PROMPT = 'phase3-stop-generating-001';
const PHASE3_STOP_GENERATING_PREFIX = 'Stop generating prefix marker';
const PHASE3_STOP_GENERATING_TAIL = 'large scale application development.';

const REQUESTER_USERNAME = 'regularuser';
const ONLOOKER_USERNAME = 'seconduser';
const REQUESTER_STOPPED_NOTICE = 'You stopped generating this response.';
const ONLOOKER_STOPPED_NOTICE = `${REQUESTER_USERNAME} stopped generating this response.`;

async function openReplyThread(page: Page): Promise<void> {
    const replyIndicator = page.getByText(/\d+ repl/).last();
    await expect(replyIndicator).toBeVisible({ timeout: 60000 });
    await replyIndicator.click();
    await expect(page.locator('#rhsContainer')).toBeVisible({ timeout: 10000 });
}

// The standard harness skips onboarding only for regularuser.
async function skipOnlookerOnboarding(mattermost: MattermostContainer): Promise<void> {
    const client = await mattermost.getClient(ONLOOKER_USERNAME, ONLOOKER_USERNAME);
    const me = await client.getMe();
    await client.savePreferences(me.id, [
        { user_id: me.id, category: 'tutorial_step', name: me.id, value: '999' },
        { user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
        { user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
        { user_id: me.id, category: 'drafts', name: 'drafts_tour_tip_showed', value: JSON.stringify({ drafts_tour_tip_showed: true }) },
        { user_id: me.id, category: 'crt_thread_pane_step', name: me.id, value: '999' },
    ]);
}

test.describe('Streaming and Persistence - aimock', () => {
    test.describe.configure({ mode: 'serial' });

    let harness: AIMockHarness;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        harness = await RunAIMockHarness({
            fixtureFile: 'llmbot-streaming-persistence.json',
            bot: {
                enabledNativeTools: [],
            },
        });
    });

    test.afterAll(async () => {
        await harness?.stop();
    });

    test('Streaming Cursor Display', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_STREAMING_CURSOR_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectPostText(PHASE3_STREAMING_CURSOR_MARKER);
    });

    test('Streaming Complete Lifecycle', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_STREAMING_LIFECYCLE_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await llmBotHelper.expectPostText(PHASE3_STREAMING_LIFECYCLE_ANSWER);

        const stopButton = llmBotHelper.getStopGeneratingButton();
        await expect(stopButton).not.toBeVisible();
    });

    test('Navigation Persistence', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_NAV_PERSISTENCE_PROMPT);
        await llmBotHelper.waitForStreamingComplete();

        const postTextBefore = llmBotHelper.getPostText();
        const contentBefore = await postTextBefore.textContent();
        expect(contentBefore).toContain(PHASE3_NAV_PERSISTENCE_MARKER);

        await aiPlugin.closeRHS();
        await expect(page.getByTestId('mattermost-ai-rhs')).toBeHidden();
        await aiPlugin.openRHS();

        const postTextAfter = llmBotHelper.getPostText();
        await expect(postTextAfter).toBeVisible();
        const contentAfter = await postTextAfter.textContent();
        expect(contentAfter).toBe(contentBefore);
        expect(contentAfter).toContain(PHASE3_NAV_PERSISTENCE_MARKER);
    });

    test('Thread View Persistence', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_THREAD_PERSIST_PROMPT);
        await llmBotHelper.waitForStreamingComplete();

        const postTextBefore = llmBotHelper.getPostText();
        const contentBefore = await postTextBefore.textContent();
        expect(contentBefore).toBeTruthy();

        await page.reload();
        await aiPlugin.openRHS();

        await aiPlugin.openChatHistory();
        await aiPlugin.clickChatHistoryItem(0);

        const postTextAfter = llmBotHelper.getPostText();
        await expect(postTextAfter).toBeVisible();
        const contentAfter = await postTextAfter.textContent();
        expect(contentAfter).toBe(contentBefore);
    });
});

test.describe('Stop Generating Button - aimock', () => {
    let harness: AIMockHarness;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        harness = await RunAIMockHarness({
            fixtureFile: 'llmbot-streaming-persistence.json',
            bot: {
                enabledNativeTools: [],
            },
        });
        await skipOnlookerOnboarding(harness.mattermost);
    });

    test.afterAll(async () => {
        await harness?.stop();
    });

    test('Stop Generating Button', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_STOP_GENERATING_PROMPT);

        const postText = llmBotHelper.getPostText();
        await expect(postText).toBeVisible({ timeout: 60000 });

        // In the plugin RHS the composer's send button is the Stop control;
        // the post shows none of its own.
        const rhs = page.getByTestId('mattermost-ai-rhs');
        const stopButton = rhs.getByRole('button', { name: 'Stop generating' });
        await expect(stopButton).toBeVisible({ timeout: 30000 });
        await expect(llmBotHelper.getStopGeneratingButton()).toHaveCount(0);
        await expect
            .poll(async () => (await postText.textContent()) ?? '')
            .toContain(PHASE3_STOP_GENERATING_PREFIX);

        await stopButton.click();
        await expect(stopButton).toHaveCount(0, { timeout: 10000 });
        await expect(rhs.getByRole('button', { name: 'Send message' })).toBeVisible();

        const content = await postText.textContent();
        expect(content).toContain(PHASE3_STOP_GENERATING_PREFIX);
        expect(content).not.toContain(PHASE3_STOP_GENERATING_TAIL);
        await expect(page.getByTestId('llm-bot-generation-stopped')).toHaveText(REQUESTER_STOPPED_NOTICE);
    });

    test('Stop in a channel is shown to the requester and onlookers', async ({ browser }) => {
        test.setTimeout(180000);

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

            await requester.mentionBot(AIMOCK_BOT_NAME, PHASE3_STOP_GENERATING_PROMPT);
            await openReplyThread(requesterPage);
            await openReplyThread(onlookerPage);

            const requesterPost = requesterPage.locator('#rhsContainer [data-testid="llm-bot-post"]').last();
            const onlookerPost = onlookerPage.locator('#rhsContainer [data-testid="llm-bot-post"]').last();
            await expect(requesterPost).toContainText(PHASE3_STOP_GENERATING_PREFIX, { timeout: 60000 });
            await expect(onlookerPost).toContainText(PHASE3_STOP_GENERATING_PREFIX, { timeout: 30000 });
            await expect(onlookerPost.getByTestId('stop-generating-button')).toHaveCount(0);

            await requesterPost.getByTestId('stop-generating-button').click();

            await expect(requesterPost.getByTestId('llm-bot-generation-stopped')).toHaveText(REQUESTER_STOPPED_NOTICE, { timeout: 15000 });
            await expect(onlookerPost.getByTestId('llm-bot-generation-stopped')).toHaveText(ONLOOKER_STOPPED_NOTICE, { timeout: 15000 });
            await expect(onlookerPost).not.toContainText(PHASE3_STOP_GENERATING_TAIL);
            // The stop is persisted on the post, so a later visit still explains the cut-off reply.
            await onlookerPage.reload();
            await openReplyThread(onlookerPage);
            await expect(onlookerPage.locator('#rhsContainer [data-testid="llm-bot-post"]').last().getByTestId('llm-bot-generation-stopped'))
                .toHaveText(ONLOOKER_STOPPED_NOTICE, { timeout: 30000 });
        } finally {
            await requesterContext.close();
            await onlookerContext.close();
        }
    });
});
