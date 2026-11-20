// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { Locator, Page, expect } from '@playwright/test';

const ROOT_ID = '[a-z0-9]{26}';

/**
 * AgentChatPage — page object for an agent's chat in the Agents product view
 * (/plug/mattermost-ai/agents/chat/<botUserId>[/<rootId>]).
 */
export class AgentChatPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  /** The host message composer (AdvancedTextEditor) of the new-chat state, in the plugin's wrapper. */
  newChatComposer(): Locator {
    return this.page.getByTestId('agent-chat-new-composer');
  }

  newChatTextbox(): Locator {
    return this.newChatComposer().getByRole('textbox');
  }

  /** Opens a new chat with the agent and waits for its composer. */
  async openNewChat(baseUrl: string, botUserId: string): Promise<void> {
    await this.page.goto(`${baseUrl}/plug/mattermost-ai/agents/chat/${botUserId}`, { waitUntil: 'domcontentloaded' });
    await expect(this.newChatTextbox()).toBeEnabled({ timeout: 30000 });
  }

  /** The URL of a chat with the agent that has been sent (has a root post). */
  threadUrlPattern(botUserId: string): RegExp {
    return new RegExp(`/agents/chat/${botUserId}/${ROOT_ID}$`);
  }

  /**
   * Presses Enter in the new-chat composer until the chat opens (the URL matches `chatUrl`, by
   * default the product's chat URL). The host composer ignores an Enter in the first half second
   * after it mounts (its guard against sending into a channel just switched to), so the key is
   * pressed again; an Enter on a draft already submitted (or cleared) sends nothing.
   */
  async pressEnterUntilSent(botUserId: string, chatUrl: RegExp = this.threadUrlPattern(botUserId)): Promise<void> {
    const textbox = this.newChatTextbox();
    await expect(async () => {
      if (await textbox.count() > 0) {
        await textbox.press('Enter');
      }
      await expect(this.page).toHaveURL(chatUrl, { timeout: 2000 });
    }).toPass({ timeout: 30000 });
  }

  /**
   * Sends `prompt` from the new-chat composer (with Enter, or the host's Send button) and returns the
   * new chat's root post id once the chat has opened.
   */
  async sendNewChat(botUserId: string, prompt: string, send: 'enter' | 'button' = 'enter'): Promise<string> {
    await this.newChatTextbox().fill(prompt);
    if (send === 'enter') {
      await this.pressEnterUntilSent(botUserId);
    } else {
      await this.newChatComposer().getByTestId('SendMessageButton').click();
      await expect(this.page).toHaveURL(this.threadUrlPattern(botUserId), { timeout: 30000 });
    }
    return new URL(this.page.url()).pathname.split('/').pop()!;
  }
}
