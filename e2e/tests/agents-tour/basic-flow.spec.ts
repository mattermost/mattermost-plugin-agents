// spec: specs/agents-tour.md
// seed: tests/seed.spec.ts

import { test, expect, Locator, Page } from '@playwright/test';

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

const getTourPreferenceValue = async () => {
    const client = await mattermost.getClient(username, password);
    const prefs = await client.getMyPreferences();
    const tourPref = prefs.find(
        (p: { category: string; name: string; value: string }) =>
            p.category === TOUR_PREFERENCE_CATEGORY && p.name === TOUR_PREFERENCE_NAME
    );

    return tourPref?.value;
};

const openTour = async (page: Page): Promise<{pulsatingDot: Locator; tourPopover: Locator}> => {
    const pulsatingDot = page.getByTestId('agents-tour-dot').or(page.locator('[class*="DotContainer"]').first());
    await expect(pulsatingDot).toBeVisible({timeout: 10000});
    await pulsatingDot.click();

    const tourPopover = page.locator('.tour-tip-tippy');
    await expect(tourPopover).toBeVisible({timeout: 5000});
    await expect(page.getByText('Agents are ready to help')).toBeVisible();
    await expect(page.getByText('AI agents now live here')).toBeVisible();

    return {pulsatingDot, tourPopover};
};

const expectTourFinished = async (page: Page, pulsatingDot: Locator, tourPopover: Locator) => {
    await expect(tourPopover).not.toBeVisible({timeout: 5000});
    expect(await getTourPreferenceValue()).toBe(TOUR_FINISHED_VALUE);

    await page.reload();
    await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});
    await expect(pulsatingDot).not.toBeVisible({timeout: 5000});
    await expect(tourPopover).not.toBeVisible();
};

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

        const {pulsatingDot, tourPopover} = await openTour(page);

        const overlay = page.getByTestId('agents-tour-overlay').or(page.locator('[class*="TourOverlay"]'));
        await expect(overlay).toBeVisible();

        const closeButton = page.getByTestId('agents-tour-close').or(page.locator('.tour-tip-tippy button').filter({ has: page.locator('.icon-close') }));
        await closeButton.click();

        // 7. Verify popover disappears immediately
        await expect(tourPopover).not.toBeVisible({ timeout: 5000 });

        await expectTourFinished(page, pulsatingDot, tourPopover);

        // 10. Verify Agents icon is still functional
        const appBarIcon = page.locator('#app-bar-icon-mattermost-ai');
        await expect(appBarIcon).toBeVisible();
    });

    test('Dismiss via outside click finishes the tour', async ({page}) => {
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const {pulsatingDot, tourPopover} = await openTour(page);

        // Dispatching on the body exercises Tippy's outside-click handler.
        await page.evaluate(() => {
            for (const type of ['mousedown', 'mouseup', 'click']) {
                document.body.dispatchEvent(new MouseEvent(type, {
                    bubbles: true,
                    cancelable: true,
                    view: window,
                }));
            }
        });

        await expectTourFinished(page, pulsatingDot, tourPopover);
    });

    test('Dismiss via Escape finishes the tour', async ({page}) => {
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const {pulsatingDot, tourPopover} = await openTour(page);

        await page.keyboard.press('Escape');

        await expectTourFinished(page, pulsatingDot, tourPopover);
    });

    test('Pulsing dot stays aligned with the Agents icon after a layout shift (MM-67439)', async ({page}) => {
        const mmPage = new MattermostPage(page);
        await mmPage.login(mattermost.url(), username, password);
        await page.getByTestId('channel_view').waitFor({state: 'visible', timeout: 30000});

        const appBarIcon = page.locator('#app-bar-icon-mattermost-ai');
        const pulsatingDot = page.getByTestId('agents-tour-dot');
        await expect(appBarIcon).toBeVisible({timeout: 20000});
        await expect(pulsatingDot).toBeVisible({timeout: 20000});

        type Measurement = {iconY: number; iconX: number; dx: number; dy: number};
        const measure = async (): Promise<Measurement | null> => {
            return page.evaluate(() => {
                const icon = document.getElementById('app-bar-icon-mattermost-ai');
                const dot = document.querySelector('[data-testid="agents-tour-dot"]') as HTMLElement | null;
                if (!icon || !dot) {
                    return null;
                }
                const i = icon.getBoundingClientRect();
                const d = dot.getBoundingClientRect();
                return {
                    iconY: i.y,
                    iconX: i.x,
                    dx: Math.abs((d.x + d.width / 2) - (i.x + i.width / 2)),
                    dy: Math.abs((d.y + d.height / 2) - (i.y + i.height / 2)),
                };
            });
        };

        const aligned = (m: Measurement | null) => m !== null && m.dx < 20 && m.dy < 20;

        // Baseline: wait for the dot to align with the icon (handles the
        // AgentsTour's 500ms mount delay without a hardcoded sleep).
        await expect.poll(async () => aligned(await measure()), {timeout: 5000}).toBe(true);
        const before = await measure();
        expect(before).not.toBeNull();

        try {
            // Simulate a top-of-page layout shift the way an admin announcement
            // banner being shown (or dismissed) would: move the agents icon
            // without firing a window resize event.
            await page.evaluate(() => {
                const style = document.createElement('style');
                style.id = 'mm-67439-test-shift';
                style.textContent = '#app-bar-icon-mattermost-ai { margin-top: 80px !important; }';
                document.head.appendChild(style);
            });

            // Poll until the icon has actually moved and the dot has caught up.
            await expect.poll(async () => {
                const m = await measure();
                return m !== null && (m.iconY - before!.iconY) > 40 && aligned(m);
            }, {timeout: 2000}).toBe(true);

            // Reverse the shift to cover the "dismissed banner" direction:
            // the icon moves back up and the dot must follow.
            await page.evaluate(() => {
                document.getElementById('mm-67439-test-shift')?.remove();
            });

            await expect.poll(async () => {
                const m = await measure();
                return m !== null && Math.abs(m.iconY - before!.iconY) < 5 && aligned(m);
            }, {timeout: 2000}).toBe(true);
        } finally {
            await page.evaluate(() => {
                document.getElementById('mm-67439-test-shift')?.remove();
            });
        }
    });
});
