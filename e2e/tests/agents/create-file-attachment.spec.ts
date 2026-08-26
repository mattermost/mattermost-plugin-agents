// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Response file attachments with deterministic aimock fixtures.
// 1. DM: the built-in CreateFile tool auto-executes (no approval card) and the
//    created file is attached to the bot's streamed reply post.
// 2. Channel mention: the embedded mattermost__create_post tool with inline
//    `files` goes through the normal approval flow and the created post
//    carries the attachment.

import {test, expect, type Locator, type Page} from '@playwright/test';

import {AIMockContainer, RunAIMockSidecar} from 'helpers/aimock-container';
import {
    buildCreateFileSequence,
    buildPostToolSequence,
    buildTitleFixture,
    mergeFixtureFiles,
} from 'helpers/aimock-fixtures';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIPlugin} from 'helpers/ai-plugin';
import {expandToolActivity} from 'helpers/llmbot-post';
import {
    RunToolConfigAIMockContainer,
    setupRegularTestUser,
} from 'helpers/tool-config-container';

const username = 'regularuser';
const password = 'regularuser';
const botName = 'toolbot';

const createFileToolLabel = 'CreateFile';

type TownSquareChannel = {
    id: string;
    displayName: string;
    teamDisplayName: string;
};

async function getTownSquareChannel(mattermostInstance: MattermostContainer): Promise<TownSquareChannel> {
    const client = await mattermostInstance.getClient(username, password);
    const teams = await client.getMyTeams();
    const team = teams[0];
    const channels = await client.getMyChannels(team.id);
    const townSquare = channels.find((channel: {name: string}) => channel.name === 'town-square');

    if (!townSquare) {
        throw new Error('Could not find town-square channel');
    }

    return {
        id: townSquare.id,
        displayName: townSquare.display_name || 'Town Square',
        teamDisplayName: team.display_name || 'Test',
    };
}

/** File name rendered by the core attachment UI within scope (thread RHS, channel post, or Agents pane). */
function attachmentLocator(scope: Locator, fileName: string): Locator {
    const byTestId = scope.locator('[data-testid="fileAttachmentList"]').getByText(fileName);
    const byClassName = scope.locator('.post-image__name').filter({hasText: fileName});
    return byTestId.or(byClassName).first();
}

async function expectNoApprovalButtons(scope: Locator): Promise<void> {
    await expect(scope.getByRole('button', {name: /^accept$/i})).not.toBeVisible();
    await expect(scope.getByRole('button', {name: /^reject$/i})).not.toBeVisible();
    await expect(scope.getByRole('button', {name: /^share$/i})).not.toBeVisible();
    await expect(scope.getByRole('button', {name: /keep private/i})).not.toBeVisible();
}

async function waitForSentPost(page: Page, message: string, timeout = 30000): Promise<Locator> {
    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, {exact: true}),
    }).last();
    await expect(post).toBeVisible({timeout});
    return post;
}

async function openThreadForPost(post: Locator, timeout = 30000): Promise<void> {
    const replyIndicator = post.getByText(/\d+ repl/i);
    await expect(replyIndicator).toBeVisible({timeout});
    await replyIndicator.click();
    const rhs = post.page().locator('#rhsContainer');
    await rhs.waitFor({state: 'visible', timeout: 10000});
    await rhs.locator('[data-testid="llm-bot-post"]').first().waitFor({state: 'visible', timeout: 10000});
}

/** One ask-policy round in the thread RHS: Accept the call stage, then Share the result stage. */
async function approveAndShareToolRound(page: Page): Promise<void> {
    const rhs = page.locator('#rhsContainer');
    const acceptButton = rhs.getByRole('button', {name: /^accept$/i}).first();
    await expect(acceptButton).toBeVisible({timeout: 90000});
    await acceptButton.click();

    const shareButton = rhs.getByRole('button', {name: /^share$/i}).first();
    await expect(shareButton).toBeVisible({timeout: 45000});
    await shareButton.click();
}

test.describe('Response File Attachments (Aimock)', () => {
    // Serial: one shared Mattermost + aimock sidecar; each test swaps fixtures via setFixtures.
    test.describe.configure({mode: 'serial'});

    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;
    let townSquare: TownSquareChannel;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunToolConfigAIMockContainer({
            toolConfigs: [
                {name: 'get_channel_info', policy: 'ask', enabled: true},
                {name: 'create_post', policy: 'ask', enabled: true},
            ],
            customInstructions: 'When asked to create a file, use the CreateFile tool. When asked to post a message, call get_channel_info first, then create_post.',
            defaultBotName: botName,
            botId: 'tool-test-bot',
            botDisplayName: 'Tool Test Bot',
        });
        await setupRegularTestUser(mattermost);
        townSquare = await getTownSquareChannel(mattermost);
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: {fixtures: [buildTitleFixture('Create file bootstrap')]},
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('CreateFile auto-executes in DM and attaches the file to the reply', async ({page}) => {
        test.setTimeout(240000);

        const prompt = `create file dm ${Date.now()}`;
        const fileName = 'create-file-e2e.md';
        const fileContent = '# Create File E2E\n\nDeterministic content from aimock.';
        const finalText = `CREATE_FILE_FINAL_${Date.now()}`;

        await aimock.setFixtures(mergeFixtureFiles(
            {fixtures: [buildTitleFixture('Create file DM')]},
            buildCreateFileSequence({
                userPrompt: prompt,
                fileName,
                fileContent,
                finalText,
                toolCallId: `call_create_file_dm_${Date.now()}`,
            }),
        ));

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.openRHS();
        await aiPlugin.sendMessage(prompt);

        const rhsContainer = page.getByTestId('mattermost-ai-rhs');
        const latestBotPost = rhsContainer.locator('[data-testid="llm-bot-post"]').last();
        await expect(latestBotPost).toBeVisible({timeout: 90000});

        await expectNoApprovalButtons(rhsContainer);
        await expect(latestBotPost.getByText(finalText)).toBeVisible({timeout: 120000});

        // The resolved tool card lives behind the collapsed activity row and
        // renders without any approval controls once revealed.
        const activityRounds = await expandToolActivity(latestBotPost);
        await expect(activityRounds.getByText(createFileToolLabel, {exact: true})).toBeVisible({timeout: 120000});
        await expect(rhsContainer.getByText('Auto-approved').first()).toBeVisible({timeout: 30000});

        // The created file is attached to the bot's reply post.
        await expect(attachmentLocator(rhsContainer, fileName)).toBeVisible({timeout: 60000});

        // Still no approval controls after the reply completes.
        await expectNoApprovalButtons(rhsContainer);
        await expect(page.getByRole('button', {name: /stop/i})).not.toBeVisible({timeout: 30000});
    });

    test('create_post with inline files attaches the file to the created post', async ({page}) => {
        test.setTimeout(300000);

        const prompt = `create post with file ${Date.now()}`;
        const postText = `Post with inline attachment ${Date.now()}`;
        const finalText = `CREATE_POST_FILES_FINAL_${Date.now()}`;
        const fileName = 'channel-notes.md';

        await aimock.setFixtures(mergeFixtureFiles(
            {fixtures: [buildTitleFixture('Create post with files')]},
            buildPostToolSequence({
                userPromptMarker: prompt,
                infoCallId: `call_files_info_${Date.now()}`,
                createCallId: `call_files_create_${Date.now()}`,
                channelId: townSquare.id,
                channelDisplayName: townSquare.displayName,
                teamDisplayName: townSquare.teamDisplayName,
                postText,
                finalText,
                files: [{name: fileName, content: '# Channel Notes\n\nInline file from create_post.'}],
            }),
        ));

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.goto(`${mattermost.url()}/test/channels/off-topic`);
        await page.locator('#channelHeaderTitle').getByText('Off-Topic', {exact: true}).waitFor({
            state: 'visible',
            timeout: 10000,
        });

        await mmPage.mentionBot(botName, prompt);
        const mentionPost = await waitForSentPost(page, `@${botName} ${prompt}`);
        await openThreadForPost(mentionPost, 120000);

        // Round 1: get_channel_info, round 2: create_post — both ask-gated.
        await approveAndShareToolRound(page);
        await approveAndShareToolRound(page);

        const rhs = page.locator('#rhsContainer');
        await expect(rhs.getByText(finalText)).toBeVisible({timeout: 120000});

        // The created post in Town Square carries the message and the inline file.
        await page.goto(`${mattermost.url()}/test/channels/town-square`);
        const createdPost = page.locator('.post').filter({
            has: page.locator('.post-message__text').getByText(postText, {exact: true}),
        }).last();
        await expect(createdPost).toBeVisible({timeout: 30000});
        await expect(attachmentLocator(createdPost, fileName)).toBeVisible({timeout: 30000});
    });
});
