// spec: Bot Management UI — legacy editor removed; notice + default bot from runtime
// seed: e2e/tests/seed.spec.ts

import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Bot Management UI
 *
 * Legacy card-based bot editing was removed from System Console; agents are managed from the Agents product page.
 */

const test = containerTest(async (own) => {
    const mattermost = own(await RunSystemConsoleContainer({
        services: [
            {
                id: 'test-service-id',
                name: 'Test Service',
                type: 'openaicompatible',
                apiURL: 'http://openai-mock:11434',
                apiKey: 'test-key',
                orgId: '',
                defaultModel: 'gpt-4',
                tokenLimit: 16384,
                streamingTimeoutSeconds: 30,
                outputTokenLimit: 4096,
                useResponsesAPI: false,
            },
        ],
        bots: [],
    }));
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Bot Management UI', () => {
    test.beforeAll(async ({ containers }) => {
        ({ mattermost, openAIMock } = containers);
    });

    test('should show moved notice instead of legacy bot editor', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToPluginConfig(mattermost.url());
        await systemConsole.waitForBotsPanel();

        await expect(page.getByText(/AI bot configuration has moved/i)).toBeVisible();
        await expect(page.getByRole('button', { name: /open agents/i })).toBeVisible();

        await expect(systemConsole.getAddBotButton()).not.toBeVisible();
        await expect(page.locator('[class*="BotContainer"]')).toHaveCount(0);
    });
});
