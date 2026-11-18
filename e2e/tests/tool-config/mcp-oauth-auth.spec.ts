// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { expect, type Locator, type Page } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';
import { SystemConsoleHelper } from 'helpers/system-console';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';
import { createToolConfigAPIHelper } from 'helpers/tool-config';
import { ToolConfigUIHelper } from 'helpers/tool-config';
import {
    MOCK_OAUTH_AUTHORIZE_URL,
    MOCK_OAUTH_SERVER_NAME,
    MOCK_OAUTH_SERVER_URL,
    registerMCPOAuthMocks,
} from 'helpers/mcp-oauth-mock';

const test = containerTest(async (own) => {
    const mattermost = own(await RunSystemConsoleContainer({
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
                id: 'test-bot',
                name: 'testbot',
                displayName: 'Test Bot',
                serviceID: 'mock-service',
                customInstructions: '',
                enabledNativeTools: [],
            },
        ],
        mcp: {
            enabled: true,
            enablePluginServer: true,
            embeddedServer: { enabled: true },
            idleTimeoutMinutes: 30,
            servers: [
                {
                    name: MOCK_OAUTH_SERVER_NAME,
                    enabled: true,
                    baseURL: MOCK_OAUTH_SERVER_URL,
                    clientID: 'test-client-id',
                    clientSecret: 'test-client-secret',
                },
            ],
        },
    }));
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

async function openServerDialog(page: Page): Promise<Locator> {
    await new MattermostPage(page).login(mattermost.url(), adminUsername, adminPassword);
    await new SystemConsoleHelper(page).navigateToPluginConfig(mattermost.url());

    await page.getByRole('button', { name: `Edit ${MOCK_OAUTH_SERVER_NAME}` }).click();
    const dialog = page.getByRole('dialog', { name: `Edit ${MOCK_OAUTH_SERVER_NAME}` });
    await expect(dialog).toBeVisible();
    return dialog;
}

async function addAuthorizationParam(dialog: Locator, name: string, value: string) {
    await dialog.getByRole('button', { name: 'Add Parameter' }).click();
    await dialog.getByPlaceholder('Parameter value (e.g. offline)').last().fill(value);
    await dialog.getByPlaceholder('Parameter name (e.g. access_type)').last().fill(name);
}

test.describe('MCP OAuth Authentication', () => {
    test.beforeAll(async ({ containers }) => {
        ({ mattermost, openAIMock } = containers);
        await registerMCPOAuthMocks(openAIMock, responseTest);
    });

    test('embedded server should return authenticated=true and needsOAuth=false', async () => {
        test.setTimeout(60000);

        const apiHelper = await createToolConfigAPIHelper(mattermost);
        const adminClient = await mattermost.getAdminClient();
        const token = adminClient.getToken();

        const tools = await apiHelper.getUserMCPTools(token);
        expect(tools.servers).toBeDefined();

        const embeddedServer = tools.servers.find((s: any) => s.name === 'Mattermost');
        expect(embeddedServer).toBeDefined();
        expect(embeddedServer.authenticated).toBe(true);
        expect(embeddedServer.needsOAuth).toBe(false);
        expect(embeddedServer.authURL).toBeUndefined();
    });

    test('OAuth-requiring server should return needsOAuth=true with authURL', async () => {
        test.setTimeout(60000);

        const apiHelper = await createToolConfigAPIHelper(mattermost);
        const adminClient = await mattermost.getAdminClient();
        const token = adminClient.getToken();

        const tools = await apiHelper.getUserMCPTools(token);
        expect(tools.servers).toBeDefined();

        const oauthServer = tools.servers.find(
            (s: any) => s.name === MOCK_OAUTH_SERVER_NAME,
        );
        expect(oauthServer).toBeDefined();
        expect(oauthServer.authenticated).toBe(false);
        expect(oauthServer.needsOAuth).toBe(true);
        expect(oauthServer.authURL).toBeDefined();

        const authURL = new URL(oauthServer.authURL);
        expect(authURL.origin).toBe(mattermost.url());
        expect(authURL.pathname).toBe(`/plugins/mattermost-ai/mcp/oauth/${encodeURIComponent(MOCK_OAUTH_SERVER_NAME)}/start`);
    });

    test('disconnect endpoint should succeed for any server', async () => {
        test.setTimeout(60000);

        const apiHelper = await createToolConfigAPIHelper(mattermost);
        const adminClient = await mattermost.getAdminClient();
        const token = adminClient.getToken();

        await expect(
            apiHelper.disconnectMCPOAuth(token, MOCK_OAUTH_SERVER_NAME),
        ).resolves.toBeUndefined();

        const tools = await apiHelper.getUserMCPTools(token);
        const oauthServer = tools.servers.find(
            (s: any) => s.name === MOCK_OAUTH_SERVER_NAME,
        );
        expect(oauthServer).toBeDefined();
        expect(oauthServer.authenticated).toBe(false);
        expect(oauthServer.needsOAuth).toBe(true);
    });

    test('disconnect endpoint should succeed even for non-OAuth server', async () => {
        test.setTimeout(60000);

        const apiHelper = await createToolConfigAPIHelper(mattermost);
        const adminClient = await mattermost.getAdminClient();
        const token = adminClient.getToken();

        await expect(
            apiHelper.disconnectMCPOAuth(token, 'Mattermost'),
        ).resolves.toBeUndefined();

        const tools = await apiHelper.getUserMCPTools(token);
        const embeddedServer = tools.servers.find((s: any) => s.name === 'Mattermost');
        expect(embeddedServer).toBeDefined();
        expect(embeddedServer.authenticated).toBe(true);
        expect(embeddedServer.needsOAuth).toBe(false);
    });

    test('authorization parameters set on the server reach the authorization URL', async ({ page, context }) => {
        test.setTimeout(120000);

        const dialog = await openServerDialog(page);
        await addAuthorizationParam(dialog, 'access_type', 'offline');
        await addAuthorizationParam(dialog, 'prompt', 'consent');
        await dialog.getByRole('button', { name: 'Save', exact: true }).click();

        await expect(dialog.getByText(/Changing the OAuth authorization parameters/)).toBeVisible();
        await dialog.getByRole('button', { name: 'Save and sign in again' }).click();
        await expect(dialog.getByText(`Sign in to ${MOCK_OAUTH_SERVER_NAME}`)).toBeVisible({ timeout: 30000 });

        // The provider lives on the Docker network, so the browser's request
        // to it is what the test inspects.
        const authorizeRequest = context.waitForEvent('request', (request) => request.url().startsWith(MOCK_OAUTH_AUTHORIZE_URL));
        const signInWindow = context.waitForEvent('page');
        await dialog.getByRole('button', { name: 'Sign in' }).click();
        const params = new URL((await authorizeRequest).url()).searchParams;
        await (await signInWindow).close();

        expect(params.get('access_type')).toBe('offline');
        expect(params.get('prompt')).toBe('consent');
        expect(params.get('client_id')).toBe('test-client-id');
        expect(params.get('resource')).toBe(MOCK_OAUTH_SERVER_URL);
        expect(params.get('code_challenge_method')).toBe('S256');
        expect(params.get('code_challenge')).toBeTruthy();
        expect(params.get('state')).toBeTruthy();
    });

    test('a reserved authorization parameter is refused by the dialog and the API', async ({ page }) => {
        test.setTimeout(60000);

        const dialog = await openServerDialog(page);
        await addAuthorizationParam(dialog, 'state', 'fixed');
        await dialog.getByRole('button', { name: 'Save', exact: true }).click();
        await expect(dialog.getByText('state is set by the OAuth sign-in and cannot be changed.')).toBeVisible();
        await expect(dialog.getByRole('button', { name: 'Save and sign in again' })).toBeHidden();

        const adminClient = await mattermost.getAdminClient();
        const configURL = `${mattermost.url()}/plugins/mattermost-ai/admin/config`;
        const headers = { Authorization: `Bearer ${adminClient.getToken()}`, 'Content-Type': 'application/json' };
        const config = await (await fetch(configURL, { headers })).json();
        const server = config.mcp.servers.find((s: any) => s.name === MOCK_OAUTH_SERVER_NAME);
        server.authorizationParams = { ...server.authorizationParams, state: 'fixed' };

        const response = await fetch(configURL, { method: 'PUT', headers, body: JSON.stringify(config) });
        expect(response.status).toBe(400);

        const stored = await (await fetch(configURL, { headers })).json();
        const storedServer = stored.mcp.servers.find((s: any) => s.name === MOCK_OAUTH_SERVER_NAME);
        expect(storedServer.authorizationParams?.state).toBeUndefined();
    });

    test('system console should show updated OAuth description text', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        const toolsUI = new ToolConfigUIHelper(page);
        await toolsUI.navigateToToolsTab(mattermost.url());

        const oauthRow = page.getByText(MOCK_OAUTH_SERVER_NAME);
        await expect(oauthRow.first()).toBeVisible();

        await oauthRow.first().click();

        const description = page.getByText('You must authenticate to fetch this server');
        await expect(description).toBeVisible();
        await expect(description).toContainText('each user must authenticate separately');
    });
});
