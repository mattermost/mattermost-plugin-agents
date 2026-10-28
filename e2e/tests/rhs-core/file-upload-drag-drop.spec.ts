// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect} from '@playwright/test';
import {containerTest} from 'helpers/container-fixture';
import type {Page} from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIPlugin} from 'helpers/ai-plugin';
import {OpenAIMockContainer, RunOpenAIMocks} from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

const test = containerTest(async (own) => {
    const mattermost = own(await RunContainer());
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async ({ containers }) => {
    ({ mattermost, openAIMock } = containers);
});

async function setupTestPage(page: Page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    await mmPage.login(mattermost.url(), username, password);
    await aiPlugin.openRHS();
    return {mmPage, aiPlugin};
}

// Synthesizes a file drop on the given element by building a DataTransfer in
// the page context, attaching the file payload, and dispatching the standard
// drag sequence (dragenter, dragover, drop). Browser automation can't perform
// a true OS-level drag, so this is the canonical way to exercise drag-drop
// handlers from Playwright.
async function dispatchFileDrop(page: Page, selector: string, fileName: string, mimeType: string, content: string) {
    const dataTransfer = await page.evaluateHandle(
        ({name, type, body}) => {
            const dt = new DataTransfer();
            dt.items.add(new File([body], name, {type}));
            return dt;
        },
        {name: fileName, type: mimeType, body: content},
    );

    await page.dispatchEvent(selector, 'dragenter', {dataTransfer});
    await page.dispatchEvent(selector, 'dragover', {dataTransfer});
    await page.dispatchEvent(selector, 'drop', {dataTransfer});
}

test.describe('Agents RHS drag-and-drop file upload', () => {
    test('drops a file onto the RHS new chat, uploads it and sends it with the message', async ({page}) => {
        const {aiPlugin} = await setupTestPage(page);

        const rhs = aiPlugin.getRhsContainer();
        await expect(rhs).toBeVisible();
        const composer = rhs.getByTestId('agent-chat-new-composer');

        // The pill composer offers attachments once the bot DM channel is ready.
        await aiPlugin.expectComposerUploadsReady(composer);

        await dispatchFileDrop(
            page,
            '[data-testid="rhs-file-drop-zone"]',
            'agents-dnd.txt',
            'text/plain',
            'hello from the agents drag-and-drop test',
        );

        const card = composer.getByRole('list', {name: 'Attachments'}).getByRole('listitem');
        await expect(card).toHaveCount(1);
        await expect(card).toContainText('agents-dnd.txt');
        await expect(card).toHaveAttribute('data-status', 'done', {timeout: 30000});
        await expect(rhs.getByTestId('agent-chat-drop-overlay')).toHaveCount(0);

        await aiPlugin.sendMessage('message with a dropped file');

        // The RHS opens the new thread; the user's post carries the file.
        const thread = rhs.getByTestId('rhs-thread');
        await expect(thread.getByTestId('post-message-text').getByText('message with a dropped file')).toBeVisible({timeout: 30000});
        await expect(thread.getByText('agents-dnd.txt')).toBeVisible({timeout: 30000});
    });

    test('a drag that carries no files shows no drop overlay', async ({page}) => {
        const {aiPlugin} = await setupTestPage(page);
        const rhs = aiPlugin.getRhsContainer();
        await aiPlugin.expectComposerUploadsReady(rhs);

        const dataTransfer = await page.evaluateHandle(() => {
            const dt = new DataTransfer();
            dt.setData('text/plain', 'just text');
            return dt;
        });
        await page.dispatchEvent('[data-testid="rhs-file-drop-zone"]', 'dragenter', {dataTransfer});
        await expect(rhs.getByTestId('agent-chat-drop-overlay')).toHaveCount(0);
    });
});
