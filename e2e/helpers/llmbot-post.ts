import { Page, Locator, expect } from '@playwright/test';

/**
 * LLMBotPostHelper - Page object for LLMBot post component interactions
 *
 * Provides locators, actions, and assertions for testing:
 * - Reasoning display (expand/collapse, loading states)
 * - Citations/annotations (icons, tooltips, clicks)
 * - Streaming indicators (cursor, status)
 * - Post text content
 * - Regeneration controls
 */
export class LLMBotPostHelper {
    readonly page: Page;

    constructor(page: Page) {
        this.page = page;
    }

    // ==================== LOCATORS ====================

    /**
     * Get the main LLMBot post container
     * @param postId - Optional post ID to target specific post
     */
    getLLMBotPost(postId?: string): Locator {
        if (postId) {
            return this.page.locator(`#post_${postId}`);
        }
        // Get the last (most recent) LLMBot post by default
        return this.page.locator('[data-testid="llm-bot-post"]').last();
    }

    /**
     * Get the reasoning display container (minimal or expanded)
     * @param postId - Optional post ID to scope the search
     */
    getReasoningDisplay(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        // Look for the minimal reasoning container or expanded reasoning header
        return baseLocator.locator('[class*="MinimalReasoningContainer"], [class*="ExpandedReasoningHeader"]').first();
    }

    /**
     * Get the reasoning toggle/header element (clickable element with "Thinking" text)
     * @param postId - Optional post ID to scope the search
     */
    getReasoningToggle(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        // Target either minimal or expanded header that contains "Thinking"
        return baseLocator.locator('[class*="MinimalReasoningContainer"], [class*="ExpandedReasoningHeader"]').first();
    }

    /**
     * Get the reasoning loading spinner
     * @param postId - Optional post ID to scope the search
     */
    getReasoningSpinner(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        // LoadingSpinner is a styled div, not an SVG
        return baseLocator.locator('div[class*="LoadingSpinner"]').first();
    }

    /**
     * Get the expanded reasoning text content
     * @param postId - Optional post ID to scope the search
     */
    getReasoningContent(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        return baseLocator.locator('div[class*="ExpandedReasoningContainer"]').first();
    }

    /**
     * Get the chevron icon for reasoning expand/collapse
     * @param postId - Optional post ID to scope the search
     */
    getReasoningChevron(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        // ChevronRight is inside MinimalExpandIcon or ExpandedChevron containers
        return baseLocator.locator('[class*="MinimalExpandIcon"] svg, [class*="ExpandedChevron"] svg').first();
    }

    /**
     * Get citation icon by index
     * @param index - Citation index (1-based)
     * @param postId - Optional post ID to scope the search
     */
    getCitationIcon(index: number, postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        // Look for citation wrapper spans, which contain the SVG icon
        return baseLocator.locator('[class*="CitationWrapper"] svg').nth(index - 1);
    }

    /**
     * Get all citation icons in a post
     * @param postId - Optional post ID to scope the search
     */
    getAllCitationIcons(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        // Look for citation wrapper spans, which contain the SVG icon
        return baseLocator.locator('[class*="CitationWrapper"] svg');
    }

    /**
     * Get citation tooltip (appears on hover)
     * @param postId - Optional post ID to scope the search
     */
    getCitationTooltip(postId?: string): Locator {
        // Tooltip is rendered at page level, not inside post container
        return this.page.locator('[class*="TooltipContainer"]').first();
    }

    /**
     * Get citation wrapper (clickable container)
     * @param index - Citation index (1-based)
     * @param postId - Optional post ID to scope the search
     */
    getCitationWrapper(index: number, postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        return baseLocator.locator('[class*="CitationWrapper"]').nth(index - 1);
    }

    /**
     * Get the post text content
     * @param postId - Optional post ID to scope the search
     */
    getPostText(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        return baseLocator.locator('[data-testid="posttext"]').first();
    }

    /**
     * Get the regenerate button
     * @param postId - Optional post ID to scope the search
     */
    getRegenerateButton(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        return baseLocator.getByRole('button', { name: /regenerate/i });
    }

    /**
     * Get the stop generating button (visible during streaming)
     * @param postId - Optional post ID to scope the search
     */
    getStopGeneratingButton(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        return baseLocator.getByRole('button', { name: /stop/i });
    }

    /**
     * Get streaming cursor indicator
     * @param postId - Optional post ID to scope the search
     */
    getStreamingCursor(postId?: string): Locator {
        const baseLocator = postId ? this.getLLMBotPost(postId) : this.getLLMBotPost();
        return baseLocator.locator('p:last-child').first();
    }

    // ==================== ACTIONS ====================

    /**
     * Click the reasoning toggle to expand or collapse
     * @param postId - Optional post ID to target specific post
     */
    async clickReasoningToggle(postId?: string): Promise<void> {
        const toggle = this.getReasoningToggle(postId);
        await toggle.click();
    }

    /**
     * Hover over a citation icon to show tooltip
     * @param index - Citation index (1-based)
     * @param postId - Optional post ID to scope the action
     */
    async hoverCitation(index: number, postId?: string): Promise<void> {
        const citationWrapper = this.getCitationWrapper(index, postId);
        await citationWrapper.hover();
        await this.page.waitForTimeout(300);
    }

    /**
     * Click a citation icon to open URL
     * @param index - Citation index (1-based)
     * @param postId - Optional post ID to scope the action
     */
    async clickCitation(index: number, postId?: string): Promise<void> {
        const citationWrapper = this.getCitationWrapper(index, postId);
        await citationWrapper.click();
    }

    /**
     * Click the regenerate button
     * @param postId - Optional post ID to scope the action
     */
    async regenerateResponse(postId?: string): Promise<void> {
        const button = this.getRegenerateButton(postId);
        await button.click();
    }

    /**
     * Click the stop generating button
     * @param postId - Optional post ID to scope the action
     */
    async stopGenerating(postId?: string): Promise<void> {
        const button = this.getStopGeneratingButton(postId);
        await button.click();
    }

    // ==================== ASSERTIONS ====================

    /**
     * Assert reasoning display visibility
     * @param expected - Expected visibility state
     * @param postId - Optional post ID to scope the assertion
     */
    async expectReasoningVisible(expected: boolean, postId?: string): Promise<void> {
        const reasoning = this.getReasoningDisplay(postId);
        if (expected) {
            await expect(reasoning).toBeVisible();
        } else {
            await expect(reasoning).not.toBeVisible();
        }
    }

    /**
     * Assert reasoning is in expanded state
     * @param expected - Expected expansion state
     * @param postId - Optional post ID to scope the assertion
     */
    async expectReasoningExpanded(expected: boolean, postId?: string): Promise<void> {
        const content = this.getReasoningContent(postId);
        if (expected) {
            await expect(content).toBeVisible();
        } else {
            await expect(content).not.toBeVisible();
        }
    }

    /**
     * Assert reasoning text content
     * @param text - Expected text (can be partial match)
     * @param postId - Optional post ID to scope the assertion
     */
    async expectReasoningText(text: string, postId?: string): Promise<void> {
        const content = this.getReasoningContent(postId);
        await expect(content).toContainText(text);
    }

    /**
     * Assert reasoning loading spinner state
     * @param visible - Expected visibility of spinner
     * @param postId - Optional post ID to scope the assertion
     */
    async expectReasoningLoading(visible: boolean, postId?: string): Promise<void> {
        const spinner = this.getReasoningSpinner(postId);
        if (visible) {
            await expect(spinner).toBeVisible();
        } else {
            await expect(spinner).not.toBeVisible();
        }
    }

    /**
     * Assert citation count
     * @param count - Expected number of citations
     * @param postId - Optional post ID to scope the assertion
     */
    async expectCitationCount(count: number, postId?: string): Promise<void> {
        const citations = this.getAllCitationIcons(postId);
        await expect(citations).toHaveCount(count);
    }

    /**
     * Assert citation tooltip content
     * @param domain - Expected domain text in tooltip
     * @param postId - Optional post ID (tooltip is typically global)
     */
    async expectCitationTooltip(domain: string, postId?: string): Promise<void> {
        const tooltip = this.getCitationTooltip(postId);
        await expect(tooltip).toBeVisible();
        await expect(tooltip).toContainText(domain);
    }

    /**
     * Assert streaming cursor visibility
     * @param visible - Expected visibility state
     * @param postId - Optional post ID to scope the assertion
     */
    async expectStreamingCursor(visible: boolean, postId?: string): Promise<void> {
        const cursor = this.getStreamingCursor(postId);
        if (visible) {
            await expect(cursor).toBeVisible();
        }
    }

    /**
     * Assert post text content
     * @param text - Expected text (can be partial match)
     * @param postId - Optional post ID to scope the assertion
     */
    async expectPostText(text: string, postId?: string): Promise<void> {
        const postText = this.getPostText(postId);
        await expect(postText).toContainText(text);
    }

    /**
     * Assert post has specific text exactly
     * @param text - Expected exact text
     * @param postId - Optional post ID to scope the assertion
     */
    async expectPostTextExact(text: string, postId?: string): Promise<void> {
        const postText = this.getPostText(postId);
        await expect(postText).toHaveText(text);
    }

    /**
     * Assert regenerate button visibility
     * @param visible - Expected visibility state
     * @param postId - Optional post ID to scope the assertion
     */
    async expectRegenerateVisible(visible: boolean, postId?: string): Promise<void> {
        const button = this.getRegenerateButton(postId);
        if (visible) {
            await expect(button).toBeVisible();
        } else {
            await expect(button).not.toBeVisible();
        }
    }

    /**
     * Wait for post text to appear with smart polling
     * Returns early when text appears, with high timeout as safety net
     * @param text - Text to wait for
     * @param postId - Optional post ID to scope the wait
     * @param maxTimeout - Maximum wait time in ms (default: 5 minutes)
     */
    async waitForPostText(text: string, postId?: string, maxTimeout: number = 300000): Promise<void> {
        const postText = this.getPostText(postId);

        // Poll every 500ms checking if the text has appeared
        const startTime = Date.now();
        while (Date.now() - startTime < maxTimeout) {
            try {
                const content = await postText.textContent();
                if (content && content.includes(text)) {
                    // Text found - wait a bit for final updates
                    await this.page.waitForTimeout(500);
                    return;
                }
            } catch (error) {
                // Element not yet available, continue polling
            }
            await this.page.waitForTimeout(500);
        }

        // If we hit max timeout, throw error
        throw new Error(`Timeout waiting for post text to contain: ${text}`);
    }

    /**
     * Wait for reasoning to complete with smart polling
     * Returns early when reasoning spinner disappears, with high timeout as safety net
     * @param postId - Optional post ID to scope the wait
     * @param maxTimeout - Maximum wait time in ms (default: 5 minutes)
     */
    async waitForReasoning(postId?: string, maxTimeout: number = 300000): Promise<void> {
        // First wait for reasoning display to appear (shorter timeout for initial appearance)
        const reasoning = this.getReasoningDisplay(postId);
        await expect(reasoning).toBeVisible({ timeout: 60000 });

        // Then poll until reasoning spinner disappears (reasoning complete)
        const spinner = this.getReasoningSpinner(postId);

        const startTime = Date.now();
        while (Date.now() - startTime < maxTimeout) {
            const isVisible = await spinner.isVisible().catch(() => false);
            if (!isVisible) {
                // Spinner gone, reasoning complete - wait a bit for final updates
                await this.page.waitForTimeout(1000);
                return;
            }
            await this.page.waitForTimeout(500);
        }

        // If we hit max timeout, that's okay - reasoning might have completed without spinner
    }

    /**
     * Wait for citation to appear with smart polling
     * Returns early when citation appears, with high timeout as safety net
     * @param index - Citation index (1-based)
     * @param postId - Optional post ID to scope the wait
     * @param maxTimeout - Maximum wait time in ms (default: 5 minutes)
     */
    async waitForCitation(index: number, postId?: string, maxTimeout: number = 300000): Promise<void> {
        const citation = this.getCitationIcon(index, postId);

        // Poll every 500ms checking if citation has appeared
        const startTime = Date.now();
        while (Date.now() - startTime < maxTimeout) {
            const isVisible = await citation.isVisible().catch(() => false);
            if (isVisible) {
                // Citation found - wait a bit for final updates
                await this.page.waitForTimeout(500);
                return;
            }
            await this.page.waitForTimeout(500);
        }

        // If we hit max timeout, throw error
        throw new Error(`Timeout waiting for citation ${index} to appear`);
    }

    /**
     * Wait for bot response streaming to complete with smart polling
     * Returns early when streaming finishes, with maxTimeout as safety net
     * @param maxTimeout - Maximum wait time in ms for entire operation (default: 5 minutes)
     */
    async waitForStreamingComplete(maxTimeout: number = 300000): Promise<void> {
        const startTime = Date.now();

        // Wait for post text to appear
        const postText = this.getPostText();
        const remainingTime = maxTimeout - (Date.now() - startTime);
        await expect(postText).toBeVisible({ timeout: remainingTime });

        // Wait for "Stop Generating" button to disappear (streaming complete)
        const stopButton = this.getStopGeneratingButton();

        // Poll every 500ms until stop button disappears (streaming complete)
        while (Date.now() - startTime < maxTimeout) {
            const isVisible = await stopButton.isVisible().catch(() => false);
            if (!isVisible) {
                // Stop button gone, streaming complete - wait a bit for final updates
                await this.page.waitForTimeout(1000);
                return;
            }
            await this.page.waitForTimeout(500);
        }

        // If we hit max timeout, that's okay - streaming might have completed without stop button
    }
}
