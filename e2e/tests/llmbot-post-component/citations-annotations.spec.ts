import { expect } from '@playwright/test';

import {
    describeAIMockCitationCase,
    sendMessageWithWebSearchApproval,
} from 'helpers/aimock-citation-harness';
import {
    buildLLMBotCitationsFixtures,
    CITATION_CLICK_PROMPT,
    CITATION_DISPLAY_PROMPT,
    CITATION_FAVICON_PROMPT,
    CITATION_INLINE_PROMPT,
    CITATION_MARKDOWN_PROMPT,
    CITATION_PERSISTENCE_PROMPT,
    CITATION_TOOLTIP_PROMPT,
    MULTIPLE_CITATIONS_PROMPT,
} from '../../fixtures/aimock/llmbot-citations';

/**
 * Test Suite: Citations and Annotations (deterministic aimock + WebSearch tool fallback)
 */

const EXPECTED_SINGLE_CITATION_COUNT = 1;
const EXPECTED_MULTIPLE_CITATION_COUNT = 2;
const CITATION_FIXTURES = buildLLMBotCitationsFixtures();

describeAIMockCitationCase({
    title: 'Citation Display - Renders Citation Annotations',
    fixtures: CITATION_FIXTURES,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, CITATION_DISPLAY_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.waitForCitation(1, undefined, 60000);

        await llmBotHelper.expectCitationCount(EXPECTED_SINGLE_CITATION_COUNT);
        await expect(llmBotHelper.getAllCitationIcons().first()).toBeVisible();
    },
});

describeAIMockCitationCase({
    title: 'Citation Hover Tooltip',
    fixtures: CITATION_FIXTURES,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, CITATION_TOOLTIP_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.waitForCitation(1, undefined, 60000);

        const citationWrapper = llmBotHelper.getCitationWrapper(1);
        await citationWrapper.scrollIntoViewIfNeeded();
        await llmBotHelper.hoverCitation(1);

        const tooltip = llmBotHelper.getCitationTooltip();
        await expect(tooltip).toBeVisible({ timeout: 5000 });
        await expect(tooltip).toContainText('typescriptlang.org');
    },
});

describeAIMockCitationCase({
    title: 'Citation Click Link',
    fixtures: CITATION_FIXTURES,
    run: async ({ page, context, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, CITATION_CLICK_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.waitForCitation(1, undefined, 60000);

        const citationWrapper = llmBotHelper.getCitationWrapper(1);
        await citationWrapper.scrollIntoViewIfNeeded();

        const pagePromise = context.waitForEvent('page');
        await llmBotHelper.clickCitation(1);

        const newPage = await pagePromise;
        await expect(newPage.url()).toContain('typescriptlang.org');
        await newPage.close();
    },
});

describeAIMockCitationCase({
    title: 'Multiple Citations',
    fixtures: CITATION_FIXTURES,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, MULTIPLE_CITATIONS_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.waitForCitation(2, undefined, 60000);

        await llmBotHelper.expectCitationCount(EXPECTED_MULTIPLE_CITATION_COUNT);

        await llmBotHelper.getCitationWrapper(1).scrollIntoViewIfNeeded();
        await llmBotHelper.hoverCitation(1);
        await expect(llmBotHelper.getCitationTooltip()).toBeVisible({ timeout: 5000 });
        await expect(llmBotHelper.getCitationTooltip()).toContainText('typescriptlang.org');

        await page.mouse.move(0, 0);
        await page.waitForTimeout(300);

        await llmBotHelper.getCitationWrapper(2).scrollIntoViewIfNeeded();
        await llmBotHelper.hoverCitation(2);
        await expect(llmBotHelper.getCitationTooltip()).toBeVisible({ timeout: 5000 });
        await expect(llmBotHelper.getCitationTooltip()).toContainText('developer.mozilla.org');
    },
});

describeAIMockCitationCase({
    title: 'Citation Persistence After Refresh',
    fixtures: CITATION_FIXTURES,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, CITATION_PERSISTENCE_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.waitForCitation(1, undefined, 60000);

        const countBefore = await llmBotHelper.getAllCitationIcons().count();
        expect(countBefore).toBe(EXPECTED_SINGLE_CITATION_COUNT);

        await page.reload();
        await aiPlugin.openRHS();
        await aiPlugin.openChatHistory();
        await aiPlugin.clickChatHistoryItem(0);

        await llmBotHelper.expectCitationCount(countBefore);
        await expect(llmBotHelper.getAllCitationIcons().first()).toBeVisible();
    },
});

describeAIMockCitationCase({
    title: 'Citations with Markdown Content',
    fixtures: CITATION_FIXTURES,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, CITATION_MARKDOWN_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.waitForCitation(1, undefined, 60000);

        await expect(llmBotHelper.getPostText()).toBeVisible();
        await llmBotHelper.expectCitationCount(EXPECTED_SINGLE_CITATION_COUNT);
    },
});

describeAIMockCitationCase({
    title: 'Citation Inline Positioning',
    fixtures: CITATION_FIXTURES,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, CITATION_INLINE_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.waitForCitation(2, undefined, 60000);

        await llmBotHelper.expectCitationsInline();
    },
});

describeAIMockCitationCase({
    title: 'Citation Favicon Display',
    fixtures: CITATION_FIXTURES,
    run: async ({ page, aiPlugin, llmBotHelper }) => {
        await sendMessageWithWebSearchApproval(page, aiPlugin, CITATION_FAVICON_PROMPT);
        await llmBotHelper.waitForStreamingComplete();
        await llmBotHelper.waitForCitation(1, undefined, 60000);

        await llmBotHelper.getCitationWrapper(1).scrollIntoViewIfNeeded();
        await llmBotHelper.hoverCitation(1);

        const tooltip = llmBotHelper.getCitationTooltip();
        await expect(tooltip).toBeVisible({ timeout: 5000 });
        await expect(tooltip).toContainText('typescriptlang.org');

        const favicon = tooltip.locator('img[src*="favicon"], svg');
        await expect(favicon.first()).toBeVisible();
    },
});
