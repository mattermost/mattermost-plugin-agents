// spec: replying in an agent thread without an @mention offers a loop in link.
// seed: tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import type { Client4 } from '@mattermost/client';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';
const botUsername = 'mock';
const botDisplayName = 'Mock Bot';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    test.setTimeout(180000);
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.beforeEach(async () => {
    await openAIMock.resetMocks();
});

test.afterAll(async () => {
    await openAIMock?.stop();
    await mattermost?.stop();
});

async function countAgentPostsInThread(client: Client4, rootPostId: string, botUserId: string): Promise<number> {
    const thread = await client.getPostThread(rootPostId);
    return Object.values(thread.posts || {}).filter((post) => post.user_id === botUserId).length;
}

test.describe('Agent mention reminder', () => {
    test('loops the agent into a thread reply that did not mention it', async ({ page }) => {
        test.setTimeout(180000);
        await openAIMock.addCompletionMock(responseTest);

        const client = await mattermost.getClient(username, password);
        const teams = await client.getMyTeams();
        const channel = await client.getChannelByName(teams[0].id, 'town-square');
        const bot = await client.getUserByUsername(botUsername);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);

        // The agent answers a channel mention in a thread, so the thread's last
        // post is authored by the agent.
        const rootPost = await client.createPost({
            channel_id: channel.id,
            message: `@${botUsername} how is the rollout going?`,
        });

        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        const threadFooter = page.locator(`#post_${rootPost.id}`).getByText(/\d+ repl/);
        await expect(threadFooter).toBeVisible({ timeout: 60000 });
        await threadFooter.click();

        const rhs = page.locator('#rhsContainer');
        await expect(rhs.getByText(responseTestText)).toBeVisible({ timeout: 60000 });

        // A plain thread reply: no @mention, so the agent stays out of it.
        const replyTextbox = page.getByTestId('reply_textbox');
        await replyTextbox.click();
        await replyTextbox.fill('Thanks, can you expand on that?');
        await replyTextbox.press('Enter');

        const loopInLink = rhs.getByRole('link', { name: `Click here to loop in @${botDisplayName}` });
        await expect(loopInLink).toBeVisible({ timeout: 60000 });

        await loopInLink.click();

        await expect(rhs.getByText(`Looped in @${botDisplayName}.`)).toBeVisible({ timeout: 60000 });
        await expect
            .poll(() => countAgentPostsInThread(client, rootPost.id, bot.id), {
                timeout: 60000,
                intervals: [500, 1000, 2000],
            })
            .toBeGreaterThan(1);
    });
});
