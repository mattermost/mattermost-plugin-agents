// spec: system-console-mcp-panel.plan.md - MCP Panel
// seed: e2e/tests/seed.spec.ts

import {test, expect} from '@playwright/test';

import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {SystemConsoleHelper} from 'helpers/system-console';
import {OpenAIMockContainer, RunOpenAIMocks} from 'helpers/openai-mock';
import RunSystemConsoleContainer, {adminUsername, adminPassword, type MCPServerConfig} from 'helpers/system-console-container';
import {
    MOCK_OAUTH_ACCESS_TOKEN,
    MOCK_OAUTH_SERVER_URL,
    MOCK_SERVER_SELF_DESCRIPTION,
    buildMCPOAuthConnectedMocks,
} from 'helpers/mcp-oauth-mock';

async function setupMattermost(servers: MCPServerConfig[] = []): Promise<MattermostContainer> {
    return RunSystemConsoleContainer({
        mcp: {
            enabled: true,
            enablePluginServer: false,
            idleTimeoutMinutes: 30,
            servers,
        },
        services: [
            {
                id: 'test-service',
                name: 'Test Service',
                type: 'openai',
                apiKey: 'test-key',
                orgId: '',
                defaultModel: 'gpt-4',
                tokenLimit: 16384,
                streamingTimeoutSeconds: 30,
                outputTokenLimit: 4096,
                useResponsesAPI: false,
            },
        ],
        bots: [
            {
                id: 'bot-1',
                name: 'testbot',
                displayName: 'Test Bot',
                serviceID: 'test-service',
                customInstructions: 'You are a helpful assistant',
                enableVision: false,
                enableTools: false,
            },
        ],
        defaultBotName: 'testbot',
    });
}

test.describe.serial('MCP Panel', () => {
    test('should keep Connection Idle Timeout empty when cleared', async ({page}) => {
        test.setTimeout(60000);
        let mattermost: MattermostContainer | undefined;
        let openAIMock: OpenAIMockContainer | undefined;

        try {
            mattermost = await setupMattermost();
            openAIMock = await RunOpenAIMocks(mattermost.network);

            const mmPage = new MattermostPage(page);
            const systemConsole = new SystemConsoleHelper(page);

            await mmPage.login(mattermost.url(), adminUsername, adminPassword);
            await systemConsole.navigateToPluginConfig(mattermost.url());

            const timeoutField = page.getByLabel(/Connection Idle Timeout \(minutes\)/i).or(
                page.locator('text=Connection Idle Timeout (minutes)').locator('..').getByRole('spinbutton'),
            );
            await timeoutField.scrollIntoViewIfNeeded();
            await expect(timeoutField).toHaveValue('30');

            await timeoutField.fill('');
            await expect(timeoutField).toHaveValue('');

            await systemConsole.clickSave();
            await page.reload();

            const reloadedTimeoutField = page.getByLabel(/Connection Idle Timeout \(minutes\)/i).or(
                page.locator('text=Connection Idle Timeout (minutes)').locator('..').getByRole('spinbutton'),
            );
            await expect(reloadedTimeoutField).toHaveValue('');
        } finally {
            if (openAIMock) {
                await openAIMock.stop();
            }
            if (mattermost) {
                await mattermost.stop();
            }
        }
    });

    test('should display the MCP OAuth callback URL with a copy button', async ({page, context}) => {
        test.setTimeout(60000);
        let mattermost: MattermostContainer | undefined;
        let openAIMock: OpenAIMockContainer | undefined;

        try {
            mattermost = await setupMattermost();
            openAIMock = await RunOpenAIMocks(mattermost.network);

            const mmPage = new MattermostPage(page);
            const systemConsole = new SystemConsoleHelper(page);

            await mmPage.login(mattermost.url(), adminUsername, adminPassword);
            await systemConsole.navigateToPluginConfig(mattermost.url());

            const callbackField = page.getByLabel(/MCP OAuth Callback URL/i);
            const callbackRow = callbackField.locator('xpath=ancestor::*[.//button][1]');
            await callbackField.scrollIntoViewIfNeeded();

            // The URL must be the SiteURL the server reports plus the plugin OAuth callback path.
            await expect(callbackField).toHaveValue(/\/plugins\/mattermost-ai\/oauth\/callback$/);
            await expect(callbackField).toHaveAttribute('readonly');

            const copyButton = callbackRow.getByRole('button', {name: /copy to clipboard/i});
            await expect(copyButton).toBeVisible();

            // navigator.clipboard only exists in secure contexts. Inside a container, testcontainers
            // reports the Docker gateway IP as the host, which is not a secure origin.
            expect(
                await page.evaluate(() => window.isSecureContext),
                `${mattermost.url()} is not a secure context; set TESTCONTAINERS_HOST_OVERRIDE=localhost when running e2e inside a container`,
            ).toBe(true);

            // Grant clipboard permissions so navigator.clipboard.writeText succeeds.
            await context.grantPermissions(['clipboard-read', 'clipboard-write']);

            const expectedValue = await callbackField.inputValue();
            await copyButton.click();

            // Button label flips to the confirmation state.
            await expect(callbackRow.getByRole('button', {name: /^copied$/i})).toBeVisible();

            const clipboardValue = await page.evaluate(() => navigator.clipboard.readText());
            expect(clipboardValue).toBe(expectedValue);
        } finally {
            if (openAIMock) {
                await openAIMock.stop();
            }
            if (mattermost) {
                await mattermost.stop();
            }
        }
    });

    test('auto-fills a remote server description from the server, then saves the edited text', async ({page}) => {
        test.setTimeout(90000);
        let mattermost: MattermostContainer | undefined;
        let openAIMock: OpenAIMockContainer | undefined;

        try {
            mattermost = await setupMattermost([{
                name: 'Tickets',
                enabled: true,
                baseURL: MOCK_OAUTH_SERVER_URL,
                headers: {Authorization: `Bearer ${MOCK_OAUTH_ACCESS_TOKEN}`},
            }]);
            openAIMock = await RunOpenAIMocks(mattermost.network);
            await openAIMock.addMocks(buildMCPOAuthConnectedMocks());

            const mmPage = new MattermostPage(page);
            const systemConsole = new SystemConsoleHelper(page);
            await mmPage.login(mattermost.url(), adminUsername, adminPassword);
            await systemConsole.navigateToPluginConfig(mattermost.url());

            const description = page.getByLabel('Description', {exact: true});
            await description.scrollIntoViewIfNeeded();
            await expect(description).toHaveValue('');

            await page.getByRole('button', {name: 'Auto-fill from server'}).click();
            await expect(description).toHaveValue(MOCK_SERVER_SELF_DESCRIPTION, {timeout: 30000});
            await expect(page.getByText('Filled from the description the server reports. Edit it as needed, then save.')).toBeVisible();

            const edited = `${MOCK_SERVER_SELF_DESCRIPTION} Use it for customer escalations.`;
            await description.fill(edited);
            await systemConsole.clickSave();
            await page.reload();

            const reloaded = page.getByLabel('Description', {exact: true});
            await reloaded.scrollIntoViewIfNeeded();
            await expect(reloaded).toHaveValue(edited);
        } finally {
            if (openAIMock) {
                await openAIMock.stop();
            }
            if (mattermost) {
                await mattermost.stop();
            }
        }
    });
});
