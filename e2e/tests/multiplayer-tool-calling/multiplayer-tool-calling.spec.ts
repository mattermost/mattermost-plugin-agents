// spec: tests/multiplayer-tool-calling/multiplayer-tool-calling.plan.md
// seed: tests/seed.spec.ts

import { test, expect, Page } from '@playwright/test';
import fs from 'fs';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import {
    getAPIConfig,
    getSkipMessage,
    getAvailableProviders,
    ProviderBundle,
} from 'helpers/api-config';
import { checkAPIHealth } from 'helpers/api-health-check';
import { attachAPIErrorContext } from 'helpers/log-scanner';

/**
 * Test Suite: Multiplayer Tool Calling
 *
 * Tests the two-stage tool approval flow in public channels with two users:
 * - Invoker: the user who @mentions the bot (sees full details, can approve/reject)
 * - Onlooker: another channel member (sees redacted tool info only)
 *
 * IMPORTANT: The LLM may make multiple sequential tool calls (e.g. get_channel_info
 * then create_post). Each tool call goes through its own Accept → Share cycle.
 * Tests must loop through ALL rounds until the bot generates its final text response.
 *
 * Bot responses to @mentions happen in a thread — the test must open the thread.
 */

const invokerUsername = 'regularuser';
const invokerPassword = 'regularuser';
const onlookerUsername = 'seconduser';
const onlookerPassword = 'seconduser';

const config = getAPIConfig();
const skipMessage = getSkipMessage();

// ==================== CONTAINER SETUP ====================

async function setupToolCallingContainer(provider: ProviderBundle): Promise<MattermostContainer> {
    let filename = '';
    fs.readdirSync('../dist/').forEach(file => {
        if (file.endsWith('.tar.gz')) {
            filename = '../dist/' + file;
        }
    });

    const botConfig = {
        ...provider.bot,
        disableTools: false,
        enabledNativeTools: [],
        customInstructions: 'You have access to Mattermost tools including create_post and get_channel_info. When a user asks you to post a message or create a post in a channel, you MUST use get_channel_info to find the channel ID first, then use create_post to create the post. Always use your tools when the user asks you to take action. Never refuse to use tools.',
    };

    const pluginConfig = {
        config: {
            allowPrivateChannels: true,
            disableFunctionCalls: false,
            enableLLMTrace: true,
            enableUserRestrictions: false,
            defaultBotName: botConfig.name,
            enableVectorIndex: true,
            enableChannelMentionToolCalling: true,
            mcp: {
                embeddedServer: { enabled: true },
                enablePluginServer: true,
                enabled: true,
                idleTimeoutMinutes: 30,
                servers: null,
            },
            services: [provider.service],
            bots: [botConfig],
        },
    };

    const mattermost = await new MattermostContainer()
        .withPlugin(filename, 'mattermost-ai', pluginConfig)
        .start();

    await mattermost.createUser('regularuser@sample.com', invokerUsername, invokerPassword);
    await mattermost.addUserToTeam(invokerUsername, 'test');
    await mattermost.createUser('seconduser@sample.com', onlookerUsername, onlookerPassword);
    await mattermost.addUserToTeam(onlookerUsername, 'test');

    for (const [uname, pwd] of [[invokerUsername, invokerPassword], [onlookerUsername, onlookerPassword]]) {
        const client = await mattermost.getClient(uname, pwd);
        const user = await client.getMe();
        await client.savePreferences(user.id, [
            { user_id: user.id, category: 'tutorial_step', name: user.id, value: '999' },
            { user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
            { user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
            { user_id: user.id, category: 'drafts', name: 'drafts_tour_tip_showed', value: JSON.stringify({ drafts_tour_tip_showed: true }) },
            { user_id: user.id, category: 'crt_thread_pane_step', name: user.id, value: '999' },
        ]);
    }

    const adminClient = await mattermost.getAdminClient();
    const admin = await adminClient.getMe();
    await adminClient.savePreferences(admin.id, [
        { user_id: admin.id, category: 'tutorial_step', name: admin.id, value: '999' },
        { user_id: admin.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
        { user_id: admin.id, category: 'onboarding_task_list', name: 'onboarding_task_list_open', value: 'false' },
        { user_id: admin.id, category: 'drafts', name: 'drafts_tour_tip_showed', value: JSON.stringify({ drafts_tour_tip_showed: true }) },
        { user_id: admin.id, category: 'crt_thread_pane_step', name: admin.id, value: '999' },
    ]);

    await adminClient.completeSetup({ organization: 'test', install_plugins: [] });
    await new Promise(resolve => setTimeout(resolve, 2000));

    return mattermost;
}

// ==================== HELPERS ====================

/**
 * Capture the current count of reply indicators on the page.
 * Call this BEFORE sending the bot message so we can detect the NEW reply later.
 */
async function captureReplyCount(page: Page): Promise<number> {
    return await page.getByText(/\d+ repl/).count().catch(() => 0);
}

/**
 * Wait for the bot to reply in a thread, then open it.
 * Uses count-based detection: compares the current reply indicator count against
 * a baseline captured BEFORE the bot message was sent. This avoids race conditions
 * where the bot replies before the baseline is captured.
 *
 * NOTE: Only use this on the page that SENT the message (the invoker).
 * For onlooker pages where the reply is already visible, use openLatestThread().
 *
 * @param page - The invoker's page
 * @param countBeforeSend - The reply indicator count captured BEFORE mentionBot()
 * @param timeout - How long to wait for the new reply (default 120s)
 */
async function waitForReplyAndOpenThread(page: Page, countBeforeSend: number, timeout: number = 120000): Promise<void> {
    const replyIndicator = page.getByText(/\d+ repl/);

    const startTime = Date.now();
    while (Date.now() - startTime < timeout) {
        const currentCount = await replyIndicator.count().catch(() => 0);
        if (currentCount > countBeforeSend) {
            // A new reply indicator appeared — click the last (most recent) one
            const lastIndicator = replyIndicator.last();
            const isVisible = await lastIndicator.isVisible().catch(() => false);
            if (isVisible) {
                await lastIndicator.click();
                await page.locator('#rhsContainer').waitFor({ state: 'visible', timeout: 10000 });
                await page.waitForTimeout(1000);
                return;
            }
        }
        await page.waitForTimeout(1000);
    }
    throw new Error('Timeout waiting for bot reply in thread');
}

/**
 * Open the most recent thread on a page where the reply is already visible.
 * Used for onlooker pages in dual-browser tests — the invoker already triggered
 * the bot response, so the reply indicator is already present on this page.
 */
async function openLatestThread(page: Page, timeout: number = 30000): Promise<void> {
    const replyIndicator = page.getByText(/\d+ repl/);
    await expect(replyIndicator.last()).toBeVisible({ timeout });
    await replyIndicator.last().click();
    await page.locator('#rhsContainer').waitFor({ state: 'visible', timeout: 10000 });
    await page.waitForTimeout(1000);
}

/**
 * Wait for a specific button to appear in the RHS thread panel.
 * Returns true if found, false if timeout (when throwOnTimeout is false).
 */
async function waitForButtonInThread(page: Page, buttonName: string, timeout: number = 120000, throwOnTimeout: boolean = true): Promise<boolean> {
    const startTime = Date.now();
    const rhs = page.locator('#rhsContainer');
    while (Date.now() - startTime < timeout) {
        const button = rhs.getByRole('button', { name: buttonName });
        const isVisible = await button.first().isVisible().catch(() => false);
        if (isVisible) {
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

/**
 * Complete one full tool call round: Accept all → Share all.
 * Returns true if a round was completed, false if no Accept button appeared
 * (meaning the LLM is done making tool calls).
 */
async function completeOneToolCallRound(page: Page, action: 'accept-share' | 'accept-keep-private' | 'reject'): Promise<boolean> {
    const rhs = page.locator('#rhsContainer');

    // Wait for Accept buttons to appear (tool call stage)
    const hasAccept = await waitForButtonInThread(page, 'Accept', 120000, false);
    if (!hasAccept) {
        return false; // No more tool calls — the LLM is done
    }

    if (action === 'reject') {
        // Reject all tool calls in this round
        const rejectButtons = rhs.getByRole('button', { name: 'Reject' });
        const count = await rejectButtons.count();
        for (let i = 0; i < count; i++) {
            await rejectButtons.nth(i).click();
        }
        await page.waitForTimeout(2000);
        return true;
    }

    // Accept all tool calls in this round
    const acceptButtons = rhs.getByRole('button', { name: 'Accept' });
    const acceptCount = await acceptButtons.count();
    for (let i = 0; i < acceptCount; i++) {
        await acceptButtons.nth(i).click();
    }

    // Wait for Share/Keep private buttons (result stage)
    await waitForButtonInThread(page, 'Share', 120000);

    if (action === 'accept-share') {
        const shareButtons = rhs.getByRole('button', { name: 'Share' });
        const shareCount = await shareButtons.count();
        for (let i = 0; i < shareCount; i++) {
            await shareButtons.nth(i).click();
        }
    } else {
        // accept-keep-private
        const keepPrivateButtons = rhs.getByRole('button', { name: 'Keep private' });
        const keepPrivateCount = await keepPrivateButtons.count();
        for (let i = 0; i < keepPrivateCount; i++) {
            await keepPrivateButtons.nth(i).click();
        }
    }

    await page.waitForTimeout(2000);
    return true;
}

/**
 * Loop through ALL tool call rounds, accepting and sharing each one,
 * until the LLM stops proposing new tool calls.
 */
async function completeAllToolCallRounds(page: Page, action: 'accept-share' | 'accept-keep-private' | 'reject'): Promise<number> {
    let rounds = 0;
    const maxRounds = 10; // Safety limit

    while (rounds < maxRounds) {
        const completed = await completeOneToolCallRound(page, action);
        if (!completed) {
            break; // No more tool calls
        }
        rounds++;

        // For reject, the LLM will still try subsequent tool calls, but we keep rejecting
        // until no more appear. Give it time to propose the next one.
        if (action === 'reject') {
            const hasMore = await waitForButtonInThread(page, 'Accept', 10000, false);
            if (!hasMore) {
                break;
            }
        }
    }

    return rounds;
}

/**
 * Wait for the bot to finish responding in the thread after all tool calls.
 * Polls until no Accept/Share/Keep private buttons remain and no stop button is visible.
 */
async function waitForBotFinished(page: Page, timeout: number = 120000): Promise<void> {
    const startTime = Date.now();
    const rhs = page.locator('#rhsContainer');

    while (Date.now() - startTime < timeout) {
        const hasAccept = await rhs.getByRole('button', { name: 'Accept' }).first().isVisible().catch(() => false);
        const hasShare = await rhs.getByRole('button', { name: 'Share' }).first().isVisible().catch(() => false);
        const hasKeepPrivate = await rhs.getByRole('button', { name: 'Keep private' }).first().isVisible().catch(() => false);
        const hasStop = await rhs.getByRole('button', { name: /stop/i }).first().isVisible().catch(() => false);

        if (!hasAccept && !hasShare && !hasKeepPrivate && !hasStop) {
            await page.waitForTimeout(2000); // Let final UI settle
            return;
        }
        await page.waitForTimeout(1000);
    }
    throw new Error('Timeout waiting for bot to finish in thread');
}

async function waitForPageReady(page: Page): Promise<void> {
    await page.waitForSelector('[class*="channel-header"], #channelHeaderInfo', { timeout: 30000 });
    await page.waitForTimeout(2000);
}

async function navigateToChannel(page: Page, mattermost: MattermostContainer, channelName: string = 'off-topic'): Promise<void> {
    await page.goto(mattermost.url() + `/test/channels/${channelName}`);
    await waitForPageReady(page);
}

// ==================== TESTS ====================

function createProviderTestSuite(provider: ProviderBundle) {
    test.describe(`Multiplayer Tool Calling - ${provider.name}`, () => {
        let mattermost: MattermostContainer;

        test.beforeAll(async () => {
            if (!config.shouldRunTests) return;
            await checkAPIHealth(provider.service);
            mattermost = await setupToolCallingContainer(provider);
        });

        test.afterAll(async () => {
            if (mattermost) {
                await mattermost.stop();
            }
        });

        test.afterEach(async ({}, testInfo) => {
            await attachAPIErrorContext(testInfo);
        });

        test('Happy path: Accept and Share all tool calls (invoker + onlooker)', async ({ browser }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(480000);

            const invokerContext = await browser.newContext();
            const onlookerContext = await browser.newContext();
            const invokerPage = await invokerContext.newPage();
            const onlookerPage = await onlookerContext.newPage();

            try {
                const invokerMM = new MattermostPage(invokerPage);
                const onlookerMM = new MattermostPage(onlookerPage);
                const botUsername = provider.bot.name;

                await invokerMM.login(mattermost.url(), invokerUsername, invokerPassword);
                await onlookerMM.login(mattermost.url(), onlookerUsername, onlookerPassword);
                await navigateToChannel(invokerPage, mattermost, 'off-topic');
                await navigateToChannel(onlookerPage, mattermost, 'off-topic');

                // Capture reply count BEFORE sending so we can detect the new reply
                const replyCountBeforeSend = await captureReplyCount(invokerPage);

                // Trigger tool call
                await invokerMM.mentionBot(botUsername, 'Post a message saying "Hello from the AI agent - happy path test" in the Town Square channel. Use the create_post tool.');

                // Open thread on invoker page (waits for NEW reply indicator)
                await waitForReplyAndOpenThread(invokerPage, replyCountBeforeSend);
                // Open thread on onlooker page (reply already visible via websocket)
                await openLatestThread(onlookerPage);

                // Before proceeding, check the onlooker's view on the FIRST tool call round
                await waitForButtonInThread(invokerPage, 'Accept');

                // === ONLOOKER ASSERTIONS (first call stage) ===
                const onlookerRhs = onlookerPage.locator('#rhsContainer');
                const onlookerBotPost = onlookerRhs.locator('[data-testid="llm-bot-post"]');
                await expect(onlookerBotPost.last()).toBeVisible({ timeout: 30000 });
                await expect(onlookerRhs.getByRole('button', { name: 'Accept' })).not.toBeVisible();
                await expect(onlookerRhs.getByRole('button', { name: 'Reject' })).not.toBeVisible();

                // === COMPLETE ALL TOOL CALL ROUNDS (accept + share each one) ===
                const rounds = await completeAllToolCallRounds(invokerPage, 'accept-share');
                expect(rounds).toBeGreaterThanOrEqual(1);

                // Wait for the bot to finish (no more approval buttons, no stop button)
                await waitForBotFinished(invokerPage);

                // Onlooker should NOT see approval buttons after completion
                await onlookerPage.waitForTimeout(3000);
                await expect(onlookerRhs.getByRole('button', { name: 'Accept' })).not.toBeVisible();
                await expect(onlookerRhs.getByRole('button', { name: 'Share' })).not.toBeVisible();

                // Verify the post was actually created in town-square
                await navigateToChannel(invokerPage, mattermost, 'town-square');
                const createdPost = invokerPage.locator('.post-message__text').getByText('Hello from the AI agent - happy path test');
                await expect(createdPost).toBeVisible({ timeout: 10000 });
            } finally {
                await invokerContext.close();
                await onlookerContext.close();
            }
        });

        test('Reject tool calls at call stage', async ({ browser }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(480000);

            const context = await browser.newContext();
            const page = await context.newPage();

            try {
                const mmPage = new MattermostPage(page);
                const botUsername = provider.bot.name;

                await mmPage.login(mattermost.url(), invokerUsername, invokerPassword);
                await navigateToChannel(page, mattermost, 'off-topic');

                // Capture reply count BEFORE sending so we can detect the new reply
                const replyCountBeforeSend = await captureReplyCount(page);

                // Trigger tool call
                await mmPage.mentionBot(botUsername, 'Post a message saying "This should be rejected" in the Town Square channel. Use the create_post tool.');

                // Open the thread
                await waitForReplyAndOpenThread(page, replyCountBeforeSend);

                // Reject ALL tool call rounds
                const rounds = await completeAllToolCallRounds(page, 'reject');
                expect(rounds).toBeGreaterThanOrEqual(1);

                // Wait for the bot to finish (it may produce a text response after rejection)
                await page.waitForTimeout(5000);

                const rhs = page.locator('#rhsContainer');

                // Should NOT see Share/Keep private buttons
                await expect(rhs.getByRole('button', { name: 'Share' })).not.toBeVisible();
                await expect(rhs.getByRole('button', { name: 'Keep private' })).not.toBeVisible();

                // The "Rejected" text should appear in the thread
                await expect(rhs.getByText('Rejected')).toBeVisible({ timeout: 10000 });

                // Verify the post was NOT created in town-square
                await navigateToChannel(page, mattermost, 'town-square');
                await expect(page.getByText('This should be rejected')).not.toBeVisible();
            } finally {
                await context.close();
            }
        });

        test('Accept tool calls then Keep Private', async ({ browser }) => {
            test.skip(!config.shouldRunTests, skipMessage);
            test.setTimeout(480000);

            const invokerContext = await browser.newContext();
            const onlookerContext = await browser.newContext();
            const invokerPage = await invokerContext.newPage();
            const onlookerPage = await onlookerContext.newPage();

            try {
                const invokerMM = new MattermostPage(invokerPage);
                const onlookerMM = new MattermostPage(onlookerPage);
                const botUsername = provider.bot.name;

                await invokerMM.login(mattermost.url(), invokerUsername, invokerPassword);
                await onlookerMM.login(mattermost.url(), onlookerUsername, onlookerPassword);
                await navigateToChannel(invokerPage, mattermost, 'off-topic');
                await navigateToChannel(onlookerPage, mattermost, 'off-topic');

                // Capture reply count BEFORE sending so we can detect the new reply
                const replyCountBeforeSend = await captureReplyCount(invokerPage);

                // Trigger tool call
                await invokerMM.mentionBot(botUsername, 'Post a message saying "This result stays private" in the Town Square channel. Use the create_post tool.');

                // Open thread on invoker page (waits for NEW reply indicator)
                await waitForReplyAndOpenThread(invokerPage, replyCountBeforeSend);
                // Open thread on onlooker page (reply already visible via websocket)
                await openLatestThread(onlookerPage);

                const rhs = invokerPage.locator('#rhsContainer');

                // The LLM will make multiple tool calls: get_channel_info then create_post.
                // We need to Accept+Share the intermediate calls (so the LLM gets the data
                // it needs for the next call), but Keep Private the LAST call's result.
                //
                // Strategy: Accept+Share rounds until we see create_post being called,
                // then Accept+Keep Private that final round.
                let rounds = 0;
                const maxRounds = 10;
                while (rounds < maxRounds) {
                    const hasAccept = await waitForButtonInThread(invokerPage, 'Accept', 120000, false);
                    if (!hasAccept) break;

                    // Accept all tool calls in this round
                    const acceptButtons = rhs.getByRole('button', { name: 'Accept' });
                    const acceptCount = await acceptButtons.count();
                    for (let i = 0; i < acceptCount; i++) {
                        await acceptButtons.nth(i).click();
                    }

                    // Wait for Share/Keep private
                    await waitForButtonInThread(invokerPage, 'Share', 120000);
                    rounds++;

                    // Peek: does the thread contain text suggesting this is a create_post result?
                    // If we see "Successfully created post" or similar, this is the final round.
                    const resultText = rhs.getByText(/created post|post.*created|Successfully/i);
                    const isCreatePostResult = await resultText.first().isVisible().catch(() => false);

                    if (isCreatePostResult) {
                        // This is the create_post result — Keep Private
                        const keepPrivateButtons = rhs.getByRole('button', { name: 'Keep private' });
                        const keepPrivateCount = await keepPrivateButtons.count();
                        for (let i = 0; i < keepPrivateCount; i++) {
                            await keepPrivateButtons.nth(i).click();
                        }
                        await invokerPage.waitForTimeout(2000);
                        break;
                    } else {
                        // Intermediate round (e.g. get_channel_info) — Share so LLM can continue
                        const shareButtons = rhs.getByRole('button', { name: 'Share' });
                        const shareCount = await shareButtons.count();
                        for (let i = 0; i < shareCount; i++) {
                            await shareButtons.nth(i).click();
                        }
                        await invokerPage.waitForTimeout(2000);
                    }
                }
                expect(rounds).toBeGreaterThanOrEqual(1);

                // Wait for the flow to settle
                await waitForBotFinished(invokerPage);

                // Onlooker should NOT see any approval buttons
                const onlookerRhs = onlookerPage.locator('#rhsContainer');
                await expect(onlookerRhs.getByRole('button', { name: 'Share' })).not.toBeVisible();
                await expect(onlookerRhs.getByRole('button', { name: 'Keep private' })).not.toBeVisible();
                await expect(onlookerRhs.getByRole('button', { name: 'Accept' })).not.toBeVisible();

                // Invoker should still see the bot post in the thread
                const invokerBotPost = rhs.locator('[data-testid="llm-bot-post"]');
                await expect(invokerBotPost.last()).toBeVisible();

                // The tool DID execute (it ran after Accept), so the post exists in town-square.
                // What's "private" is the result sharing — the bot won't use it in its response.
                await navigateToChannel(invokerPage, mattermost, 'town-square');
                const privatePost = invokerPage.locator('.post-message__text').getByText('This result stays private');
                await expect(privatePost).toBeVisible({ timeout: 10000 });
            } finally {
                await invokerContext.close();
                await onlookerContext.close();
            }
        });
    });
}

const providers = getAvailableProviders();
providers.forEach(provider => {
    createProviderTestSuite(provider);
});
