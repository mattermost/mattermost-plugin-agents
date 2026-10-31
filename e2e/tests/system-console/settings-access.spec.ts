// spec: Agents settings access — the System Console delegates the Agents
// settings to read-only and read/write users.

import { expect, type Page } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import { withDeadline } from 'helpers/deadline';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { MATTERMOST_AI_PLUGIN_ID, mattermostAIAdminConfigApiFromClient } from 'helpers/plugin-http';
import { SystemConsoleHelper } from 'helpers/system-console';
import { RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Agents settings access
 *
 * The plugin configuration lives in the Agents product's Settings view. The
 * System Console only lists who besides system admins may view (read-only,
 * without credentials) or change (read/write) it.
 */

const SERVICE_API_KEY = 'sk-delegation-sentinel-key';
const PASSWORD = 'Delegate-pass1';
const VIEWER = 'settingsviewer';
const EDITOR = 'settingseditor';
const OUTSIDER = 'settingsoutsider';

const test = containerTest(async (own) => {
    const mattermost = own(await RunSystemConsoleContainer({
        services: [{
            id: 'delegationservice000000000',
            name: 'Delegation Service',
            type: 'openaicompatible',
            apiURL: 'http://openai-mock:11434',
            apiKey: SERVICE_API_KEY,
            defaultModel: 'gpt-4',
            tokenLimit: 16384,
            outputTokenLimit: 4096,
            streamingTimeoutSeconds: 30,
            useResponsesAPI: false,
        }],
        bots: [],
    }));
    own(await RunOpenAIMocks(mattermost.network));
    return { mattermost };
});

async function seedUser(mattermost: MattermostContainer, username: string): Promise<void> {
    await mattermost.createUser(`${username}@example.com`, username, PASSWORD);
    await mattermost.addUserToTeam(username, 'test');
    const client = await mattermost.getClient(username, PASSWORD);
    const me = await client.getMe();
    await client.savePreferences(me.id, [
        { user_id: me.id, category: 'tutorial_step', name: me.id, value: '999' },
        { user_id: me.id, category: 'onboarding_task_list', name: 'onboarding_task_list_show', value: 'false' },
        { user_id: me.id, category: 'crt_thread_pane_step', name: me.id, value: '999' },
    ]);
}

async function grant(page: Page, pickerName: string, username: string): Promise<void> {
    const picker = page.getByRole('combobox', { name: pickerName });
    await picker.click();
    await picker.fill(username);
    await page.getByRole('listbox').getByRole('option', { name: username, exact: true }).click();
}

const settingsRow = (page: Page) => page.getByRole('navigation', { name: 'Agents' }).getByRole('button', { name: 'Settings' });

test.describe.serial('Agents settings access', () => {
    let mattermost: MattermostContainer;

    test.beforeAll(async ({ containers }) => {
        ({ mattermost } = containers);
        await withDeadline('Seeding the delegated users', 60000, async () => {
            await seedUser(mattermost, VIEWER);
            await seedUser(mattermost, EDITOR);
            await seedUser(mattermost, OUTSIDER);
        });
    });

    test('the System Console grants access and opens the Agents settings', async ({ page }) => {
        test.setTimeout(90000);
        const mmPage = new MattermostPage(page);
        const settings = new SystemConsoleHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await settings.navigateToSystemConsole(mattermost.url());

        await grant(page, 'Search for people to give read and write access', EDITOR);
        await grant(page, 'Search for people to give read-only access', VIEWER);
        const [response] = await Promise.all([
            page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().includes(`/plugins/${MATTERMOST_AI_PLUGIN_ID}/admin/settings-access`)),
            page.getByRole('button', { name: 'Save', exact: true }).click(),
        ]);
        expect(response.status()).toBe(200);

        await page.getByRole('button', { name: 'Open Agents settings' }).click();
        await expect(page).toHaveURL(/\/agents\/settings$/);
        await expect(settings.getFunctionsPanel()).toBeVisible();
    });

    test('a read-only user sees the settings without credentials and cannot change them', async ({ page }) => {
        test.setTimeout(90000);
        const mmPage = new MattermostPage(page);
        const settings = new SystemConsoleHelper(page);

        await mmPage.login(mattermost.url(), VIEWER, PASSWORD);
        const configResponse = page.waitForResponse((r) => r.request().method() === 'GET' && r.url().endsWith(`/plugins/${MATTERMOST_AI_PLUGIN_ID}/admin/config`));
        await settings.navigateToAgentsSettings(mattermost.url(), 'general');

        const body = await (await configResponse).text();
        expect(body).toContain('Delegation Service');
        expect(body).not.toContain(SERVICE_API_KEY);

        await expect(page.getByText('You have read-only access')).toBeVisible();
        await expect(settingsRow(page)).toBeVisible();
        await expect(page.getByRole('radiogroup', { name: 'Render AI-generated links' }).getByRole('radio').first()).toBeDisabled();

        await settings.openSection('services');
        await expect(page.getByText('Delegation Service').first()).toBeVisible();
        await expect(page.getByRole('button', { name: /add.*ai.*service/i })).toHaveCount(0);
        await expect(page.getByRole('button', { name: 'Delete service' })).toHaveCount(0);
        await expect(settings.getSaveButton()).toHaveCount(0);
    });

    test('a read/write user changes a setting', async ({ page }) => {
        test.setTimeout(90000);
        const mmPage = new MattermostPage(page);
        const settings = new SystemConsoleHelper(page);

        await mmPage.login(mattermost.url(), EDITOR, PASSWORD);
        await settings.navigateToAgentsSettings(mattermost.url(), 'general');
        await expect(page.getByText('You have read-only access')).toHaveCount(0);

        await page.getByRole('radiogroup', { name: 'Render AI-generated links' }).getByRole('radio', { name: 'True' }).check();
        await settings.clickSave();
        await expect(settings.getSaveButton()).toHaveCount(0);

        const adminClient = await mattermost.getClient(adminUsername, adminPassword);
        const config = await mattermostAIAdminConfigApiFromClient(adminClient, mattermost.url()).get();
        expect(config.allowUnsafeLinks).toBe(true);
    });

    test('anyone else gets no Settings entry and no settings', async ({ page }) => {
        test.setTimeout(90000);
        const mmPage = new MattermostPage(page);

        await mmPage.login(mattermost.url(), OUTSIDER, PASSWORD);
        await page.goto(`${mattermost.url()}/plug/${MATTERMOST_AI_PLUGIN_ID}/agents/settings`);

        await expect(page.getByText("You don't have access to the Agents settings")).toBeVisible({ timeout: 15000 });
        await expect(page.getByRole('navigation', { name: 'Agents' })).toBeVisible();
        await expect(settingsRow(page)).toHaveCount(0);
    });
});
