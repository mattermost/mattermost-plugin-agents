import { expect } from '@playwright/test';

import {
    approvePendingWebSearchTool,
    describeAIMockCitationCase,
    sendMessageWithWebSearchApproval,
} from 'helpers/aimock-citation-harness';
import {
    buildLLMBotCombinedFeaturesFixtures,
    COMBINED_REASONING_TEXT,
    REASONING_CITATIONS_PROMPT,
    REGENERATE_CITATIONS_PROMPT,
} from '../../fixtures/aimock/llmbot-combined-features';

/**
 * Test Suite: Combined Features (deterministic aimock + WebSearch tool fallback)
 */

const EXPECTED_CITATION_COUNT = 1;
const COMBINED_FIXTURES = buildLLMBotCombinedFeaturesFixtures();

describeAIMockCitationCase({
    title: 'Reasoning and Citations Together',
    fixtures: COMBINED_FIXTURES,
    reasoningEnabled: true,
    timeoutMs: 180000,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, REASONING_CITATIONS_PROMPT);
        await llmBotHelper.waitForReasoning(undefined, 35000);
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await expect(llmBotHelper.getReasoningLabel()).toBeVisible();
        await llmBotHelper.expectCitationCount(EXPECTED_CITATION_COUNT);

        await llmBotHelper.clickReasoningToggle();
        await llmBotHelper.expectReasoningExpanded(true);
        await expect(llmBotHelper.getReasoningContent()).toContainText(COMBINED_REASONING_TEXT);

        await llmBotHelper.getCitationWrapper(1).scrollIntoViewIfNeeded();
        await llmBotHelper.hoverCitation(1);
        await expect(llmBotHelper.getCitationTooltip()).toBeVisible({ timeout: 5000 });
        await expect(llmBotHelper.getCitationTooltip()).toContainText('typescriptlang.org');
    },
});

describeAIMockCitationCase({
    title: 'Regenerate Functionality with Reasoning and Citations',
    fixtures: COMBINED_FIXTURES,
    reasoningEnabled: true,
    timeoutMs: 240000,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, REGENERATE_CITATIONS_PROMPT);
        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await expect(llmBotHelper.getReasoningLabel()).toBeVisible();
        await expect(llmBotHelper.getPostText()).toBeVisible();
        await llmBotHelper.expectCitationCount(EXPECTED_CITATION_COUNT);

        await llmBotHelper.getLLMBotPost().hover();
        await expect(llmBotHelper.getRegenerateButton()).toBeVisible();
        await llmBotHelper.regenerateResponse();
        await approvePendingWebSearchTool(page);

        await llmBotHelper.waitForReasoning();
        await llmBotHelper.waitForStreamingComplete();

        await llmBotHelper.expectReasoningVisible(true);
        await expect(llmBotHelper.getReasoningLabel()).toBeVisible();
        await expect(llmBotHelper.getPostText()).toBeVisible();
        await llmBotHelper.expectCitationCount(EXPECTED_CITATION_COUNT);
    },
});
