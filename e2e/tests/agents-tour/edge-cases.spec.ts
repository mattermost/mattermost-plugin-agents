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

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async () => {
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.beforeEach(async () => {
    const client = await mattermost.getClient(username, password);
    const user = await client.getMe();
    try {
        await client.deletePreferences(user.id, [{
            user_id: user.id,
            category: TOUR_PREFERENCE_CATEGORY,
            name: TOUR_PREFERENCE_NAME
        }]);
    } catch (e) {
        // Preference may not exist
    }
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

test.describe('Agents Tour - Edge Cases', () => {
    test('Browser resize during tour display keeps popover functional', async ({ page }) => {
        const mmPage = new MattermostPage(page);

        await mmPage.login(mattermost.url(), username, password);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        const pulsatingDot = page.getByTestId('agents-tour-dot');
        await expect(pulsatingDot).toBeVisible({ timeout: 10000 });
        await pulsatingDot.click();

        const tourPopover = page.locator('.tour-tip-tippy');
        await expect(tourPopover).toBeVisible({ timeout: 5000 });

        await page.setViewportSize({ width: 800, height: 600 });
        await expect(tourPopover).toBeVisible();
        await expect(page.getByText('Agents are ready to help')).toBeVisible();

        await page.setViewportSize({ width: 1400, height: 900 });
        await expect(tourPopover).toBeVisible();

        const closeButton = page.getByTestId('agents-tour-close');
        await expect(closeButton).toBeVisible();
        await closeButton.click();
        await expect(tourPopover).not.toBeVisible({ timeout: 5000 });
    });
});
