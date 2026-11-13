// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Generative UI components in the RHS, where jsdom can't check the layout:
// host CSS (`.post-right__container hr`) must not erase Compass Divider's
// line, tables must not clip in the 300px column, and Compass ProgressBar
// must render at its default size.

import { expect, type Locator, type Page } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import { withDeadline } from 'helpers/deadline';

import { AIMockHarness, RunAIMockHarness, setupAimockTestPage } from 'helpers/aimock-harness';

const HARNESS_START_MS = 360000;

const compassTest = containerTest(async (own) => {
    const harness = own(await withDeadline('Starting the aimock harness', HARNESS_START_MS, () => RunAIMockHarness({
        fixtureFile: 'generative-ui-compass.json',
        bot: { skills: { generativeUI: true } },
        config: { enableGenerativeUI: true },
    })));
    return { harness };
});

async function replyBlock(page: Page, harness: AIMockHarness, prompt: string): Promise<Locator> {
    const { aiPlugin, llmBotHelper } = await setupAimockTestPage(page, harness.mattermost.url());
    await aiPlugin.sendMessage(prompt);
    await llmBotHelper.waitForStreamingComplete();
    const block = page.getByTestId('mattermost-ai-rhs').getByTestId('generative-ui-block').last();
    await expect(block).toBeVisible();
    return block;
}

compassTest.describe('Generative UI Compass components in the RHS', () => {
    compassTest.describe.configure({ mode: 'serial' });

    let harness: AIMockHarness;

    compassTest.beforeAll(async ({ containers }) => {
        ({ harness } = containers);
    });

    compassTest('draws a visible Divider line', async ({ page }) => {
        compassTest.setTimeout(120000);
        const block = await replyBlock(page, harness, 'genui-text');

        const dividers = block.locator('hr');
        await expect(dividers).toHaveCount(2);
        for (const divider of await dividers.all()) {
            await expect(divider).toHaveCSS('border-top-width', '1px');
            await expect(divider).toHaveCSS('border-top-style', 'solid');
        }
        await expect(block.getByText('Deployed to staging')).toBeVisible();
        await expect(block.getByText('Shift', { exact: true })).toBeVisible();
        await expect(block.getByText('No open incidents')).toBeVisible();
    });

    compassTest('shows every table cell without clipping', async ({ page }) => {
        compassTest.setTimeout(120000);
        const block = await replyBlock(page, harness, 'genui-incident');

        await expect(block.getByRole('table')).toBeHidden();
        const stacked = block.getByTestId('generative-ui-table-narrow');
        for (const cell of ['payments', '@alice', '130 min', 'Resolved', 'auth', '@dan', 'Down']) {
            await expect(stacked.getByText(cell, { exact: true })).toBeVisible();
        }
        const overflow = await stacked.evaluate((element) => element.scrollWidth - element.clientWidth);
        expect(overflow).toBeLessThanOrEqual(0);

        await expect(block.getByRole('img', { name: 'payments: 130 min' })).toBeVisible();
        await expect(block.getByRole('progressbar')).toHaveCount(0);
    });

    compassTest('renders Progress at the Compass default size with its caption announced', async ({ page }) => {
        compassTest.setTimeout(120000);
        const block = await replyBlock(page, harness, 'genui-status');

        const milestones = block.getByRole('progressbar', { name: 'Milestones' });
        await expect(milestones).toHaveAttribute('aria-valuetext', '3 of 5 done');
        await expect(milestones).toHaveCSS('height', '8px');
    });
});
