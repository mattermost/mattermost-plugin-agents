// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Channel artifacts: channel admins pin an agent-built HTML file into a
// per-channel library (Claude Artifacts-style). Everyone in the channel can
// open the saved page from the Channel artifacts RHS; managers can rename,
// replace the HTML in place, and remove entries.

import {expect, type Locator, type Page} from '@playwright/test';
import {containerTest} from 'helpers/container-fixture';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {buildCreateFileSequence, buildTitleFixture, mergeFixtureFiles} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {RunToolConfigAIMockContainer, setupRegularTestUser} from 'helpers/tool-config-container';
import {
    artifactFrame,
    artifactWrappers,
    runArtifact,
    setupSecondViewer,
    SECOND_VIEWER_PASSWORD,
    SECOND_VIEWER_USERNAME,
} from 'helpers/html-artifact';
import {
    acceptNextConfirm,
    CHANNEL_ARTIFACT_CONTENT_URL,
    channelArtifactCard,
    channelArtifactsEmptyState,
    channelArtifactsRHS,
    makeChannelAdmin,
    openChannelArtifactsRHS,
    pinArtifactFromBookmarksMenu,
    releaseNotesHTML,
    RELEASE_NOTES_FILE_NAME,
    saveArtifactToChannel,
    setChannelArtifactsEnabled,
} from 'helpers/channel-artifacts';

const username = 'regularuser';
const password = 'regularuser';
const botName = 'toolbot';
const offTopicPath = '/test/channels/off-topic';

const test = containerTest(async (own) => {
    const mattermost = own(await RunToolConfigAIMockContainer({
        customInstructions: 'When asked to create a file, use the CreateFile tool.',
        defaultBotName: botName,
        botId: 'channel-artifact-bot',
        botDisplayName: 'Artifact Bot',
    }));
    const aimock = own(await RunAIMockSidecar(mattermost.network, {
        fixtures: {fixtures: [buildTitleFixture('Channel artifacts bootstrap')]},
    }));
    return {mattermost, aimock};
});

async function waitForBotUsername(mattermost: MattermostContainer, name: string): Promise<void> {
    const admin = await mattermost.getAdminClient();
    await expect.poll(async () => {
        try {
            await admin.getUserByUsername(name);
            return true;
        } catch {
            return false;
        }
    }, {timeout: 90000, intervals: [1000, 2000, 3000]}).toBe(true);
}

/**
 * Mentions the bot in Off-Topic so CreateFile lands on a shared channel post.
 * Opens the reply thread when the bot reply is collapsed under CRT.
 */
async function postHTMLInOffTopic(
    page: Page,
    mattermost: MattermostContainer,
    aimock: AIMockContainer,
    fileName: string,
    html: string,
    finalText: string,
): Promise<Locator> {
    const prompt = `${fileName} ${Date.now()}`;
    await aimock.setFixtures(mergeFixtureFiles(
        {fixtures: [buildTitleFixture(fileName)]},
        buildCreateFileSequence({
            userPrompt: prompt,
            fileName,
            fileContent: html,
            finalText,
            toolCallId: `call_channel_artifact_${Date.now()}`,
        }),
    ));
    const mm = new MattermostPage(page);
    // Avoid a second login mid-test — the login page never appears when
    // the session cookie is still valid and the wait times out.
    if (!(await page.getByTestId('channel_view').isVisible().catch(() => false))) {
        await mm.login(mattermost.url(), username, password);
    }
    await page.goto(`${mattermost.url()}${offTopicPath}`);
    await expect(page.locator('button#channelHeaderDropdownButton')).toBeVisible({timeout: 15000});
    await mm.mentionBot(botName, prompt);

    const userPost = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(`@${botName} ${prompt}`, {exact: true}),
    }).last();
    await expect(userPost).toBeVisible({timeout: 30000});

    const inlineBot = page.locator('#postListContent [data-testid="llm-bot-post"]').filter({hasText: finalText});
    try {
        await expect(inlineBot).toBeVisible({timeout: 45000});
        await expect(inlineBot.getByTestId('html-artifact-run-placeholder')).toBeVisible({timeout: 60000});
        return inlineBot;
    } catch {
        // CRT keeps the reply in the thread pane.
    }

    const replyIndicator = userPost.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({timeout: 90000});
    await replyIndicator.click();
    const threadBot = page.locator('#rhsContainer [data-testid="llm-bot-post"]').filter({hasText: finalText});
    await expect(threadBot).toBeVisible({timeout: 120000});
    await expect(threadBot.getByTestId('html-artifact-run-placeholder')).toBeVisible({timeout: 60000});
    return threadBot;
}

/** Reads the sandboxed content iframe's src from a host wrapper. */
async function artifactContentSrc(wrapper: Locator): Promise<string> {
    return (await wrapper.contentFrame().locator('iframe[data-testid="html-artifact-content"]').getAttribute('src')) || '';
}

test.describe('Channel artifacts', () => {
    test.describe.configure({mode: 'serial'});
    test.use({viewport: {width: 1440, height: 900}, permissions: ['notifications']});

    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;
    let channelId: string;

    test.beforeAll(async ({containers}) => {
        ({mattermost, aimock} = containers);
        await setChannelArtifactsEnabled(mattermost, true);
        const admin = await mattermost.getAdminClient();
        await admin.patchConfig({
            ServiceSettings: {CollapsedThreads: 'default_off'},
            EmailSettings: {EnablePreviewModeBanner: false},
        } as any);
        await setupRegularTestUser(mattermost);
        await waitForBotUsername(mattermost, botName);
        channelId = await makeChannelAdmin(mattermost, username, 'test', 'off-topic');
        await setupSecondViewer(mattermost);

        const client = await mattermost.getClient(username, password);
        const me = await client.getMe();
        await client.savePreferences(me.id, [
            {user_id: me.id, category: 'display_settings', name: 'collapsed_reply_threads', value: 'off'},
        ]);
    });

    test('admin saves from the card, opens from RHS, renames, replaces, and deletes', async ({page}) => {
        test.setTimeout(360000);

        const v1Text = `Here are the release notes v1 (${Date.now()}).`;
        const botPost = await postHTMLInOffTopic(
            page,
            mattermost,
            aimock,
            RELEASE_NOTES_FILE_NAME,
            releaseNotesHTML('v1.0'),
            v1Text,
        );
        await runArtifact(botPost);

        const entry = await saveArtifactToChannel(page, botPost, {title: 'Sprint release notes'});
        expect(entry.channel_id).toBe(channelId);
        expect(entry.title).toBe('Sprint release notes');
        expect(entry.version).toBe(1);

        // Leave any thread RHS so the channel-header menu targets Off-Topic.
        await page.goto(`${mattermost.url()}${offTopicPath}`);
        await expect(page.locator('button#channelHeaderDropdownButton')).toBeVisible({timeout: 15000});

        const rhs = await openChannelArtifactsRHS(page);
        await expect(rhs).toContainText(/1 artifact/);
        const listCard = channelArtifactCard(rhs, entry.id);
        await expect(listCard).toContainText('Sprint release notes');

        await listCard.getByTestId(`channel-artifact-open-${entry.id}`).click();
        const viewer = page.getByTestId('html-artifact-fullscreen');
        await expect(viewer).toBeVisible({timeout: 15000});
        const wrapper = artifactWrappers(viewer).first();
        expect(await artifactContentSrc(wrapper)).toMatch(CHANNEL_ARTIFACT_CONTENT_URL);
        const frame = artifactFrame(wrapper);
        await expect(frame.getByRole('heading', {name: 'Release notes'})).toBeVisible({timeout: 30000});
        await expect(frame.getByTestId('version')).toHaveText('v1.0');
        await frame.getByRole('button', {name: 'Show details'}).click();
        await expect(frame.getByTestId('details')).toBeVisible();
        await viewer.getByTestId('html-artifact-fullscreen-close').click();
        await expect(viewer).not.toBeVisible();

        await listCard.getByTestId(`channel-artifact-edit-${entry.id}`).click();
        const rename = rhs.getByTestId(`channel-artifact-rename-${entry.id}`);
        await rename.fill('Team release notes');
        await rename.press('Enter');
        await expect(listCard).toContainText('Team release notes', {timeout: 15000});

        const v2Text = `Here are the release notes v2 (${Date.now()}).`;
        const nextPost = await postHTMLInOffTopic(
            page,
            mattermost,
            aimock,
            'release-notes-v2.html',
            releaseNotesHTML('v2.0'),
            v2Text,
        );
        await runArtifact(nextPost);
        const updated = await saveArtifactToChannel(page, nextPost, {
            title: 'Team release notes',
            replaceId: entry.id,
        });
        expect(updated.id).toBe(entry.id);
        expect(updated.version).toBeGreaterThanOrEqual(2);

        await page.goto(`${mattermost.url()}${offTopicPath}`);
        await expect(page.locator('button#channelHeaderDropdownButton')).toBeVisible({timeout: 15000});

        const rhsAgain = await openChannelArtifactsRHS(page);
        await expect(channelArtifactCard(rhsAgain, entry.id)).toContainText(`v${updated.version}`);
        await channelArtifactCard(rhsAgain, entry.id).getByTestId(`channel-artifact-open-${entry.id}`).click();
        const viewer2 = page.getByTestId('html-artifact-fullscreen');
        await expect(viewer2).toBeVisible({timeout: 15000});
        await expect(artifactFrame(artifactWrappers(viewer2).first()).getByTestId('version')).toHaveText('v2.0', {timeout: 30000});
        await viewer2.getByTestId('html-artifact-fullscreen-close').click();

        acceptNextConfirm(page);
        await channelArtifactCard(rhsAgain, entry.id).getByTestId(`channel-artifact-delete-${entry.id}`).click();
        const restored = channelArtifactCard(channelArtifactsRHS(page), entry.id);
        await expect(restored).toBeVisible({timeout: 15000});
        await expect(restored).toContainText('Sprint release notes');
        await expect(channelArtifactsEmptyState(channelArtifactsRHS(page))).toHaveCount(0);
    });

    test('opens library from Channel Info and from Bookmarks → Add channel artifact', async ({page}) => {
        test.setTimeout(360000);

        const admin = await mattermost.getAdminClient();
        const license = await admin.getClientLicenseOld();
        const bookmarksLicensed = license?.IsLicensed === 'true';

        const finalText = `Info and bookmark notes (${Date.now()}).`;
        const botPost = await postHTMLInOffTopic(
            page,
            mattermost,
            aimock,
            RELEASE_NOTES_FILE_NAME,
            releaseNotesHTML('bookmark'),
            finalText,
        );
        await runArtifact(botPost);
        const entry = await saveArtifactToChannel(page, botPost, {title: 'Bookmarked notes'});

        await page.goto(`${mattermost.url()}${offTopicPath}`);
        await expect(page.locator('button#channelHeaderDropdownButton')).toBeVisible({timeout: 15000});

        // Channel Info row needs a Mattermost build with registerChannelInfoMenuAction.
        await page.locator('#channel-info-btn').click();
        await expect(page.getByTestId('channel_info_rhs-menu')).toBeVisible({timeout: 15000});
        const infoItem = page.getByRole('button', {name: 'Channel artifacts'});
        if (await infoItem.isVisible().catch(() => false)) {
            await infoItem.click();
            const rhs = channelArtifactsRHS(page);
            await expect(rhs).toBeVisible({timeout: 15000});
            await expect(channelArtifactCard(rhs, entry.id)).toBeVisible({timeout: 15000});
        } else {
            await openChannelArtifactsRHS(page);
            await expect(channelArtifactCard(channelArtifactsRHS(page), entry.id)).toBeVisible({timeout: 15000});
        }

        if (bookmarksLicensed) {
            await pinArtifactFromBookmarksMenu(page, entry);
            const bookmark = page.getByTestId('channel-bookmarks-container').getByText('Bookmarked notes');
            await expect(bookmark).toBeVisible({timeout: 15000});
            await bookmark.click();
        } else {
            await page.goto(`${mattermost.url()}${offTopicPath}?mm_ai_library_artifact=${entry.id}`);
        }

        const viewer = page.getByTestId('html-artifact-fullscreen');
        await expect(viewer).toBeVisible({timeout: 15000});
        await expect(artifactFrame(artifactWrappers(viewer).first()).getByTestId('version')).toHaveText('bookmark', {timeout: 30000});
        await expect(page).not.toHaveURL(/mm_ai_library_artifact=/);
        await viewer.getByTestId('html-artifact-fullscreen-close').click();
    });

    test('channel members can open a library artifact but cannot save or manage', async ({page, browser}) => {
        test.setTimeout(360000);

        const finalText = `Shared library notes (${Date.now()}).`;
        const botPost = await postHTMLInOffTopic(
            page,
            mattermost,
            aimock,
            RELEASE_NOTES_FILE_NAME,
            releaseNotesHTML('shared'),
            finalText,
        );
        await runArtifact(botPost);
        const entry = await saveArtifactToChannel(page, botPost, {title: 'Shared notes'});

        const viewerContext = await browser.newContext({viewport: {width: 1440, height: 900}, permissions: ['notifications']});
        try {
            const viewerPage = await viewerContext.newPage();
            await new MattermostPage(viewerPage).login(mattermost.url(), SECOND_VIEWER_USERNAME, SECOND_VIEWER_PASSWORD);
            await viewerPage.goto(`${mattermost.url()}${offTopicPath}`);
            await expect(viewerPage.locator('button#channelHeaderDropdownButton')).toBeVisible({timeout: 15000});

            await expect(viewerPage.getByTestId('html-artifact-save-to-channel')).toHaveCount(0);

            const rhs = await openChannelArtifactsRHS(viewerPage);
            const listCard = channelArtifactCard(rhs, entry.id);
            await expect(listCard).toBeVisible({timeout: 30000});
            await expect(listCard.getByTestId(`channel-artifact-edit-${entry.id}`)).toHaveCount(0);
            await expect(listCard.getByTestId(`channel-artifact-delete-${entry.id}`)).toHaveCount(0);

            await listCard.getByTestId(`channel-artifact-open-${entry.id}`).click();
            const viewer = viewerPage.getByTestId('html-artifact-fullscreen');
            await expect(viewer).toBeVisible({timeout: 15000});
            await expect(artifactFrame(artifactWrappers(viewer).first()).getByTestId('version')).toHaveText('shared', {timeout: 30000});
        } finally {
            await viewerContext.close();
        }
    });
});
