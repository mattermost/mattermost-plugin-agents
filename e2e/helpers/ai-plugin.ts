import { Page, Locator, expect } from '@playwright/test';

export class AIPlugin {
  readonly page: Page;
  readonly appBarIcon: Locator;
  readonly rhsPostTextarea: Locator;
  readonly rhsSendButton: Locator;
  readonly regenerateButton: Locator;
  readonly chatHistoryButton: Locator;
  readonly threadsListContainer: Locator;
  readonly promptTemplates: {
    [key: string]: Locator;
  };

  constructor(page: Page) {
    this.page = page;
    this.appBarIcon = page.locator('#app-bar-icon-mattermost-ai');
    this.rhsPostTextarea = page.locator("#rhsContainer").locator('textarea');
    this.rhsSendButton = page.locator('#rhsContainer').getByTestId('SendMessageButton');
    this.regenerateButton = page.getByRole('button', { name: 'Regenerate' });
    this.chatHistoryButton = page.getByTestId('chat-history');
    this.threadsListContainer = page.getByTestId('rhs-threads-list');
    this.promptTemplates = {
      'brainstorm': page.getByRole('button', { name: 'Brainstorm ideas' }),
      'todo': page.getByRole('button', { name: 'To-do list' }),
      'proscons': page.getByRole('button', { name: 'Pros and Cons' }),
    };
  }

  async openRHS() {
    // Wait for plugin to be fully initialized with a longer timeout for flaky scenarios
    // The longer timeout helps handle cases where plugin initialization is slow
    await expect(this.appBarIcon).toBeVisible({ timeout: 30000 });

    // Check if RHS is already open to avoid unnecessary clicks
    const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');
    const isRHSVisible = await rhsContainer.isVisible().catch(() => false);

    if (!isRHSVisible) {
      // Wait for the icon to be in a stable, clickable state
      // This helps with timing issues where the element is visible but not yet interactive
      await this.appBarIcon.waitFor({ state: 'visible', timeout: 5000 });
      await this.page.waitForTimeout(500); // Small delay to ensure the icon is fully rendered
      
      // Retry click with error handling for obscured/not clickable elements
      let clicked = false;
      for (let attempt = 0; attempt < 3; attempt++) {
        try {
          await this.appBarIcon.click({ timeout: 10000 });
          clicked = true;
          break;
        } catch (error) {
          if (attempt < 2) {
            await this.page.waitForTimeout(1000);
          } else {
            throw error;
          }
        }
      }
      
      if (clicked) {
        await expect(rhsContainer).toBeVisible({ timeout: 10000 });
      }
    }
  }

  async sendMessage(message: string) {
    await this.rhsPostTextarea.fill(message);
    await this.rhsSendButton.click();
  }

  async usePromptTemplate(templateName: keyof typeof this.promptTemplates) {
    await this.promptTemplates[templateName].click();
  }

  async regenerateResponse() {
    await this.regenerateButton.click();
  }

  async switchBot(botName: string) {
    await this.page.getByTestId(`bot-selector-rhs`).click();
    await this.page.getByRole('button', { name: botName }).click();
  }

  async waitForBotResponse(expectedText: string) {
    // Scope to RHS container to avoid matching text elsewhere on the page
    const rhsContainer = this.page.getByTestId('mattermost-ai-rhs');
    // Prefer the most recent matching response so virtualized lists don't hide older entries
    await expect(rhsContainer.getByText(expectedText).last()).toBeVisible({timeout: 10000});
  }

  async expectTextInTextarea(text: string) {
    await expect(this.rhsPostTextarea).toHaveText(text);
  }

  async openChatHistory() {
    await this.chatHistoryButton.click();
    await expect(this.threadsListContainer).toBeVisible();
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
      await this.page.waitForTimeout(500);
    }
  }

}
