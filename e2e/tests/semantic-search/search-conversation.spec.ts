import { test, expect, Page } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    titleGenerationMockRule,
    turnMocksWithTitleSiphon,
} from 'helpers/openai-mock';

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
    await openAIMock.stop();
    await mattermost.stop();
});

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    await mmPage.login(mattermost.url(), username, password);

    // Wait for the plugin to finish initializing before using its entry points.
    await aiPlugin.openRHS();
    await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });
    await aiPlugin.closeRHS();

    return { mmPage, aiPlugin };
}

async function getTownSquareChannelID(): Promise<string> {
    const client = await mattermost.getClient(username, password);
    const teams = await client.getMyTeams();
    const channels = await client.getMyChannels(teams[0].id);
    const townSquare = channels.find((channel) => channel.name === 'town-square');
    if (!townSquare) {
        throw new Error('town-square channel not found');
    }
    return townSquare.id;
}

test.describe('Search bar conversation', () => {
    test('answers a search bar query in a thread in the agent DM', async ({ page }) => {
        const { aiPlugin } = await setupTestPage(page);

        const query = 'what is the status of the budget review?';
        const answer = 'The budget review is scheduled for next week.';
        await openAIMock.addMocks(turnMocksWithTitleSiphon(buildTextResponse(answer)));

        await aiPlugin.triggerEmbeddingSearch(query);
        await aiPlugin.waitForBotResponse(answer);

        const rhs = page.getByTestId('mattermost-ai-rhs');
        await expect(rhs.getByText(query)).toBeVisible();
    });

    test('/ask-channel points the agent at the current channel', async ({ page }) => {
        const { mmPage, aiPlugin } = await setupTestPage(page);
        const townSquareID = await getTownSquareChannelID();

        // Served only when the system prompt names the channel to start in.
        const answer = 'Town Square only answer.';
        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(answer), { bodyContains: `channel ID ${townSquareID}` }),
            titleGenerationMockRule(),
        ]);

        await mmPage.sendChannelMessage('/ask-channel what did we decide?');
        await aiPlugin.waitForBotResponse(answer);
    });
});
