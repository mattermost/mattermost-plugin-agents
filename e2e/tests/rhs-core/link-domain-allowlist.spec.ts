import { expect, Locator, Page } from '@playwright/test';

import { containerTest } from 'helpers/container-fixture';

import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { SystemConsoleHelper } from 'helpers/system-console';
import { RunAIMockContainer } from 'helpers/plugincontainer';
import { mattermostAIAdminConfigApiFromClient } from 'helpers/plugin-http';
import { OpenAIMockContainer, RunOpenAIMocks, buildTextResponse, turnMocksWithTitleSiphon } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';
const adminUsername = 'admin';
const adminPassword = 'admin';

const ALLOWED_LINK_DOMAINS = 'allowed.example.com, *.trusted.example';
const REPLY_MARKER = 'Link allowlist check';
const REPLY_MARKDOWN = [
    REPLY_MARKER,
    '',
    '- [Allowed](https://allowed.example.com/page)',
    '- [Sub](https://docs.trusted.example/x)',
    '- [Blocked](https://evil.example.net/login)',
    '- https://evil.example.net/raw',
    '- [Town Square](/test/channels/town-square)',
].join('\n');

const test = containerTest(async (own) => {
    const mattermost = own(await RunAIMockContainer({
        config: { allowUnsafeLinks: true },
    }));
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

async function setAllowedLinkDomains(value: string): Promise<void> {
    const adminClient = await mattermost.getAdminClient();
    const api = mattermostAIAdminConfigApiFromClient(adminClient, mattermost.url());
    const current = await api.get();
    await api.put({ ...current, allowedLinkDomains: value });
}

async function askForLinks(page: Page): Promise<Locator> {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    await mmPage.login(mattermost.url(), username, password);
    await aiPlugin.resetState();

    await openAIMock.addMocks(turnMocksWithTitleSiphon(buildTextResponse(REPLY_MARKDOWN)));
    await aiPlugin.sendMessage('Give me some links');
    await aiPlugin.waitForBotResponse(REPLY_MARKER);

    const botPost = page.getByTestId('mattermost-ai-rhs').getByTestId('llm-bot-post').last();
    return botPost.getByTestId('posttext').last();
}

test.describe.serial('AI-generated link domain allowlist', () => {
    test.beforeAll(async ({ containers }) => {
        ({ mattermost, openAIMock } = containers);
    });

    test('keeps allowlisted links clickable and renders others as plain text', async ({ page }) => {
        await setAllowedLinkDomains(ALLOWED_LINK_DOMAINS);

        const postText = await askForLinks(page);

        await expect(postText.getByRole('link', { name: 'Allowed', exact: true })).toHaveAttribute('href', 'https://allowed.example.com/page');
        await expect(postText.getByRole('link', { name: 'Sub', exact: true })).toHaveAttribute('href', 'https://docs.trusted.example/x');

        await expect(postText.locator('a[href*="evil.example.net"]')).toHaveCount(0);
        await expect(postText.getByText('Blocked : https://evil.example.net/login', { exact: true })).toBeVisible();
        await expect(postText.getByText('https://evil.example.net/raw', { exact: true })).toBeVisible();
        await expect(postText.getByRole('link', { name: 'Town Square', exact: true })).toBeVisible();
    });

    test('keeps every link clickable when the allowlist is empty', async ({ page }) => {
        await setAllowedLinkDomains('');

        const postText = await askForLinks(page);

        await expect(postText.getByRole('link', { name: 'Blocked', exact: true })).toHaveAttribute('href', 'https://evil.example.net/login');
        await expect(postText.locator('a[href="https://evil.example.net/raw"]')).toBeVisible();
        await expect(postText.getByRole('link', { name: 'Allowed', exact: true })).toHaveAttribute('href', 'https://allowed.example.com/page');
        await expect(postText).not.toContainText('Blocked : https://evil.example.net/login');
    });

    test('the Agents settings save the allowlist, rejects invalid entries, and hides it while links are plain text', async ({ page }) => {
        await setAllowedLinkDomains('');

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToAgentsSettings(mattermost.url(), 'general');
        await systemConsole.getFunctionsPanel().scrollIntoViewIfNeeded();

        const field = page.getByLabel('Allowed Link Domains (csv)');
        const renderLinks = page.getByRole('radiogroup', { name: 'Render AI-generated links' });
        const trueRadio = renderLinks.getByRole('radio', { name: 'True' });
        const falseRadio = renderLinks.getByRole('radio', { name: 'False' });

        await expect(trueRadio).toBeChecked();
        await expect(field).toHaveValue('');

        await field.fill('allowed.example.com, not a host');
        await systemConsole.clickSave();
        await expect(page.getByText('One or more entries are not valid domains.')).toBeVisible();

        await field.fill(ALLOWED_LINK_DOMAINS);
        await systemConsole.clickSave();
        await page.reload();
        await expect(field).toHaveValue(ALLOWED_LINK_DOMAINS);

        await falseRadio.click();
        await expect(field).toBeHidden();
        await trueRadio.click();
        await expect(field).toHaveValue(ALLOWED_LINK_DOMAINS);
    });
});
