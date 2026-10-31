import { Page, Locator, expect } from '@playwright/test';

// A view sliding out of the RHS (rhs-view-exiting) stays mounted for a moment; locators skip it.
const OUTSIDE_EXITING_RHS_VIEW = ':not([data-testid="rhs-view-exiting"] *)';

export class AIPlugin {
  readonly page: Page;
  readonly appBarIcon: Locator;
  readonly rhsPostTextarea: Locator;
  readonly rhsSendButton: Locator;
  readonly regenerateButton: Locator;
  readonly chatHistoryButton: Locator;
  readonly threadsListContainer: Locator;

  constructor(page: Page) {
    this.page = page;
    this.appBarIcon = page.locator('#app-bar-icon-mattermost-ai');
    // The host message composer, on the New chat tab (AdvancedTextEditor) and
    // under a thread (the ThreadViewer's reply box). Both are RHS composers,
    // whose text box the host names reply_textbox. A view sliding out of the
    // RHS is skipped.
    this.rhsPostTextarea = page.getByTestId('mattermost-ai-rhs').getByTestId('reply_textbox')
      .and(page.locator(OUTSIDE_EXITING_RHS_VIEW));
    this.rhsSendButton = page.getByTestId('mattermost-ai-rhs').getByTestId('SendMessageButton')
      .and(page.locator(OUTSIDE_EXITING_RHS_VIEW));
    this.regenerateButton = page.getByRole('button', { name: 'Regenerate' });
    this.chatHistoryButton = page.getByTestId('chat-history');
    this.threadsListContainer = page.getByTestId('rhs-threads-list');
  }

  /** Root of the Copilot / AI RHS panel (plugin UI). */
  getRhsContainer(): Locator {
    return this.page.getByTestId('mattermost-ai-rhs');
  }

  /** Triggers the per-session MCP tool provider popover (in the RHS header title, only on the New chat tab). */
  getRhsToolsMenuButton(): Locator {
    return this.page.getByTestId('rhs-title').getByRole('button', { name: /^Tools$/i });
  }

  /**
   * Ensures the RHS is on the New chat tab so the Tools control is available.
   * When another tab is active, the New chat button is visible and is clicked.
   */
  async ensureRhsNewChatTab(): Promise<void> {
    const newChat = this.page.getByTestId('new-chat');
    if (await newChat.isVisible().catch(() => false)) {
      await newChat.click();
    }
  }

  async openRhsToolProvidersMenu(): Promise<Locator> {
    await this.ensureRhsNewChatTab();
    await this.getRhsToolsMenuButton().click();
    const menu = this.page.getByTestId('dropdownmenu');
    await expect(menu).toBeVisible({ timeout: 10000 });
    return menu;
  }

  async openRHS() {
    // Wait for plugin to be fully initialized with a longer timeout for flaky scenarios
    // The longer timeout helps handle cases where plugin initialization is slow
    await expect(this.appBarIcon).toBeVisible({ timeout: 30000 });

    // Check if RHS is already open to avoid unnecessary clicks
    const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');
    const isRHSVisible = await rhsContainer.isVisible().catch(() => false);

    if (!isRHSVisible) {
      // Ensure any tooltips from previous hovers are gone
      await this.page.mouse.move(0, 0);

      // Wait for the icon to be in a stable, clickable state
      // This helps with timing issues where the element is visible but not yet interactive
      await this.appBarIcon.waitFor({ state: 'visible', timeout: 5000 });

      // Retry click with error handling for obscured/not clickable elements
      let clicked = false;
      for (let attempt = 0; attempt < 3; attempt++) {
        try {
          await this.appBarIcon.click({ timeout: 5000 });
          clicked = true;
          break;
        } catch (error) {
          if (attempt === 2) {
            console.log("Standard click failed, attempting force click on RHS icon");
            try {
               await this.appBarIcon.click({ force: true });
               clicked = true;
            } catch (e) {
               throw error;
            }
          }
        }
      }

      if (clicked) {
        await expect(rhsContainer).toBeVisible({ timeout: 10000 });
      }
    }
  }

  async sendMessage(message: string) {
    // The new-chat tab and the thread view both use the host composer. Its
    // Send button, not Enter: the host ignores an Enter in the first half
    // second after the composer mounts.
    await expect(this.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
    await this.rhsPostTextarea.fill(message);
    await expect(this.rhsSendButton).toBeEnabled({ timeout: 10000 });
    await this.rhsSendButton.click();
  }

  /** Wait until no in-progress generation remains in the plugin RHS. */
  async waitForGeneratingToStop() {
    await expect(this.getRhsContainer().getByTestId('stop-generating-button').filter({ visible: true })).toHaveCount(0, { timeout: 30000 });
  }

  /** Wait until ThreadViewer is not generating and the reply box accepts input. */
  async waitForThreadComposerIdle() {
    await this.waitForGeneratingToStop();
    await expect(this.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
  }

  async regenerateResponse() {
    await this.regenerateButton.click();
  }

  async switchBot(botName: string) {
    await this.page.getByTestId('bot-selector-rhs').click();
    await this.page.getByRole('menuitem', { name: 'Switch agent' }).click();
    await this.page.getByRole('menu', { name: 'Switch agent' }).getByRole('menuitem', { name: botName }).click();
  }

  /**
   * Select a bot in the RHS title's agent menu, retrying until the bot appears (Agents list can lag behind API creates).
   */
  async switchBotWhenListed(botDisplayName: string): Promise<void> {
    const selector = this.page.getByTestId('bot-selector-rhs');
    const option = this.page.getByRole('menu', { name: 'Switch agent' }).getByRole('menuitem', { name: botDisplayName });
    await expect(async () => {
      await this.page.keyboard.press('Escape');
      await selector.click();
      await this.page.getByRole('menuitem', { name: 'Switch agent' }).click();
      await expect(option).toBeVisible({ timeout: 3000 });
      await option.click();
    }).toPass({ timeout: 45000, intervals: [500, 1000, 2000] });
  }

  async waitForBotResponse(expectedText: string) {
    // Scope to RHS container to avoid matching text elsewhere on the page
    const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');

    // 1. Wait for text to appear (indicates response started)
    // Increased timeout to 30s for CI
    await expect(rhsContainer.getByText(expectedText).last()).toBeVisible({timeout: 30000});

    // 2. Wait for streaming to finish. There can be more than one stop
    // control (one per generating post); require all of them to be gone.
    await this.waitForGeneratingToStop();

    // 3. The composer (reply or new chat) accepts input again.
    await expect(this.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
  }

  async openChatHistory() {
    await this.chatHistoryButton.click();
    await expect(this.threadsListContainer).toBeVisible({ timeout: 15000 });
  }

  async expectChatHistoryVisible() {
    await expect(this.threadsListContainer).toBeVisible();
  }

  async clickChatHistoryItem(index: number = 0) {
    const threadItems = this.threadsListContainer.locator('div');
    await threadItems.nth(index).click();
  }

  async clickNewMessagesButton() {
    const askAIButton = this.page.getByRole('button', { name: 'Ask AI' })
    await expect(askAIButton).toBeVisible();
    await askAIButton.click();
  }

  async clickSummarizeNewMessages() {
	const summarizeButton = this.page.getByRole('button', { name: 'Summarize new messages' })
    await expect(summarizeButton).toBeVisible();
    await summarizeButton.click();
  }

  async expectRHSOpenWithPost(expectedText?: string) {
    await expect(this.page.getByTestId('mattermost-ai-rhs')).toBeVisible();
    if (expectedText) {
      await expect(this.page.getByText(expectedText)).toBeVisible();
    }
  }

  async closeRHS() {
    const closeButton = this.page.locator('#rhsContainer button[aria-label="Close"]').first();
    const isVisible = await closeButton.isVisible().catch(() => false);
    if (isVisible) {
      await closeButton.click();
      await expect(closeButton).toBeHidden({ timeout: 10000 });
    }
  }

  async resetState() {
    const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');
    if (await rhsContainer.isVisible().catch(() => false)) {
        // Close RHS to reset internal component state if needed
        await this.closeRHS();
    }
    // Re-open cleanly
    await this.openRHS();

    // If "New Chat" is visible, click it to ensure fresh context
    const newChatButton = this.page.getByTestId('new-chat');
    if (await newChatButton.isVisible().catch(() => false)) {
        await newChatButton.click();
        await expect(newChatButton).not.toBeVisible({ timeout: 10000 });
        await expect(rhsContainer.locator('[data-testid="llm-bot-post"]')).toHaveCount(0, { timeout: 10000 });
    }
    await expect(this.rhsPostTextarea).toBeVisible({ timeout: 10000 });
  }

  /**
   * Trigger an embedding search via the search bar
   * @param query - The search query to execute
   */
  async triggerEmbeddingSearch(query: string) {
    // Open the search bar
    await this.page.getByRole('button', { name: 'Search' }).click();
    // Select the Agents search type once the search options have appeared
    const agentsRadio = this.page.getByRole('radio', { name: /Agents/i });
    await expect(agentsRadio).toBeVisible({ timeout: 10000 });
    await agentsRadio.click();
    // Enter search query and execute
    await this.page.getByRole('searchbox', { name: 'Search' }).fill(query);
    await this.page.getByRole('searchbox', { name: 'Search' }).press('Enter');
  }

  /**
   * Verify the Agents search option is visible in the search bar
   */
  async expectAgentsSearchVisible() {
    // Open the search bar
    await this.page.getByRole('button', { name: 'Search' }).click();
    // Verify Agents radio option is visible
    const agentsRadio = this.page.getByRole('radio', { name: /Agents/i });
    await expect(agentsRadio).toBeVisible({ timeout: 10000 });
  }

  async openChannelAnalysisPopover() {
    const popover = this.page.locator('.channel-summarize-popover');

    // A popover that was just submitted stays visible while it fades out (it drops the
    // `in` class first); treating it as open would act on a popover about to unmount.
    await expect(this.page.locator('.channel-summarize-popover:not(.in)')).toHaveCount(0, { timeout: 10000 });
    if (await popover.isVisible().catch(() => false)) {
      return;
    }

    const buttonCandidates = [
      this.page.getByTestId('ask-channel-button'),
      this.page.getByRole('button', { name: /Ask Agents about this channel/i }),
      this.page.locator('button[title="Ask Agents about this channel"]'),
    ];

    let clicked = false;
    for (const candidate of buttonCandidates) {
      const isVisible = await candidate.first().isVisible().catch(() => false);
      if (!isVisible) {
        continue;
      }

      await candidate.first().click({ timeout: 10000 });
      clicked = true;
      break;
    }

    if (!clicked) {
      throw new Error('Channel analysis button was not visible');
    }

    await expect(popover).toBeVisible({ timeout: 10000 });
    await expect(popover.getByPlaceholder(/Ask Agents about this channel/i)).toBeVisible({ timeout: 10000 });
    await expect(popover.getByText('Summarize unreads')).toBeVisible({ timeout: 10000 });
    await expect(popover.getByText(/GENERATE WITH/i)).toBeVisible({ timeout: 10000 });
  }

  async sendChannelAnalysisMessage(message: string) {
    // Type in the channel analysis input field and submit
    const popover = this.page.locator('.channel-summarize-popover');
    const input = popover.locator('input[type="text"]');

    // Use fill() to set the value and wait for React state to update
    await input.fill(message);

    // Verify the input has the expected value before submitting
    await expect(input).toHaveValue(message);

    // Press Enter to submit - this is processed in the same event loop as React state
    await input.press('Enter');

    // Wait for RHS to open with the response
    const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');
    await expect(rhsContainer).toBeVisible({ timeout: 10000 });
  }

  async clickSummarizeUnreads() {
    const popover = this.page.locator('.channel-summarize-popover');
    const unreadsButton = popover.getByText('Summarize unreads');
    await unreadsButton.click();

    // Wait for RHS to open with the response
    const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');
    await expect(rhsContainer).toBeVisible({ timeout: 10000 });
  }

  async clickSummarizeDays(days: 7 | 14) {
    const popover = this.page.locator('.channel-summarize-popover');
    const daysButton = popover.getByText(`Summarize last ${days} days`);
    await daysButton.click();

    // Wait for RHS to open with the response
    const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');
    await expect(rhsContainer).toBeVisible({ timeout: 10000 });
  }

  /**
   * Waits until the host composer inside `container` can attach files: its
   * file input is there (the host renders it once uploads are allowed).
   */
  async expectComposerUploadsReady(container: Locator): Promise<void> {
    await expect(container.locator('input[type="file"]')).toBeAttached({ timeout: 30000 });
  }
}
