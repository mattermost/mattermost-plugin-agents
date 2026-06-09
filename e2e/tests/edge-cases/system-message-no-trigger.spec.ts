// spec: MM-67969 — system messages (e.g. channel header / purpose change with an @agent in the
// new value) must not trigger an agent response.
// seed: tests/seed.spec.ts

import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
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

test.describe('System messages do not trigger agent', () => {
    test('updating a channel header to mention an agent does not trigger a response', async ({ page }) => {
        // If the bug were to regress and the agent did respond, the mock would serve a real
        // chat-completion stream. Pre-seeding it makes the regression observable in the post list
        // rather than failing silently because no mock was registered.
        await openAIMock.addCompletionMock(responseTest);

        const client = await mattermost.getClient(username, password);
        const teams = await client.getMyTeams();
        const channels = await client.getMyChannels(teams[0].id);
        const townSquare = channels.find((c) => c.name === 'town-square');
        if (!townSquare) {
            throw new Error('town-square channel not found');
        }

        const beforePosts = await client.getPosts(townSquare.id, 0, 200);
        const beforeIds = new Set(Object.keys(beforePosts.posts || {}));

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        await client.patchChannel(townSquare.id, { header: '@mock please respond' });

        // The system message reads "{user} updated the channel header to: @mock please respond"
        // (or "from: X to: Y" when a previous header existed). Scope to the channel's post list
        // to avoid colliding with the rendered channel header at the top of the page.
        const systemMessage = page
            .getByTestId('postContent')
            .filter({ hasText: /updated the channel header/i })
            .first();
        await expect(systemMessage).toBeVisible({ timeout: 15000 });

        // Give the plugin a window to (incorrectly) generate and stream a reply.
        await page.waitForTimeout(5000);

        const afterPosts = await client.getPosts(townSquare.id, 0, 200);
        const newPosts = Object.values(afterPosts.posts || {})
            .filter((p) => !beforeIds.has(p.id));

        // Bot responses are tagged with the dedicated custom_llmbot post type
        // (see streaming.ModifyPostForBot). Restricting to that type avoids
        // false positives from unrelated posts that other tests in the shard
        // might leave around.
        const botResponses = newPosts.filter((p) => p.type === 'custom_llmbot');
        expect(
            botResponses,
            `Expected no bot response to the header change system message. Got: ${JSON.stringify(botResponses, null, 2)}`,
        ).toEqual([]);
    });
});
