import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';

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

const harnessTest = () => containerTest(async (own) => {
    const harness = own(await RunAIMockHarness({
        fixtureFile: 'llmbot-reasoning-display.json',
        bot: {
            reasoningEnabled: true,
            reasoningEffort: 'high',
            thinkingBudget: 0,
            enabledNativeTools: [],
        },
    }));
    return { harness };
});

const reasoningDisplayTest = harnessTest();

reasoningDisplayTest.describe('Reasoning Display - aimock', () => {
    reasoningDisplayTest.describe.configure({ mode: 'serial', retries: 1 });

    let harness: AIMockHarness;

    reasoningDisplayTest.beforeAll(async ({ containers }) => {
        ({ harness } = containers);
    });

    reasoningDisplayTest('Reasoning Display - Renders from aimock', async ({ page }) => {
        reasoningDisplayTest.setTimeout(120000);

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

    reasoningDisplayTest('Reasoning Toggle - Expand and Collapse', async ({ page }) => {
        reasoningDisplayTest.setTimeout(120000);

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

    reasoningDisplayTest('Reasoning States - Complete State', async ({ page }) => {
        reasoningDisplayTest.setTimeout(120000);

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

    reasoningDisplayTest('Multiple Posts with Reasoning', async ({ page }) => {
        reasoningDisplayTest.setTimeout(180000);

        const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());

        await aiPlugin.sendMessage(PHASE3_REASONING_MULTI_FIRST_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.expectReasoningVisible(true);
        await expect(llmBotHelper.getReasoningLabel()).toBeVisible();

        await aiPlugin.sendMessage(PHASE3_REASONING_MULTI_SECOND_PROMPT);

        const allReasoningDisplays = llmBotHelper.getAllReasoningDisplays();
        await expect.poll(() => allReasoningDisplays.count(), { timeout: 120000 }).toBeGreaterThanOrEqual(2);

        const firstReasoning = allReasoningDisplays.first();
        await firstReasoning.scrollIntoViewIfNeeded();
        await firstReasoning.click();

        const secondReasoning = allReasoningDisplays.nth(1);
        await secondReasoning.scrollIntoViewIfNeeded();
        await secondReasoning.click();

        const countAfterClicks = await allReasoningDisplays.count();
        expect(countAfterClicks).toBeGreaterThanOrEqual(2);
    });
});

const reasoningPersistenceTest = harnessTest();

reasoningPersistenceTest.describe('Reasoning Persistence After Refresh - aimock', () => {
    let harness: AIMockHarness;

    reasoningPersistenceTest.beforeAll(async ({ containers }) => {
        ({ harness } = containers);
    });

    reasoningPersistenceTest('Reasoning Persistence After Refresh', async ({ page }) => {
        reasoningPersistenceTest.setTimeout(120000);

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

        await aiPlugin.openChatHistory();
        await aiPlugin.clickChatHistoryItem(0);

        await llmBotHelper.expectReasoningVisible(true);
        await llmBotHelper.expectReasoningExpanded(false);
        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);
        await llmBotHelper.expectReasoningText(PHASE3_REASONING_PERSIST_REASONING);
    });
});
