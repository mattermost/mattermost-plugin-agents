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
    test('drops a file onto the RHS new-tab and attaches it to the editor', async ({page}) => {
        const {aiPlugin} = await setupTestPage(page);

        const rhs = aiPlugin.getRhsContainer();
        await expect(rhs).toBeVisible();

        // The drop zone forwards files to the editor's file input and drops them if the
        // editor is still waiting on the bot DM channel.
        await expect(page.locator('[data-testid="rhs-file-drop-zone"] input[type="file"]')).toBeAttached({timeout: 30000});

        await dispatchFileDrop(
            page,
            '[data-testid="rhs-file-drop-zone"]',
            'agents-dnd.txt',
            'text/plain',
            'hello from the agents drag-and-drop test',
        );

        // Assert the user-visible attachment preview instead of the editor's
        // hidden file input so the test tracks real RHS composer behavior.
        await expect(rhs.locator('.file-preview__container')).toContainText('agents-dnd.txt');
    });
});
