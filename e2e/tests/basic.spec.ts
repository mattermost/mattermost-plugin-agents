import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTest2, responseTest2Text, responseTestText } from 'helpers/openai-mock';

// Test configuration
const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

// Setup for all tests in the file
test.beforeAll(async () => {
  mattermost = await RunContainer();
  openAIMock = await RunOpenAIMocks(mattermost.network);
});

// Cleanup after all tests
test.afterAll(async () => {
  await openAIMock.stop();
  await mattermost.stop();
});

// Common test setup
async function setupTestPage(page) {
  const mmPage = new MattermostPage(page);
  const aiPlugin = new AIPlugin(page);
  const url = mattermost.url();

  await mmPage.login(url, username, password);

  return { mmPage, aiPlugin };
}

// Test suites
test.describe('Plugin Installation', () => {
  test('Plugin was installed correctly', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();
    await expect(aiPlugin.appBarIcon).toBeVisible();
  });
});

test.describe('RHS Bot Interactions', () => {
  test('can send message and receive response', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();

    await openAIMock.addCompletionMock(responseTest);
    await aiPlugin.sendMessage('Hello!');
    await aiPlugin.waitForBotResponse(responseTestText);
  });

  test('regenerate button creates new response', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();

    // First response
    await openAIMock.addCompletionMock(responseTest);
    await aiPlugin.sendMessage('Hello!');
    await aiPlugin.waitForBotResponse(responseTestText);

    // Second response with regenerate
    await openAIMock.addCompletionMock(responseTest2);
    await aiPlugin.regenerateResponse();
    await aiPlugin.waitForBotResponse(responseTest2Text);
  });

  test('can switch between bots', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();
    await openAIMock.addCompletionMock(responseTest, "second");

    // Switch to second bot
    await aiPlugin.switchBot('Second Bot');

    await aiPlugin.sendMessage('Hello!');
    await expect(page.getByRole('button', { name: 'second', exact: true })).toBeVisible();
    await aiPlugin.waitForBotResponse(responseTestText);
  });
});

test.describe('Prompt Templates', () => {
  test('prompt templates replace text in textarea', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();

    // Clicking prompt template adds message
    await aiPlugin.usePromptTemplate('brainstorm');
    await aiPlugin.expectTextInTextarea('Brainstorm ideas about ');

    // Clicking without editing replaces the text
    await aiPlugin.usePromptTemplate('todo');
    await aiPlugin.expectTextInTextarea('Write a todo list about ');
  });
});

test.describe('Bot Mentions', () => {
  test('bot responds to channel mentions but ignores code blocks', async ({ page }) => {
    const { mmPage } = await setupTestPage(page);
    await openAIMock.addCompletionMock(responseTest);

    // Code block mention - should be ignored
    await mmPage.sendChannelMessage('`@mock` TestBotMention1');
    await mmPage.expectNoReply();

    // Multi-line code block mention - should be ignored
    await mmPage.sendChannelMessage('```\n@mock\n``` TestBotMention2');
    await mmPage.expectNoReply();

    // Regular mention - should get response
    await mmPage.mentionBot('mock', 'TestBotMention3');
    await mmPage.waitForReply();
  });
});

// Error handling tests
test.describe('Error Handling', () => {
  test('handles API errors gracefully', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();

    await openAIMock.addErrorMock(500, "Internal Server Error");
    await aiPlugin.sendMessage('This should cause an error');

    // Check if error message is displayed
    await expect(page.getByText(/An error occurred/i)).toBeVisible();
  });
});

test.describe('Chat History', () => {
  test('can view chat history after creating conversations', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();

    // Create first conversation
    await openAIMock.addCompletionMock(responseTest);
    await aiPlugin.sendMessage('First conversation message');
    await aiPlugin.waitForBotResponse(responseTestText);

    // Create second conversation by starting new chat
    await page.getByTestId('new-chat').click();
    await openAIMock.addCompletionMock(responseTest2);
    await aiPlugin.sendMessage('Second conversation message');
    await aiPlugin.waitForBotResponse(responseTest2Text);

    // Open chat history
    await aiPlugin.openChatHistory();
    await aiPlugin.expectChatHistoryVisible();

    // Verify we can see conversation entries
    await expect(aiPlugin.threadsListContainer.locator('div').first()).toBeVisible();
  });

  test('can click on chat history items without errors', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();

    // Create a conversation first
    await openAIMock.addCompletionMock(responseTest);
    await aiPlugin.sendMessage('Test conversation');
    await aiPlugin.waitForBotResponse(responseTestText);

    // Open chat history
    await aiPlugin.openChatHistory();
    await aiPlugin.expectChatHistoryVisible();

    // Click on the first history item
    await aiPlugin.clickChatHistoryItem(0);

    // Verify we can see the conversation content we created
    await expect(page.getByText('Test conversation')).toBeVisible();
    await expect(page.getByText(responseTestText)).toBeVisible();
  });

  test('chat history button is visible and functional', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();

    // Create a conversation first so we have content in the history
    await openAIMock.addCompletionMock(responseTest);
    await aiPlugin.sendMessage('Test for history button');
    await aiPlugin.waitForBotResponse(responseTestText);

    // Chat history button should be visible
    await expect(aiPlugin.chatHistoryButton).toBeVisible();

    // Should be clickable
    await aiPlugin.chatHistoryButton.click();

    // Should show threads list with content
    await expect(aiPlugin.threadsListContainer).toBeVisible();
    await expect(aiPlugin.threadsListContainer.locator('div').first()).toBeVisible();
  });
});

test.describe('Thread Analysis', () => {
  test('thread summarization follow-up questions work correctly', async ({ page }) => {
    const { mmPage, aiPlugin } = await setupTestPage(page);

    // Create a thread by posting a root message and replies
    const rootPost = await mmPage.sendMessageAsUser(mattermost, username, password, 'First message in the thread discussing the project timeline');

    // Get client to create replies
    const userClient = await mattermost.getClient(username, password);

    // Create replies to form a thread
    await userClient.createPost({
      channel_id: rootPost.channel_id,
      root_id: rootPost.id,
      message: 'Second message: We need to complete the design phase by next Friday'
    });

    await userClient.createPost({
      channel_id: rootPost.channel_id,
      root_id: rootPost.id,
      message: 'Third message: The development phase will take 3 weeks after that'
    });

    // Navigate to the post
    await page.goto(mattermost.url() + '/test/channels/town-square');

    // Wait for the post to be visible
    await page.locator(`#post_${rootPost.id}`).waitFor({ state: 'visible' });

    // Hover over the root post to show the post menu
    await page.locator(`#post_${rootPost.id}`).hover();

    // Click on the AI actions menu
    await page.getByTestId(`ai-actions-menu`).click();

    // Click on "Summarize Thread"
    await openAIMock.addCompletionMock(responseTest);
    await page.getByRole('button', { name: 'Summarize Thread' }).click();

    // Wait for the AI RHS to open and show the summary
    await aiPlugin.expectRHSOpenWithPost();
    await expect(page.getByText(responseTestText)).toBeVisible();

    // Now test the follow-up question functionality
    await openAIMock.addCompletionMock(responseTest2);

    // Send a follow-up question
    await aiPlugin.sendMessage('What is the total duration for both phases?');

    // Verify the follow-up response is received successfully
    await aiPlugin.waitForBotResponse(responseTest2Text);
  });

});
