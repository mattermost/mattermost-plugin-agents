import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { resetSelectedAgentPreference } from 'helpers/agent_preferences';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
  mattermost = await RunContainer();
  openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.afterAll(async () => {
  await openAIMock.stop();
  await mattermost.stop();
});

test.beforeEach(async () => {
  await resetSelectedAgentPreference(mattermost, username, password);
});

test.describe('Agent mention reminder', () => {
  // Replying in an agent thread without an @mention triggers an ephemeral
  // reminder rendered by the custom post component. Guards MM-69160: the link
  // text must be capitalized and match regular post text size.
  test('renders a capitalized link at regular post text size', async ({ page }) => {
    const mmPage = new MattermostPage(page);
    await mmPage.login(mattermost.url(), username, password);

    // Root mention creates a thread whose previous post is authored by the bot.
    await openAIMock.addCompletionMock(responseTest);
    await mmPage.mentionBot('mock', 'Hello');
    await mmPage.waitForReply();

    // Open the thread and reply without mentioning the bot.
    await page.getByText('1 reply').click();
    const replyBox = page.locator('#rhsContainer').getByTestId('reply_textbox');
    await expect(replyBox).toBeVisible({ timeout: 15000 });
    await replyBox.click();
    await replyBox.fill('thanks');
    await replyBox.press('Enter');

    const rhs = page.locator('#rhsContainer');
    await expect(rhs.getByText('To respond to an agent you must @mention them.')).toBeVisible({ timeout: 15000 });

    const loopInLink = rhs.getByRole('link', { name: /loop in @Mock Bot/ });
    await expect(loopInLink).toBeVisible();

    // Capitalized "Click here" (MM-69160).
    const linkText = (await loopInLink.textContent())?.trim() ?? '';
    expect(linkText).toBe('Click here to loop in @Mock Bot');

    // Matches regular post text size, not the smaller 13px hint size (MM-69160).
    const fontSize = await loopInLink.evaluate((el) => window.getComputedStyle(el).fontSize);
    expect(fontSize).toBe('14px');
  });
});
