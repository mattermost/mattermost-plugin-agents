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
    this.rhsPostTextarea = page.getByTestId('rhs-new-tab-create-post').locator('textarea');
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
    await expect(this.appBarIcon).toBeVisible();
    await this.appBarIcon.click();
    await expect(this.page.getByTestId('mattermost-ai-rhs')).toBeVisible();
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
    await expect(this.page.getByText(expectedText)).toBeVisible();
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
    const threadItems = this.threadsListContainer.locator('div').first();
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

}
