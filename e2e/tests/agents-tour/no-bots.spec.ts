// spec: specs/agents-tour.md
// seed: tests/seed.spec.ts

import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';

import { RunSystemConsoleContainer, adminUsername, adminPassword } from 'helpers/system-console-container';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';

const test = containerTest(async (own) => {
    const mattermost = own(await RunSystemConsoleContainer({
        services: [{
            id: 'mock-service',
            name: 'Mock Service',
            type: 'openaicompatible',
            apiKey: 'mock',
            apiURL: 'http://localhost:8080',
        }],
        bots: [],
    }));
    return { mattermost };
});

let mattermost: MattermostContainer;

test.beforeAll(async ({ containers }) => {
    ({ mattermost } = containers);
});

test.describe('Agents Tour - No Bots Configured', () => {
    test('Tour does not appear when no bots are configured', async ({ page }) => {
        const mmPage = new MattermostPage(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        const pulsatingDot = page.getByTestId('agents-tour-dot');
        await expect(pulsatingDot).not.toBeVisible({ timeout: 5000 });

        const tourPopover = page.locator('.tour-tip-tippy');
        await expect(tourPopover).not.toBeVisible();
    });
});
