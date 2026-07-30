// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import { test, expect, Page } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';

/**
 * Boundary coverage for MattermostPage.login channel_view handling. These tests drive a real
 * Mattermost container and only inject browser-level CSS to hide channel_view — no Mattermost
 * API is mocked. Each test spies on the page.reload boundary the helper uses (asserting the exact
 * number of helper-triggered reloads) and counts hide-injections via localStorage.
 */

const username = 'regularuser';
const password = 'regularuser';

const HIDE_APPLICATIONS_KEY = '__hideApplications__';

let mattermost: MattermostContainer;

test.beforeAll(async () => {
    test.setTimeout(180000);
    mattermost = await RunContainer();
});

test.afterAll(async () => {
    if (mattermost) {
        await mattermost.stop();
    }
});

type ReloadFn = Page['reload'];

// Wrap page.reload so we count exactly the reloads the login helper triggers; restore() puts the
// original method back.
function spyOnReload(page: Page): { count: () => number; restore: () => void } {
    const original: ReloadFn = page.reload.bind(page);
    let count = 0;
    page.reload = ((options?: Parameters<ReloadFn>[0]) => {
        count += 1;
        return original(options);
    }) as ReloadFn;
    return { count: () => count, restore: () => { page.reload = original; } };
}

// Hide channel_view via CSS so a `waitFor({state:'visible'})` never resolves, on every document.
async function injectAlwaysHideChannelView(page: Page): Promise<void> {
    await page.addInitScript((key) => {
        if (window.self !== window.top) {
            return;
        }
        const apply = () => {
            const style = document.createElement('style');
            style.textContent = '[data-testid="channel_view"]{display:none !important;}';
            (document.head || document.documentElement).appendChild(style);
            localStorage.setItem(key, String(Number(localStorage.getItem(key) || '0') + 1));
        };
        if (document.head || document.documentElement) {
            apply();
        } else {
            document.addEventListener('DOMContentLoaded', apply);
        }
    }, HIDE_APPLICATIONS_KEY);
}

// Hide channel_view only on the first document of the session; a reload renders normally.
async function injectHideChannelViewUntilReload(page: Page): Promise<void> {
    await page.addInitScript((key) => {
        if (window.self !== window.top) {
            return;
        }
        const deciderKey = '__hideDecider__';
        const seen = Number(localStorage.getItem(deciderKey) || '0') + 1;
        localStorage.setItem(deciderKey, String(seen));
        if (seen !== 1) {
            return;
        }
        const apply = () => {
            const style = document.createElement('style');
            style.textContent = '[data-testid="channel_view"]{display:none !important;}';
            (document.head || document.documentElement).appendChild(style);
            localStorage.setItem(key, String(Number(localStorage.getItem(key) || '0') + 1));
        };
        if (document.head || document.documentElement) {
            apply();
        } else {
            document.addEventListener('DOMContentLoaded', apply);
        }
    }, HIDE_APPLICATIONS_KEY);
}

async function readCounter(page: Page, key: string): Promise<number> {
    return page.evaluate((k) => Number(localStorage.getItem(k) || '0'), key);
}

test('surfaces an explicit auth failure without reload recovery on invalid credentials', async ({ page }) => {
    test.setTimeout(60000);
    const mmPage = new MattermostPage(page);
    const reload = spyOnReload(page);

    try {
        await expect(
            mmPage.login(mattermost.url(), username, 'wrong-password', { channelViewTimeoutMs: 8000 }),
        ).rejects.toThrow(/auth failure/i);
        await expect(page.getByText('Log in to your account')).toBeVisible();
    } finally {
        reload.restore();
    }

    expect(reload.count()).toBe(0);
});

test('recovers when the first channel document stalls but a reload is healthy', async ({ page }) => {
    test.setTimeout(90000);
    const mmPage = new MattermostPage(page);
    const reload = spyOnReload(page);
    await injectHideChannelViewUntilReload(page);

    try {
        await mmPage.login(mattermost.url(), username, password, {
            channelViewTimeoutMs: 30000,
            enableChannelViewReloadRecovery: true,
        });
        await expect(page.getByTestId('channel_view')).toBeVisible();
    } finally {
        reload.restore();
    }

    const hides = await readCounter(page, HIDE_APPLICATIONS_KEY);
    expect(hides).toBe(1);
    expect(reload.count()).toBe(1);
});

test('fails after bounded attempts when every channel document stalls', async ({ page }) => {
    test.setTimeout(90000);
    const mmPage = new MattermostPage(page);
    const reload = spyOnReload(page);
    await injectAlwaysHideChannelView(page);

    try {
        await expect(
            mmPage.login(mattermost.url(), username, password, {
                channelViewTimeoutMs: 24000,
                enableChannelViewReloadRecovery: true,
            }),
        ).rejects.toThrow(/channel_view never became visible/);
    } finally {
        reload.restore();
    }

    const hides = await readCounter(page, HIDE_APPLICATIONS_KEY);
    expect(hides).toBe(3);
    expect(reload.count()).toBe(2);
});
