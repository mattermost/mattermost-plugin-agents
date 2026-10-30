import { expect, type Page, type TestInfo } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { registerMCPToolsServerMocks } from 'helpers/mcp-tools-mock';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildChatCompletionMockRule,
    buildTextResponse,
    titleGenerationMockRule,
} from 'helpers/openai-mock';
import { SystemConsoleHelper } from 'helpers/system-console';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: MCP servers limited to direct messages
 *
 * Salesforce is limited to direct messages; Docs is not. Each LLM mock only
 * matches a request with the content under test, so a bot reply proves the
 * plugin sent it: in a channel, the prompt section naming the DM-only server;
 * in a DM, the server's tool.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

const botUsername = 'salesbot';
const salesforceToolName = 'query_accounts';
const salesforceDescription = 'Customer accounts and opportunities from our CRM.';
const salesforceMockPath = '/salesforce-mcp/';
const docsMockPath = '/docs-mcp/';

// Only the channel prompt carries this section, and it names the server with
// its description.
const dmOnlyPromptMatch = `Salesforce: ${salesforceDescription}`;
const channelReply = 'Salesforce is only available in a direct message with me. Send me a DM and I can look up your accounts there.';
const dmReply = 'Here are your biggest accounts from Salesforce.';

const test = containerTest(async (own) => {
    const mattermost = own(await RunSystemConsoleContainer({
        enableChannelMentionToolCalling: true,
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
                id: 'sales-bot',
                name: botUsername,
                displayName: 'Sales Bot',
                serviceID: 'mock-service',
                customInstructions: '',
                disableTools: false,
                mcpDynamicToolLoading: false,
                autoEnableNewMCPTools: true,
                enabledNativeTools: [],
            },
        ],
        mcp: {
            enabled: true,
            enablePluginServer: false,
            embeddedServer: { enabled: true },
            idleTimeoutMinutes: 30,
            servers: [
                {
                    name: 'Salesforce',
                    enabled: true,
                    baseURL: `http://openai:8080${salesforceMockPath}`,
                    description: salesforceDescription,
                    dmOnly: true,
                },
                {
                    name: 'Docs',
                    enabled: true,
                    baseURL: `http://openai:8080${docsMockPath}`,
                    description: 'Product documentation.',
                },
            ],
        },
    }));
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

// addMocks resets Smocker, mocks and request history alike, so every test
// registers the MCP servers along with its LLM rules.
async function registerMocks() {
    await openAIMock.addMocks([
        titleGenerationMockRule(),
        buildChatCompletionMockRule(buildTextResponse(channelReply), { bodyContains: dmOnlyPromptMatch }),
        buildChatCompletionMockRule(buildTextResponse(dmReply), { bodyContains: salesforceToolName }),
    ]);
    await registerMCPToolsServerMocks(openAIMock, salesforceMockPath, [
        { name: salesforceToolName, description: 'Query Salesforce accounts' },
    ]);
    await registerMCPToolsServerMocks(openAIMock, docsMockPath, [
        { name: 'search_docs', description: 'Search product documentation' },
    ]);
}

// Opens the thread the agent replied in under the post with exactly message.
async function openReplyThread(page: Page, message: string) {
    const post = page.locator('.post').filter({
        has: page.locator('.post-message__text').getByText(message, { exact: true }),
    }).last();
    await expect(post.getByText(/\d+ repl/i)).toBeVisible({ timeout: 30000 });
    await post.getByText(/\d+ repl/i).click();
    return page.locator('#rhsContainer');
}

async function attachScreenshot(page: Page, testInfo: TestInfo, name: string) {
    const path = testInfo.outputPath(`${name}.png`);
    await page.mouse.move(0, 0);
    await page.screenshot({ path });
    await testInfo.attach(name, { path, contentType: 'image/png' });
}

test.describe('MCP servers limited to direct messages (Mocked LLM)', () => {
    test.beforeAll(async ({ containers }) => {
        ({ mattermost, openAIMock } = containers);
    });

    test.beforeEach(async () => {
        await registerMocks();
    });

    test('the System Console shows the setting on each remote server', async ({ page }, testInfo) => {
        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToPluginConfig(mattermost.url());

        const settings = page.getByRole('radiogroup', { name: 'Direct Messages Only' });
        await expect(settings).toHaveCount(2);
        await expect(settings.nth(0).getByRole('radio', { name: 'True' })).toBeChecked();
        await expect(settings.nth(1).getByRole('radio', { name: 'False' })).toBeChecked();

        await page.getByRole('button', { name: 'Rename Salesforce' }).scrollIntoViewIfNeeded();
        await attachScreenshot(page, testInfo, 'system-console-setting');
    });

    test('a DM with the agent offers the server', async ({ page }, testInfo) => {
        test.setTimeout(90000);
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await mmPage.createAndNavigateToDMWithBot(mattermost, adminUsername, adminPassword, botUsername);

        const message = 'What are our biggest accounts?';
        await mmPage.sendChannelMessage(message);

        const rhs = await openReplyThread(page, message);
        await expect(rhs.getByText(dmReply)).toBeVisible({ timeout: 30000 });
        expect(await openAIMock.countRequestsContaining(salesforceToolName)).toBeGreaterThan(0);
        await attachScreenshot(page, testInfo, 'dm-reply');
    });

    test('a channel mention leaves the server out and points the user to a DM', async ({ page }, testInfo) => {
        test.setTimeout(90000);
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await page.goto(`${mattermost.url()}/test/channels/town-square`);

        const message = 'What are our biggest accounts?';
        await mmPage.mentionBot(botUsername, message);

        const rhs = await openReplyThread(page, `@${botUsername} ${message}`);
        await expect(rhs.getByText(channelReply)).toBeVisible({ timeout: 30000 });
        expect(await openAIMock.countRequestsContaining(salesforceToolName)).toBe(0);
        await attachScreenshot(page, testInfo, 'channel-reply');
    });
});
