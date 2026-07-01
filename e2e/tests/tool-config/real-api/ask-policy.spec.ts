import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { AIMockContainer, RunAIMockSidecar } from 'helpers/aimock-container';
import { buildToolCallAndTextResponse } from 'helpers/aimock-fixtures';
import { RunToolConfigAIMockContainer, setupRegularTestUser } from 'helpers/tool-config-container';

const username = 'regularuser';
const password = 'regularuser';

const embeddedGetChannelInfoTool = 'mattermost__get_channel_info';
const getChannelInfoConfigName = 'get_channel_info';
const getChannelInfoLabel = 'Get Channel Info';

const userMessage = 'aimock ask policy get channel info';
const continuationText = 'Aimock ask policy continuation after channel lookup.';

test.describe('Ask Policy (Aimock)', () => {
    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunToolConfigAIMockContainer([
            { name: getChannelInfoConfigName, policy: 'ask', enabled: true },
        ]);
        await setupRegularTestUser(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: buildToolCallAndTextResponse({
                userMessage,
                toolCallId: 'call_aimock_ask_get_channel_info',
                toolName: embeddedGetChannelInfoTool,
                toolArguments: { channel_name: 'Town Square' },
                finalContent: continuationText,
                title: 'Aimock ask policy',
            }),
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('ask policy tool shows pending approval in DM', async ({ page }) => {
        test.setTimeout(120000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.openRHS();
        await aiPlugin.sendMessage(userMessage);

        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        await expect(rhsContainer).toBeVisible();

        const botPosts = rhsContainer.locator('[data-testid="llm-bot-post"]');
        const latestBotPost = botPosts.last();
        await expect(latestBotPost).toBeVisible({ timeout: 90000 });
        await expect(latestBotPost.getByText(getChannelInfoLabel, { exact: true })).toBeVisible({
            timeout: 90000,
        });

        const acceptButton = rhsContainer.getByRole('button', { name: /^accept$/i });
        const rejectButton = rhsContainer.getByRole('button', { name: /^reject$/i });
        await expect(acceptButton).toBeVisible();
        await expect(rejectButton).toBeVisible();
        await expect(latestBotPost.getByText(continuationText)).not.toBeVisible();

        await acceptButton.click();
        await expect(latestBotPost.getByText(continuationText)).toBeVisible({ timeout: 60000 });
        await expect(page.getByRole('button', { name: /stop/i })).not.toBeVisible({ timeout: 30000 });
    });
});
