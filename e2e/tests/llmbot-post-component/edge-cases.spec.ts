import { test, expect, Page } from '@playwright/test';

import { AIMockHarness, RunAIMockHarness, setupAimockTestPage } from 'helpers/aimock-harness';

const PHASE3_EMPTY_REASONING_PROMPT = 'phase3-empty-reasoning-001';
const PHASE3_EMPTY_REASONING_ANSWER = 'Empty reasoning answer marker four.';

const PHASE3_LONG_REASONING_PROMPT = 'phase3-long-reasoning-001';
const PHASE3_LONG_REASONING_MARKER = 'Long reasoning marker alpha';

const PHASE3_RAPID_TOGGLE_PROMPT = 'phase3-rapid-toggle-001';

const PHASE3_SPECIAL_CHARS_PROMPT = 'phase3-special-chars-001';
const PHASE3_SPECIAL_CHARS_MARKER = '<tag>';

const PHASE3_CONCURRENT_ONE_PROMPT = 'phase3-concurrent-one-001';
const PHASE3_CONCURRENT_TWO_PROMPT = 'phase3-concurrent-two-001';
const PHASE3_CONCURRENT_THREE_PROMPT = 'phase3-concurrent-three-001';
const PHASE3_CONCURRENT_MARKER_ONE = 'Concurrent marker one';
const PHASE3_CONCURRENT_MARKER_TWO = 'Concurrent marker two';
const PHASE3_CONCURRENT_MARKER_THREE = 'Concurrent marker three';

const PHASE3_EMPTY_POST_PROMPT = 'phase3-empty-post-001';
const PHASE3_EMPTY_POST_FALLBACK = 'Sorry! The LLM did not return a result.';

const PHASE3_UNICODE_PROMPT = 'phase3-unicode-001';
const PHASE3_UNICODE_MARKER = 'Unicode marker';

const PHASE3_LARGE_REASONING_PROMPT = 'phase3-large-reasoning-001';
const PHASE3_LARGE_CONTENT_MARKER = 'Large post content marker';
const PHASE3_LARGE_REASONING_MARKER = 'Large post reasoning marker';

const PHASE3_NETWORK_ERROR_PROMPT = 'phase3-network-error-001';
const PHASE3_NETWORK_RECOVERY_PROMPT = 'phase3-network-recovery-001';
const PHASE3_NETWORK_ERROR_MESSAGE = 'Sorry! An error occurred while accessing the LLM';
const PHASE3_NETWORK_RECOVERY_MARKER = 'Network recovery marker';

const PHASE3_RAPID_MSG_ONE_PROMPT = 'phase3-rapid-msg-one-001';
const PHASE3_RAPID_MSG_TWO_PROMPT = 'phase3-rapid-msg-two-001';
const PHASE3_RAPID_MSG_THREE_PROMPT = 'phase3-rapid-msg-three-001';
const PHASE3_RAPID_MSG_MARKER_ONE = 'Rapid msg marker one';
const PHASE3_RAPID_MSG_MARKER_TWO = 'Rapid msg marker two';
const PHASE3_RAPID_MSG_MARKER_THREE = 'Rapid msg marker three';

async function waitForPostTextCount(page: Page, minCount: number, maxTimeout = 120000): Promise<void> {
    const allPosts = page.locator('[data-testid="posttext"]');
    const startTime = Date.now();

    while (Date.now() - startTime < maxTimeout) {
        const count = await allPosts.count();
        if (count >= minCount) {
            await page.waitForTimeout(1000);
            return;
        }
        await page.waitForTimeout(500);
    }

    const finalCount = await allPosts.count();
    expect(finalCount).toBeGreaterThanOrEqual(minCount);
}

async function expectRhsContainsMarkers(page: Page, markers: string[]): Promise<void> {
    const rhsText = await page.getByTestId('mattermost-ai-rhs').textContent();
    for (const marker of markers) {
        expect(rhsText).toContain(marker);
    }
}

test.describe('Edge Cases - aimock', () => {
    test.describe.configure({ mode: 'serial' });

    let harness: AIMockHarness;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        harness = await RunAIMockHarness({
            fixtureFile: 'llmbot-edge-cases.json',
            bot: {
                enabledNativeTools: [],
            },
        });
    });

    test.afterAll(async () => {
        await harness?.stop();
    });

    test('Empty Reasoning Response', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_EMPTY_REASONING_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectPostText(PHASE3_EMPTY_REASONING_ANSWER);

        const reasoning = llmBotHelper.getReasoningDisplay();
        await expect(reasoning).not.toBeVisible();
    });

    test('Empty Post Content', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_EMPTY_POST_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectPostText(PHASE3_EMPTY_POST_FALLBACK);
    });

    test('Very Long Reasoning Content', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_LONG_REASONING_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);
        await llmBotHelper.expectReasoningText(PHASE3_LONG_REASONING_MARKER);
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(false);
    });

    test('Special Characters in Response', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_SPECIAL_CHARS_PROMPT);
        await llmBotHelper.waitForStreamingComplete();

        const postText = llmBotHelper.getPostText();
        const content = await postText.textContent();
        expect(content).toContain(PHASE3_SPECIAL_CHARS_MARKER);
        expect(content).toContain('&');
        expect(content).toContain('|');
    });

    test('Unicode Content', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_UNICODE_PROMPT);
        await llmBotHelper.waitForStreamingComplete();

        const postText = llmBotHelper.getPostText();
        const content = await postText.textContent();
        expect(content).toContain(PHASE3_UNICODE_MARKER);
        expect(content).toContain('🚀');
    });

    test('Concurrent Posts', async ({ page }) => {
        test.setTimeout(180000);

        const { aiPlugin } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_CONCURRENT_ONE_PROMPT);
        await page.waitForTimeout(2000);
        await aiPlugin.sendMessage(PHASE3_CONCURRENT_TWO_PROMPT);
        await page.waitForTimeout(2000);
        await aiPlugin.sendMessage(PHASE3_CONCURRENT_THREE_PROMPT);

        await waitForPostTextCount(page, 3);

        const allPosts = page.locator('[data-testid="posttext"]');
        await expect(allPosts.first()).toBeVisible();
        await expect(allPosts.nth(1)).toBeVisible();
        await expect(allPosts.nth(2)).toBeVisible();

        await expectRhsContainsMarkers(page, [
            PHASE3_CONCURRENT_MARKER_ONE,
            PHASE3_CONCURRENT_MARKER_TWO,
            PHASE3_CONCURRENT_MARKER_THREE,
        ]);
    });

    test('Large Post with Reasoning', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_LARGE_REASONING_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectPostText(PHASE3_LARGE_CONTENT_MARKER);
        await llmBotHelper.expectReasoningVisible(true);
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);
        await llmBotHelper.expectReasoningText(PHASE3_LARGE_REASONING_MARKER);
    });

    test('Network Error Handling', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_NETWORK_ERROR_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectPostText(PHASE3_NETWORK_ERROR_MESSAGE);

        await aiPlugin.ensureRhsNewChatTab();
        await aiPlugin.sendMessage(PHASE3_NETWORK_RECOVERY_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectPostText(PHASE3_NETWORK_RECOVERY_MARKER);
    });

    test('Multiple Rapid Messages', async ({ page }) => {
        test.setTimeout(180000);

        const { aiPlugin } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_RAPID_MSG_ONE_PROMPT);
        await page.waitForTimeout(1000);
        await aiPlugin.sendMessage(PHASE3_RAPID_MSG_TWO_PROMPT);
        await page.waitForTimeout(1000);
        await aiPlugin.sendMessage(PHASE3_RAPID_MSG_THREE_PROMPT);

        await waitForPostTextCount(page, 3);

        const allPosts = page.locator('[data-testid="posttext"]');
        const count = await allPosts.count();
        expect(count).toBeGreaterThanOrEqual(3);

        for (let i = 0; i < Math.min(count, 3); i++) {
            await expect(allPosts.nth(i)).toBeVisible();
        }

        await expectRhsContainsMarkers(page, [
            PHASE3_RAPID_MSG_MARKER_ONE,
            PHASE3_RAPID_MSG_MARKER_TWO,
            PHASE3_RAPID_MSG_MARKER_THREE,
        ]);
    });

    test('Rapid Reasoning Toggle', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_RAPID_TOGGLE_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectReasoningVisible(true);

        for (let i = 0; i < 5; i++) {
            await llmBotHelper.clickReasoningToggle();
            await page.waitForTimeout(100);
            await llmBotHelper.clickReasoningToggle();
            await page.waitForTimeout(100);
        }

        await llmBotHelper.expectReasoningVisible(true);
    });
});
