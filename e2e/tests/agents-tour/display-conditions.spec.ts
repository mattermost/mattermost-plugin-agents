// spec: specs/agents-tour.md
// seed: tests/seed.spec.ts

import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

const TOUR_PREFERENCE_CATEGORY = 'mattermost-ai-tutorial';
const TOUR_PREFERENCE_NAME = 'agents_tour_v1';
const TOUR_FINISHED_VALUE = '999';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

test.describe('Agents Tour - Display Conditions', () => {
    test('Tour does not appear when preference already set to FINISHED (999)', async ({ page }) => {
        const client = await mattermost.getClient(username, password);
        const user = await client.getMe();
        await client.savePreferences(user.id, [{
            user_id: user.id,
            category: TOUR_PREFERENCE_CATEGORY,
            name: TOUR_PREFERENCE_NAME,
            value: TOUR_FINISHED_VALUE
        }]);

        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        const pulsatingDot = page.getByTestId('agents-tour-dot');
        await expect(pulsatingDot).not.toBeVisible({ timeout: 5000 });

        const tourPopover = page.locator('.tour-tip-tippy');
        await expect(tourPopover).not.toBeVisible();

        const appBarIcon = page.locator('#app-bar-icon-mattermost-ai');
        await expect(appBarIcon).toBeVisible();
    });
});
