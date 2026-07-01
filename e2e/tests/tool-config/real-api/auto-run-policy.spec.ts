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

const userMessage = 'aimock dm auto-run get channel info';
const continuationText = 'Aimock DM auto-run completed after channel lookup.';

test.describe('Auto Run (DM) Policy (Aimock)', () => {
    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunToolConfigAIMockContainer([
            { name: getChannelInfoConfigName, policy: 'auto_run_in_dm', enabled: true },
        ]);
        await setupRegularTestUser(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: buildToolCallAndTextResponse({
                userMessage,
                toolCallId: 'call_aimock_dm_auto_get_channel_info',
                toolName: embeddedGetChannelInfoTool,
                toolArguments: { channel_name: 'Town Square' },
                finalContent: continuationText,
                title: 'Aimock DM auto-run',
            }),
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('auto_run embedded tool executes without approval in DM', async ({ page }) => {
        test.setTimeout(180000);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.openRHS();
        await aiPlugin.sendMessage(userMessage);

        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        const latestBotPost = rhsContainer.locator('[data-testid="llm-bot-post"]').last();
        await expect(latestBotPost).toBeVisible({ timeout: 90000 });

        const acceptButton = rhsContainer.getByRole('button', { name: /^accept$/i });
        const rejectButton = rhsContainer.getByRole('button', { name: /^reject$/i });
        await expect(acceptButton).not.toBeVisible();
        await expect(rejectButton).not.toBeVisible();

        await expect(latestBotPost.getByText(getChannelInfoLabel, { exact: true })).toBeVisible({
            timeout: 120000,
        });
        await expect(latestBotPost.getByText(continuationText)).toBeVisible({ timeout: 120000 });
        await expect(rhsContainer.getByText('Auto-approved').first()).toBeVisible({ timeout: 30000 });
        await expect(page.getByRole('button', { name: /stop/i })).not.toBeVisible({ timeout: 30000 });
    });
});
