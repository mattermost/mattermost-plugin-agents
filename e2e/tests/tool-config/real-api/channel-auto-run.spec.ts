import { test, expect, type Locator, type Page } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIMockContainer, RunAIMockSidecar } from 'helpers/aimock-container';
import { buildToolCallAndTextResponse } from 'helpers/aimock-fixtures';
import { RunToolConfigAIMockContainer, setupRegularTestUser } from 'helpers/tool-config-container';
import { createToolConfigAPIHelper } from 'helpers/tool-config';

const username = 'regularuser';
const password = 'regularuser';
const botName = 'toolbot';

const embeddedReadChannelTool = 'mattermost__read_channel';
const readChannelConfigName = 'read_channel';
const readChannelLabel = 'Read Channel';

async function getTownSquareChannelID(mattermost: MattermostContainer): Promise<string> {
    const userClient = await mattermost.getClient(username, password);
    const teams = await userClient.getMyTeams();
    const channels = await userClient.getMyChannels(teams[0].id);
    const townSquare = channels.find((channel) => channel.name === 'town-square');

    if (!townSquare) {
        throw new Error('town-square channel not found');
    }

    return townSquare.id;
}

async function waitForSentPost(page: Page, message: string, timeout = 30000): Promise<Locator> {
    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, { exact: true }),
    }).last();
    await expect(post).toBeVisible({ timeout });
    return post;
}

async function openThreadForPost(post: Locator, timeout = 30000): Promise<void> {
    const replyIndicator = post.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({ timeout });
    await replyIndicator.click();
    const rhs = post.page().locator('#rhsContainer');
    await rhs.waitFor({ state: 'visible', timeout: 10000 });
    await rhs.locator('[data-testid="llm-bot-post"]').first().waitFor({ state: 'visible', timeout: 10000 });
}

async function mentionBotAndOpenThread(
    page: Page,
    mmPage: MattermostPage,
    message: string,
    timeout = 30000,
): Promise<void> {
    await mmPage.mentionBot(botName, message);
    const post = await waitForSentPost(page, `@${botName} ${message}`, timeout);
    await openThreadForPost(post, timeout);
}

async function closeRHSIfOpen(page: Page): Promise<void> {
    const rhs = page.locator('#rhsContainer');
    const closeButton = rhs.getByRole('button', { name: /close rhs|close/i }).first();
    if (await rhs.isVisible().catch(() => false) && await closeButton.isVisible().catch(() => false)) {
        await closeButton.click();
        await expect(rhs).not.toBeVisible({ timeout: 10000 });
    }
}

test.describe('Channel Auto Run Policy (Aimock)', () => {
    test.describe.configure({ mode: 'serial' });

    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;
    let townSquareChannelID: string;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunToolConfigAIMockContainer([
            { name: readChannelConfigName, policy: 'auto_run_in_dm', enabled: true },
        ]);
        await setupRegularTestUser(mattermost);
        townSquareChannelID = await getTownSquareChannelID(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: buildToolCallAndTextResponse({
                userMessage: 'aimock channel dm-only read channel',
                toolCallId: 'call_aimock_channel_dm_only_read_channel',
                toolName: embeddedReadChannelTool,
                toolArguments: { channel_id: townSquareChannelID, limit: 5 },
                finalContent: 'Aimock channel dm-only follow-up after share.',
                title: 'Aimock channel dm-only',
            }),
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('auto_run_in_dm in channel requires call approval and then share approval', async ({ page }) => {
        test.setTimeout(180000);

        const userMessage = 'aimock channel dm-only read channel';
        const continuationText = 'Aimock channel dm-only follow-up after share.';

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.goto(`${mattermost.url()}/test/channels/off-topic`);
        await page.locator('#channelHeaderTitle').getByText('Off-Topic', { exact: true }).waitFor({
            state: 'visible',
            timeout: 10000,
        });

        await mentionBotAndOpenThread(page, mmPage, userMessage);

        const rhs = page.locator('#rhsContainer');
        const latestBotPost = rhs.locator('[data-testid="llm-bot-post"]').last();
        await expect(latestBotPost.getByText(readChannelLabel, { exact: true })).toBeVisible({
            timeout: 90000,
        });

        const acceptButton = rhs.getByRole('button', { name: /^accept$/i });
        const shareButton = rhs.getByRole('button', { name: /^share$/i });
        const keepPrivateButton = rhs.getByRole('button', { name: /keep private/i });

        await expect(acceptButton).toBeVisible({ timeout: 30000 });
        await expect(shareButton).not.toBeVisible();
        await expect(keepPrivateButton).not.toBeVisible();
        await expect(rhs.getByText(continuationText)).not.toBeVisible();

        await acceptButton.click();
        await expect(shareButton).toBeVisible({ timeout: 30000 });
        await expect(keepPrivateButton).toBeVisible();
        await expect(rhs.getByText(continuationText)).not.toBeVisible();

        await shareButton.click();
        await expect(rhs.getByText(continuationText)).toBeVisible({ timeout: 45000 });
        await expect(rhs.getByRole('button', { name: /^share$/i })).not.toBeVisible();
    });

    test('auto_run_everywhere in channel skips approval and shares automatically', async ({ page }) => {
        test.setTimeout(180000);

        const userMessage = 'aimock channel everywhere read channel';
        const continuationText = 'Aimock channel everywhere auto-run completed.';

        const apiHelper = await createToolConfigAPIHelper(mattermost);
        await apiHelper.setEmbeddedServerToolConfigs([
            { name: readChannelConfigName, policy: 'auto_run_everywhere', enabled: true },
        ]);

        await aimock.setFixtures(
            buildToolCallAndTextResponse({
                userMessage,
                toolCallId: 'call_aimock_channel_everywhere_read_channel',
                toolName: embeddedReadChannelTool,
                toolArguments: { channel_id: townSquareChannelID, limit: 5 },
                finalContent: continuationText,
                title: 'Aimock channel everywhere',
            }),
        );

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.goto(`${mattermost.url()}/test/channels/off-topic`);
        await page.locator('#channelHeaderTitle').getByText('Off-Topic', { exact: true }).waitFor({
            state: 'visible',
            timeout: 10000,
        });

        await closeRHSIfOpen(page);
        await mentionBotAndOpenThread(page, mmPage, userMessage);

        const rhs = page.locator('#rhsContainer');
        await expect(rhs.getByText(continuationText)).toBeVisible({ timeout: 45000 });
        await expect(rhs.getByRole('button', { name: /^accept$/i })).not.toBeVisible();
        await expect(rhs.getByRole('button', { name: /^reject$/i })).not.toBeVisible();
        await expect(rhs.getByRole('button', { name: /^share$/i })).not.toBeVisible();
        await expect(rhs.getByRole('button', { name: /keep private/i })).not.toBeVisible();
        await expect(rhs.getByText('Auto-approved')).toBeVisible();
    });
});
