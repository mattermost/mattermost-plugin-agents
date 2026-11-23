import { expect, type Browser, type Locator, type Page } from '@playwright/test';
import type { Channel } from '@mattermost/types/channels';
import { containerTest } from 'helpers/container-fixture';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { RunAIMockSidecar } from 'helpers/aimock-container';
import { buildToolCallAndTextResponse } from 'helpers/aimock-fixtures';
import { RunToolConfigAIMockContainer, setupRegularTestUser } from 'helpers/tool-config-container';

const requesterUsername = 'regularuser';
const requesterPassword = 'regularuser';
const onlookerUsername = 'onlookeruser';
const onlookerPassword = 'onlookeruser';
const botName = 'toolbot';

const embeddedReadChannelTool = 'mattermost__read_channel';
const privateChannelDisplayName = 'Sales Leadership';
const privatePostText = 'Quarterly pipeline note kept in the private channel.';
const userMessage = 'aimock channel private source read channel';
const continuationText = 'Aimock channel private source follow-up after the share decision.';

async function setupOnlooker(mattermost: MattermostContainer): Promise<void> {
    await mattermost.createUser(`${onlookerUsername}@sample.com`, onlookerUsername, onlookerPassword);
    await mattermost.addUserToTeam(onlookerUsername, 'test');
    const client = await mattermost.getClient(onlookerUsername, onlookerPassword);
    const user = await client.getMe();
    await client.savePreferences(user.id, [
        { user_id: user.id, category: 'tutorial_step', name: user.id, value: '999' },
        { user_id: user.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
        { user_id: user.id, category: 'crt_thread_pane_step', name: user.id, value: '999' },
    ]);
}

async function createPrivateSourceChannel(mattermost: MattermostContainer): Promise<string> {
    const client = await mattermost.getClient(requesterUsername, requesterPassword);
    const teams = await client.getMyTeams();
    const channel = await client.createChannel({
        team_id: teams[0].id,
        name: `sales-leadership-${Date.now()}`,
        display_name: privateChannelDisplayName,
        type: 'P',
    } as Channel);
    await client.createPost({ channel_id: channel.id, message: privatePostText } as any);
    return channel.id;
}

async function openOffTopic(page: Page, baseUrl: string): Promise<void> {
    await page.goto(`${baseUrl}/test/channels/off-topic`);
    await page.locator('#channelHeaderTitle').getByText('Off-Topic', { exact: true }).waitFor({
        state: 'visible',
        timeout: 10000,
    });
}

async function openThreadForPost(page: Page, message: string): Promise<Locator> {
    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, { exact: true }),
    }).last();
    await expect(post).toBeVisible({ timeout: 30000 });
    const replyIndicator = post.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({ timeout: 30000 });
    await replyIndicator.click();
    const rhs = page.locator('#rhsContainer');
    await rhs.locator('[data-testid="llm-bot-post"]').first().waitFor({ state: 'visible', timeout: 10000 });
    return rhs;
}

async function loginAndOpenOffTopic(browser: Browser, baseUrl: string, username: string, password: string) {
    const context = await browser.newContext();
    const page = await context.newPage();
    await new MattermostPage(page).login(baseUrl, username, password);
    await openOffTopic(page, baseUrl);
    return { context, page };
}

const test = containerTest(async (own) => {
    const mattermost = own(await RunToolConfigAIMockContainer([
        { name: 'read_channel', policy: 'auto_run_everywhere', enabled: true },
    ]));
    const aimock = own(await RunAIMockSidecar(mattermost.network));
    return { mattermost, aimock };
});

test.describe('Channel auto-run with a private source (Aimock)', () => {
    let mattermost: MattermostContainer;

    test.beforeAll(async ({ containers }) => {
        // setFixtures can fall back to restarting aimock, which outlasts the default hook timeout.
        test.setTimeout(180000);
        const { aimock } = containers;
        mattermost = containers.mattermost;
        await setupRegularTestUser(mattermost);
        await setupOnlooker(mattermost);
        const privateChannelID = await createPrivateSourceChannel(mattermost);
        await aimock.setFixtures(buildToolCallAndTextResponse({
            userMessage,
            toolCallId: 'call_aimock_channel_private_source_read_channel',
            toolName: embeddedReadChannelTool,
            toolArguments: { channel_id: privateChannelID, limit: 5 },
            finalContent: continuationText,
        }));
    });

    test('pauses for the requester, names the private channel only to them, and resumes on Keep private', async ({ browser }) => {
        test.setTimeout(180000);
        const baseUrl = mattermost.url();
        const requester = await loginAndOpenOffTopic(browser, baseUrl, requesterUsername, requesterPassword);
        const onlooker = await loginAndOpenOffTopic(browser, baseUrl, onlookerUsername, onlookerPassword);

        try {
            await new MattermostPage(requester.page).mentionBot(botName, userMessage);
            const rhs = await openThreadForPost(requester.page, `@${botName} ${userMessage}`);

            const notice = rhs.getByTestId('share-review-notice');
            await expect(notice).toBeVisible({ timeout: 90000 });
            await expect(notice).toContainText(privateChannelDisplayName);
            await expect(rhs.getByRole('button', { name: /^share$/i })).toBeVisible();
            const keepPrivate = rhs.getByRole('button', { name: /keep private/i });
            await expect(keepPrivate).toBeVisible();
            await expect(rhs.getByText(continuationText)).not.toBeVisible();

            const onlookerRhs = await openThreadForPost(onlooker.page, `@${botName} ${userMessage}`);
            await expect(onlookerRhs.locator('[data-testid="llm-bot-post"]').last()).toBeVisible();
            await expect(onlookerRhs.getByTestId('share-review-notice')).toHaveCount(0);
            await expect(onlookerRhs.getByText(privateChannelDisplayName)).toHaveCount(0);
            await expect(onlookerRhs.getByText(privatePostText)).toHaveCount(0);
            await expect(onlookerRhs.getByRole('button', { name: /keep private/i })).toHaveCount(0);

            await keepPrivate.click();
            await expect(rhs.getByText(continuationText)).toBeVisible({ timeout: 45000 });
            await expect(notice).not.toBeVisible();
            await expect(onlookerRhs.getByText(continuationText)).toBeVisible({ timeout: 45000 });
            await expect(onlookerRhs.getByText(privatePostText)).toHaveCount(0);
        } finally {
            await requester.context.close();
            await onlooker.context.close();
        }
    });
});
