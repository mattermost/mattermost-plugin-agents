// spec: specs/agents-tour.md
// seed: tests/seed.spec.ts

import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';

// Test configuration
const username = 'regularuser';
const password = 'regularuser';

// Tour-related constants
const TOUR_PREFERENCE_CATEGORY = 'mattermost-ai-tutorial';
const TOUR_PREFERENCE_NAME = 'agents_tour_v1';
const TOUR_FINISHED_VALUE = '999';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

// Setup for all tests in the file
test.beforeAll(async () => {
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

// Reset tour preference before each test
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
        // Preference may not exist, that's okay
    }
});

// Cleanup after all tests
test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

test.describe('Agents Tour - Basic Flow', () => {
    test('Full tour flow: appear, open, dismiss via X, preference saved, no reappear on reload', async ({ page }) => {
        const mmPage = new MattermostPage(page);

        // 1. Login as the test user
        await mmPage.login(mattermost.url(), username, password);
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        const pulsatingDot = page.getByTestId('agents-tour-dot').or(page.locator('[class*="DotContainer"]').first());
        await expect(pulsatingDot).toBeVisible({ timeout: 10000 });

        // 3. Click on the pulsating dot to open the tour popover
        await pulsatingDot.click();

        // 4. Wait for tour popover to appear and verify content
        const tourPopover = page.locator('.tour-tip-tippy');
        await expect(tourPopover).toBeVisible({ timeout: 5000 });

        // Verify meaningful content is displayed
        await expect(page.getByText('Agents are ready to help')).toBeVisible();
        await expect(page.getByText('AI agents now live here')).toBeVisible();

        const overlay = page.getByTestId('agents-tour-overlay').or(page.locator('[class*="TourOverlay"]'));
        await expect(overlay).toBeVisible();

        const closeButton = page.getByTestId('agents-tour-close').or(page.locator('.tour-tip-tippy button').filter({ has: page.locator('.icon-close') }));
        await closeButton.click();

        // 7. Verify popover disappears immediately
        await expect(tourPopover).not.toBeVisible({ timeout: 5000 });

        // 8. Verify preference was saved via API
        const client = await mattermost.getClient(username, password);
        const user = await client.getMe();
        const prefs = await client.getMyPreferences();
        const tourPref = prefs.find(
            (p: { category: string; name: string; value: string }) =>
                p.category === TOUR_PREFERENCE_CATEGORY && p.name === TOUR_PREFERENCE_NAME
        );
        expect(tourPref?.value).toBe(TOUR_FINISHED_VALUE);

        // 9. Reload page and verify tour does NOT reappear
        await page.reload();
        await page.getByTestId('channel_view').waitFor({ state: 'visible', timeout: 30000 });

        // Wait for any delayed rendering, then verify no tour elements
        await expect(pulsatingDot).not.toBeVisible({ timeout: 5000 });
        await expect(tourPopover).not.toBeVisible();

        // 10. Verify Agents icon is still functional
        const appBarIcon = page.locator('#app-bar-icon-mattermost-ai');
        await expect(appBarIcon).toBeVisible();
    });
});
