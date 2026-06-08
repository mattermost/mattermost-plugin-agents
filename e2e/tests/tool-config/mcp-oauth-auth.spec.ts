// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';
import { createToolConfigAPIHelper } from 'helpers/tool-config';
import { ToolConfigUIHelper } from 'helpers/tool-config';
import {
    MOCK_OAUTH_SERVER_NAME,
    MOCK_OAUTH_SERVER_URL,
    registerMCPOAuthMocks,
} from 'helpers/mcp-oauth-mock';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('MCP OAuth Authentication', () => {
    test.beforeAll(async () => {
        mattermost = await RunSystemConsoleContainer({
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
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);
        await registerMCPOAuthMocks(openAIMock, responseTest);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
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
