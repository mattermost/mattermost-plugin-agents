// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, type Locator, type Page} from '@playwright/test';

import {containerTest} from 'helpers/container-fixture';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {SystemConsoleHelper} from 'helpers/system-console';
import {OpenAIMockContainer, RunOpenAIMocks} from 'helpers/openai-mock';
import RunSystemConsoleContainer, {adminUsername, adminPassword} from 'helpers/system-console-container';
import {
    MOCK_OAUTH_AUTHORIZE_URL,
    MOCK_OAUTH_SERVER_URL,
    buildMCPOAuthConnectedMocks,
    buildMCPOAuthMocks,
} from 'helpers/mcp-oauth-mock';
import {buildMCPToolsServerMocks} from 'helpers/mcp-tools-mock';

/**
 * Test Suite: Adding a remote MCP server through the connect-and-review wizard
 *
 * The admin saves the server, signs in when it uses OAuth, and reviews its
 * tools, which are grouped and pre-set from the server's readOnlyHint
 * annotations. Accepting saves an explicit policy for every tool. Skipping
 * the connection saves the server with no policies; the Tools tab then marks
 * its tools as not reviewed and opens the same review for them.
 */

const OAUTH_SERVER_NAME = 'Ticket Tracker';
const PLAIN_SERVER_NAME = 'Docs Search';
const PLAIN_SERVER_PATH = '/mock-docs-mcp';

const oauthTools = [
    {name: 'list_tickets', description: 'List open tickets', readOnlyHint: true},
    {name: 'get_ticket', description: 'Get one ticket', readOnlyHint: true},
    {name: 'close_ticket', description: 'Close a ticket', readOnlyHint: false},
];
const plainTools = [
    {name: 'search_docs', description: 'Search the documentation', readOnlyHint: true},
    {name: 'publish_doc', description: 'Publish a document'},
];

// Screenshots for the pull request are taken only when SHOT_DIR is set.
async function shot(target: Page | Locator, name: string) {
    const dir = process.env.SHOT_DIR;
    if (dir) {
        await target.screenshot({path: `${dir}/${name}.png`});
    }
}

type SavedMCPServer = {name: string; tool_configs?: Array<{name: string; policy: string; enabled: boolean}>};

// savedToolConfigs returns a saved server's tool configs sorted by name:
// discovery does not list tools in a fixed order.
async function savedToolConfigs(page: Page, name: string) {
    const toolConfigs = (await savedServer(page, name))?.tool_configs ?? [];
    return [...toolConfigs].sort((a, b) => a.name.localeCompare(b.name));
}

async function savedServer(page: Page, name: string): Promise<SavedMCPServer | undefined> {
    const config = await page.evaluate(async () => {
        const response = await fetch('/plugins/mattermost-ai/admin/config', {
            headers: {'X-Requested-With': 'XMLHttpRequest'},
        });
        return response.json();
    });
    return (config.mcp?.servers ?? []).find((server: SavedMCPServer) => server.name === name);
}

async function openAddServerWizard(page: Page, baseURL: string): Promise<Locator> {
    const mmPage = new MattermostPage(page);
    const systemConsole = new SystemConsoleHelper(page);
    await mmPage.login(baseURL, adminUsername, adminPassword);
    await systemConsole.navigateToPluginConfig(baseURL);

    await page.getByRole('button', {name: 'Add Remote MCP Server'}).click();
    const dialog = page.getByRole('dialog', {name: 'Add remote MCP server'});
    await expect(dialog).toBeVisible();
    return dialog;
}

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const test = containerTest(async (own) => {
    const mattermost = own(await RunSystemConsoleContainer({
        mcp: {
            enabled: true,
            enablePluginServer: false,
            embeddedServer: {enabled: true},
            idleTimeoutMinutes: 30,
            servers: [],
        },

        // The MCP panel is shown once an AI service is configured.
        services: [
            {
                id: 'mock-service',
                name: 'Mock Service',
                type: 'openaicompatible',
                apiKey: 'mock',
                apiURL: 'http://openai:8080',
                defaultModel: 'gpt-mock',
                useResponsesAPI: false,
            },
        ],
        bots: [
            {
                id: 'mock-bot',
                name: 'mockbot',
                displayName: 'Mock Bot',
                serviceID: 'mock-service',
                customInstructions: '',
                enabledNativeTools: [],
            },
        ],
    }));
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return {mattermost, openAIMock};
});

test.describe('MCP add server wizard', () => {
    test.beforeAll(async ({containers}) => {
        ({mattermost, openAIMock} = containers);
        await openAIMock.addMocks([
            ...buildMCPOAuthMocks(),
            ...buildMCPOAuthConnectedMocks(oauthTools),
            ...buildMCPToolsServerMocks(PLAIN_SERVER_PATH, plainTools),
        ]);
    });

    test('connects through OAuth and saves the reviewed policies for every tool', async ({page, context}) => {
        test.setTimeout(120000);
        await page.setViewportSize({width: 1280, height: 1000});

        const dialog = await openAddServerWizard(page, mattermost.url());
        await dialog.getByLabel('Name', {exact: true}).fill(OAUTH_SERVER_NAME);
        await dialog.getByLabel('Server URL').fill(MOCK_OAUTH_SERVER_URL);
        await dialog.getByLabel('Description').fill('Support tickets for the help desk.');
        await dialog.getByLabel('OAuth client ID').fill('test-client-id');
        await dialog.getByLabel('OAuth client secret').fill('test-client-secret');
        await shot(dialog, '1-details');

        await dialog.getByRole('button', {name: 'Save and connect'}).click();
        await expect(dialog.getByText(`Sign in to ${OAUTH_SERVER_NAME}`)).toBeVisible({timeout: 30000});
        await shot(dialog, '2-connect-oauth');

        // The provider lives on the Docker network, so the test approves as
        // the provider: it sends the admin's session back to the callback.
        const authorizeRequest = context.waitForEvent('request', (request) => request.url().startsWith(MOCK_OAUTH_AUTHORIZE_URL));
        const signInWindow = context.waitForEvent('page');
        await dialog.getByRole('button', {name: 'Sign in'}).click();
        const authorizeURL = new URL((await authorizeRequest).url());
        const callbackURL = new URL(authorizeURL.searchParams.get('redirect_uri') ?? '');
        callbackURL.searchParams.set('code', 'mock-authorization-code');
        callbackURL.searchParams.set('state', authorizeURL.searchParams.get('state') ?? '');
        const callback = await context.request.get(callbackURL.toString());
        expect(callback.status()).toBe(200);
        await (await signInWindow).close();
        await page.bringToFront();

        // Signing in moves the wizard on to the review, pre-set from the annotations.
        const readGroup = dialog.getByRole('group', {name: 'Read-only tools'});
        const writeGroup = dialog.getByRole('group', {name: 'Write tools'});
        await expect(readGroup).toBeVisible({timeout: 30000});
        await expect(readGroup.getByText('list_tickets', {exact: true})).toBeVisible();
        await expect(writeGroup.getByText('close_ticket', {exact: true})).toBeVisible();
        await expect(dialog.getByRole('combobox', {name: 'Approval policy for list_tickets'})).toHaveText('Auto Run (DM)');
        await expect(dialog.getByRole('combobox', {name: 'Approval policy for close_ticket'})).toHaveText('Ask Every Time');
        await shot(dialog, '3-review');

        // The server is saved before review, with no tool policies yet.
        expect((await savedServer(page, OAUTH_SERVER_NAME))?.tool_configs ?? []).toEqual([]);

        await dialog.getByRole('combobox', {name: 'Approval policy for get_ticket'}).click();
        await page.getByRole('option', {name: 'Ask Every Time'}).click();
        await dialog.getByRole('button', {name: 'Accept and save'}).click();
        await expect(dialog).toBeHidden({timeout: 30000});

        await expect.poll(() => savedToolConfigs(page, OAUTH_SERVER_NAME)).toEqual([
            {name: 'close_ticket', policy: 'ask', enabled: true},
            {name: 'get_ticket', policy: 'ask', enabled: true},
            {name: 'list_tickets', policy: 'auto_run_in_dm', enabled: true},
        ]);
    });

    test('skipping the connection warns, saves no policies, and leaves the tools to review', async ({page}) => {
        test.setTimeout(120000);
        await page.setViewportSize({width: 1280, height: 1000});

        const dialog = await openAddServerWizard(page, mattermost.url());
        await dialog.getByLabel('Name', {exact: true}).fill(PLAIN_SERVER_NAME);
        await dialog.getByLabel('Server URL').fill(`http://openai:8080${PLAIN_SERVER_PATH}`);
        await dialog.getByRole('button', {name: 'Skip connecting'}).click();

        await expect(dialog.getByText('Save without reviewing tools?')).toBeVisible();
        await expect(dialog.getByText(/ask for approval before every run/)).toBeVisible();
        await shot(dialog, '4-skip-warning');

        await dialog.getByRole('button', {name: 'Save without connecting'}).click();
        await expect(dialog).toBeHidden({timeout: 30000});
        await expect.poll(async () => (await savedServer(page, PLAIN_SERVER_NAME))?.name).toBe(PLAIN_SERVER_NAME);
        expect((await savedServer(page, PLAIN_SERVER_NAME))?.tool_configs ?? []).toEqual([]);

        // The Tools tab marks the server's tools as not reviewed.
        await page.getByRole('tab', {name: 'Tools', exact: true}).click();
        const reviewButton = page.getByRole('button', {name: `Review tools for ${PLAIN_SERVER_NAME}`});
        await expect(reviewButton).toBeVisible({timeout: 30000});
        const serverRow = page.getByRole('button', {name: new RegExp(PLAIN_SERVER_NAME)}).first();
        await serverRow.click();
        const notReviewed = page.getByText('Not reviewed', {exact: true});
        await expect(notReviewed).toHaveCount(2);
        await expect(page.getByText('2 tools not reviewed')).toBeVisible();
        // The innermost element holding both the Review button and a tool is the server's row.
        const serverCard = page.locator('div').filter({has: reviewButton}).filter({has: page.getByText('search_docs', {exact: true})}).last();
        await shot(serverCard, '5-tools-tab-not-reviewed');

        await reviewButton.click();
        const review = page.getByRole('dialog', {name: `Review tools for ${PLAIN_SERVER_NAME}`});
        await expect(review.getByRole('group', {name: 'Read-only tools'})).toBeVisible({timeout: 30000});
        await expect(review.getByRole('combobox', {name: 'Approval policy for search_docs'})).toHaveText('Auto Run (DM)');
        await expect(review.getByRole('combobox', {name: 'Approval policy for publish_doc'})).toHaveText('Ask Every Time');
        await shot(review, '6-review-from-tools-tab');

        await review.getByRole('button', {name: 'Accept and save'}).click();
        await expect(review).toBeHidden({timeout: 30000});
        await expect(notReviewed).toHaveCount(0);
        await expect.poll(() => savedToolConfigs(page, PLAIN_SERVER_NAME)).toEqual([
            {name: 'publish_doc', policy: 'ask', enabled: true},
            {name: 'search_docs', policy: 'auto_run_in_dm', enabled: true},
        ]);
    });
});
