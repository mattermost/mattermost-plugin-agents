import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { AIMockContainer, RunAIMockSidecar } from 'helpers/aimock-container';
import { buildToolCallAndTextResponse } from 'helpers/aimock-fixtures';
import { expandToolActivity, expectToolActivitySummary, toolCard } from 'helpers/llmbot-post';
import { RunToolConfigAIMockContainer, setupRegularTestUser } from 'helpers/tool-config-container';

const username = 'regularuser';
const password = 'regularuser';

const embeddedGetChannelInfoTool = 'mattermost__get_channel_info';
const getChannelInfoConfigName = 'get_channel_info';
const getChannelInfoLabel = 'Get Channel Info';

const userMessage = 'aimock dm auto-run get channel info';
const continuationText = 'Aimock DM auto-run completed after channel lookup.';

const test = containerTest(async (own) => {
    const mattermost = own(await RunToolConfigAIMockContainer([
        { name: getChannelInfoConfigName, policy: 'auto_run_in_dm', enabled: true },
    ]));
    const aimock = own(await RunAIMockSidecar(mattermost.network, {
        fixtures: buildToolCallAndTextResponse({
            userMessage,
            toolCallId: 'call_aimock_dm_auto_get_channel_info',
            toolName: embeddedGetChannelInfoTool,
            toolArguments: { channel_name: 'Town Square' },
            finalContent: continuationText,
            title: 'Aimock DM auto-run',
        }),
    }));
    return { mattermost, aimock };
});

test.describe('Auto Run (DM) Policy (Aimock)', () => {
    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async ({ containers }) => {
        ({ mattermost, aimock } = containers);
        await setupRegularTestUser(mattermost);
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

        await expect(latestBotPost.getByText(continuationText)).toBeVisible({ timeout: 120000 });
        await expect(page.getByRole('button', { name: /stop/i })).not.toBeVisible({ timeout: 30000 });

        // Auto-run tools never interrupt, so once the response finishes they
        // are only a collapsed "Used 1 tool" summary until it is expanded.
        await expect(latestBotPost.getByText(getChannelInfoLabel, { exact: true })).toHaveCount(0);
        await expectToolActivitySummary(latestBotPost, 1);

        const activityRounds = await expandToolActivity(latestBotPost);
        await expect(toolCard(activityRounds, getChannelInfoLabel)).toBeVisible({
            timeout: 120000,
        });
        await expect(rhsContainer.getByText('Auto-approved').first()).toBeVisible({ timeout: 30000 });
    });
});
