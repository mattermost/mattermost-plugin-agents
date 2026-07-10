import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { AgentPageHelper } from 'helpers/agent-page';
import { expectSelectedAgentPreference, resetSelectedAgentPreference } from 'helpers/agent_preferences';
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

// Agent selection persists server-side; reset so mock-bot tests stay isolated.
test.beforeEach(async () => {
  await resetSelectedAgentPreference(mattermost, username, password);
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

  test('Manage opens the Agents product page and browser back returns to the channel', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    const agentPage = new AgentPageHelper(page);
    const userClient = await mattermost.getClient(username, password);
    const user = await userClient.getMe();
    const secondBot = await userClient.getUserByUsername('second');
    const channelURL = new URL(page.url());

    await aiPlugin.openRHS();
    await aiPlugin.ensureRhsNewChatTab();

    const rhs = aiPlugin.getRhsContainer();
    const selector = rhs.getByTestId('bot-selector-rhs');
    await expect(rhs.getByTestId('rhs-new-tab-create-post')).toBeVisible({ timeout: 30000 });
    await expect(selector).toHaveAttribute('title', 'Mock Bot');
    await selector.click();

    const menu = page.getByTestId('dropdownmenu').filter({ hasText: 'Choose an Agent' });
    await expect(menu.getByText('Choose an Agent', { exact: true })).toBeVisible();
    await menu.getByRole('button', { name: 'Second Bot', exact: true }).click();
    await expect(menu).not.toBeVisible();
    await expect(selector).toHaveAttribute('title', 'Second Bot');
    await expectSelectedAgentPreference(userClient, user.id, secondBot.id);

    await selector.click();
    await expect(menu.getByText('Choose an Agent', { exact: true })).toBeVisible();
    const manageButton = menu.getByRole('button', { name: 'Manage', exact: true });
    await expect(manageButton).toBeVisible();
    await manageButton.click();

    await expect(page).toHaveURL(`${mattermost.url()}/plug/mattermost-ai/agents`);
    await agentPage.waitForAgentsLoaded();
    await expect(agentPage.getAgentRowByName('Second Bot')).toBeVisible();

    await page.goBack();
    await expect(page).toHaveURL((url) => (
      url.origin === channelURL.origin &&
      url.pathname === channelURL.pathname &&
      url.search === channelURL.search
    ));
    await expect(page.getByTestId('channel_view')).toBeVisible();

    await aiPlugin.openRHS();
    await aiPlugin.ensureRhsNewChatTab();
    const restoredSelector = aiPlugin.getRhsContainer().getByTestId('bot-selector-rhs');
    await expect(restoredSelector).toHaveAttribute('title', 'Second Bot');
    await expect(restoredSelector).toContainText('Second Bot');
    await expectSelectedAgentPreference(userClient, user.id, secondBot.id);
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
