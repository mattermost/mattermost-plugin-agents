import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText } from 'helpers/openai-mock';

// Test configuration
const username = 'regularuser';
const password = 'regularuser';

const test = containerTest(async (own) => {
  const mattermost = own(await RunContainer());
  const openAIMock = own(await RunOpenAIMocks(mattermost.network));
  return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async ({ containers }) => {
  ({ mattermost, openAIMock } = containers);
});

// Common test setup
async function setupTestPage(page) {
  const mmPage = new MattermostPage(page);
  const aiPlugin = new AIPlugin(page);
  const url = mattermost.url();

  await mmPage.login(url, username, password);

  return { mmPage, aiPlugin };
}

test.describe('Direct Channel Creation', () => {
  test('creates direct channel when not existing', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    // Add custom method to simulate non-existent DM channel
    // We'll intercept the API calls to simulate a missing channel
    let channelCreationRequested = false;
    await page.route('**/api/v4/channels/direct', async (route) => {
      if (route.request().method() === 'POST') {
        // Allow the POST request (channel creation) to succeed
        channelCreationRequested = true;
        await route.continue();
      }
    });
    await aiPlugin.openRHS();

    // Now the channel should exist and we should be able to use it
    // Verify the text area is enabled and we can type in it
    await expect(aiPlugin.rhsPostTextarea).toBeEnabled();

    // Send a message to verify the channel works
    await openAIMock.addCompletionMock(responseTest);
    await aiPlugin.sendMessage(`Testing new channel`);

    // Should see a response in the newly created channel
    await expect(aiPlugin.getRhsContainer().getByTestId('posttext').getByText(responseTestText)).toBeVisible({ timeout: 10000 });

    // Verify channel creation was requested
    expect(channelCreationRequested).toBe(true);
  });

  test('uses existing direct channel when available', async ({ page }) => {
    const { aiPlugin } = await setupTestPage(page);
    await aiPlugin.openRHS();

    // Intercept to verify no channel creation request is made
    let channelCreationRequested = false;
    await page.route('**/api/v4/channels/direct', async (route) => {
      if (route.request().method() === 'POST') {
        channelCreationRequested = true;
      }
      await route.continue();
    });

    await aiPlugin.appBarIcon.click();
    await expect(page.getByTestId('mattermost-ai-rhs')).not.toBeVisible();
    await aiPlugin.openRHS();

    // Select a bot (the channel should already exist from previous test)
    const botSelector = page.getByTestId('bot-selector-rhs');
    await botSelector.click();

    // Select first bot (from the Switch agent submenu)
    await page.getByRole('menuitem', { name: 'Switch agent' }).click();
    const firstBot = page.getByRole('menu', { name: 'Switch agent' }).getByRole('menuitem', { name: /.*Bot.*/ }).first();
    await firstBot.click();

    // Channel should be immediately available (no loading state)
    await expect(page.getByText('Setting up chat…')).not.toBeVisible({ timeout: 1000 });

    // Verify the text area is enabled
    await expect(aiPlugin.rhsPostTextarea).toBeEnabled();

    // Send a message to verify the channel works
    await openAIMock.addCompletionMock(responseTest);
    await aiPlugin.sendMessage('Testing existing channel');

    // Should see a response (in the chat's posts; the generated chat title in the header can repeat it)
    await expect(aiPlugin.getRhsContainer().getByTestId('posttext').getByText(responseTestText)).toBeVisible({ timeout: 10000 });

    // Verify no channel creation was requested
    expect(channelCreationRequested).toBe(false);
  });
});
