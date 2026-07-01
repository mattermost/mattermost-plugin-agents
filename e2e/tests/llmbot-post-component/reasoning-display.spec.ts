import { test, expect } from '@playwright/test';

import { AIMockHarness, RunAIMockHarness, setupAimockTestPage } from 'helpers/aimock-harness';

const PHASE3_REASONING_RENDER_PROMPT = 'phase3-reasoning-render-001';
const PHASE3_REASONING_RENDER_REASONING = 'Reasoning render marker';

const PHASE3_REASONING_TOGGLE_PROMPT = 'phase3-reasoning-toggle-001';
const PHASE3_REASONING_TOGGLE_REASONING = 'Reasoning toggle marker';

const PHASE3_REASONING_PERSIST_PROMPT = 'phase3-reasoning-persist-001';
const PHASE3_REASONING_PERSIST_REASONING = 'Reasoning persist marker';

const PHASE3_REASONING_COMPLETE_PROMPT = 'phase3-reasoning-complete-001';

const PHASE3_REASONING_MULTI_FIRST_PROMPT = 'phase3-reasoning-multi-first-001';
const PHASE3_REASONING_MULTI_SECOND_PROMPT = 'phase3-reasoning-multi-second-001';

test.describe('Reasoning Display - aimock', () => {
    test.describe.configure({ mode: 'serial' });

    let harness: AIMockHarness;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        harness = await RunAIMockHarness({
            fixtureFile: 'llmbot-reasoning-display.json',
            bot: {
                reasoningEnabled: true,
                reasoningEffort: 'high',
                thinkingBudget: 0,
                enabledNativeTools: [],
            },
        });
    });

    test.afterAll(async () => {
        await harness?.stop();
    });

    test('Reasoning Display - Renders from aimock', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_REASONING_RENDER_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await expect(llmBotHelper.getReasoningLabel()).toBeVisible();
        await llmBotHelper.expectReasoningExpanded(false);

        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);
        await llmBotHelper.expectReasoningText(PHASE3_REASONING_RENDER_REASONING);
    });

    test('Reasoning Toggle - Expand and Collapse', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_REASONING_TOGGLE_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await llmBotHelper.expectReasoningExpanded(false);

        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);
        await llmBotHelper.expectReasoningText(PHASE3_REASONING_TOGGLE_REASONING);

        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(false);
        await expect(llmBotHelper.getReasoningLabel()).toBeVisible();
    });

    test('Reasoning States - Complete State', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_REASONING_COMPLETE_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await expect(llmBotHelper.getReasoningLabel()).toBeVisible();
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(false);
        await llmBotHelper.expectReasoningVisible(true);
    });

    test('Multiple Posts with Reasoning', async ({ page }) => {
        test.setTimeout(180000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_REASONING_MULTI_FIRST_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectReasoningVisible(true);
        await expect(llmBotHelper.getReasoningLabel()).toBeVisible();

        await aiPlugin.sendMessage(PHASE3_REASONING_MULTI_SECOND_PROMPT);

        const allReasoningDisplays = llmBotHelper.getAllReasoningDisplays();
        const startTime = Date.now();
        const maxTimeout = 120000;

        while (Date.now() - startTime < maxTimeout) {
            const count = await allReasoningDisplays.count();
            if (count >= 2) {
                await page.waitForTimeout(1000);
                break;
            }
            await page.waitForTimeout(500);
        }

        const finalCount = await allReasoningDisplays.count();
        expect(finalCount).toBeGreaterThanOrEqual(2);

        const firstReasoning = allReasoningDisplays.first();
        await firstReasoning.scrollIntoViewIfNeeded();
        await page.waitForTimeout(500);
        await firstReasoning.click();

        const secondReasoning = allReasoningDisplays.nth(1);
        await secondReasoning.scrollIntoViewIfNeeded();
        await page.waitForTimeout(500);
        await secondReasoning.click();

        const countAfterClicks = await allReasoningDisplays.count();
        expect(countAfterClicks).toBeGreaterThanOrEqual(2);
    });
});

test.describe('Reasoning Persistence After Refresh - aimock', () => {
    let harness: AIMockHarness;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        harness = await RunAIMockHarness({
            fixtureFile: 'llmbot-reasoning-display.json',
            bot: {
                reasoningEnabled: true,
                reasoningEffort: 'high',
                thinkingBudget: 0,
                enabledNativeTools: [],
            },
        });
    });

    test.afterAll(async () => {
        await harness?.stop();
    });

    test('Reasoning Persistence After Refresh', async ({ page }) => {
        test.setTimeout(120000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_REASONING_PERSIST_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await llmBotHelper.expectReasoningExpanded(false);
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);

        await page.reload();
        await aiPlugin.openRHS();
        await page.waitForTimeout(2000);

        await aiPlugin.openChatHistory();
        await page.waitForTimeout(1000);
        await aiPlugin.clickChatHistoryItem(0);
        await page.waitForTimeout(2000);

        await llmBotHelper.expectReasoningVisible(true);
        await llmBotHelper.expectReasoningExpanded(false);
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);
        await llmBotHelper.expectReasoningText(PHASE3_REASONING_PERSIST_REASONING);
    });
});
