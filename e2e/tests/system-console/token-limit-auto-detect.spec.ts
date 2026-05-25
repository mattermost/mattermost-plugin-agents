// spec: System Console - token-limit inputs reflect Bifrost-provided model metadata
// seed: e2e/tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Verifies the UX contract added in the Bifrost token-metadata work: when the
 * `/admin/models/fetch` endpoint returns a model with input/output token
 * limits, the corresponding inputs in the service form become disabled and
 * prefilled with those values. When the limits are absent, the inputs stay
 * editable.
 *
 * The Mattermost server is stubbed via Playwright route interception rather
 * than the Smocker upstream mock — we only need to control the plugin's own
 * model-list endpoint response, not Bifrost's upstream behavior.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Service token-limit auto-detect', () => {
    test.beforeAll(async () => {
        mattermost = await RunSystemConsoleContainer({ services: [], bots: [] });
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('disables and prefills token-limit inputs when models endpoint reports limits', async ({ page }) => {
        test.setTimeout(120000);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Intercept the model-list endpoint and return a single Anthropic model
        // with both token limits populated.
        await page.route('**/plugins/mattermost-ai/admin/models/fetch', async (route) => {
            await route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify([
                    {
                        id: 'claude-sonnet-4-5',
                        displayName: 'Claude Sonnet 4.5',
                        inputTokenLimit: 200000,
                        outputTokenLimit: 8192,
                    },
                ]),
            });
        });

        await mmPage.login(mattermost.url(), adminUsername, adminPassword, {
            channelViewTimeoutMs: 90000,
        });
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Add a new service, set type=anthropic with a key so the fetch fires,
        // then pick the model the mock returns.
        await systemConsole.getAddServiceButton().click();
        const serviceCard = page.locator('[class*="ServiceContainer"]').last();
        await serviceCard.click();

        await serviceCard.getByRole('textbox').first().fill('Bifrost-aware Anthropic');
        await serviceCard.getByRole('combobox').first().selectOption('anthropic');
        await serviceCard.getByPlaceholder(/api key/i).fill('test-api-key');

        // Wait for fetchModels to populate the dropdown and select the model.
        const modelInput = serviceCard.getByText('Claude Sonnet 4.5');
        await expect(modelInput).toBeVisible({ timeout: 10000 });
        await modelInput.click();

        // Token-limit fields: locate by sibling label text.
        const inputLimitField = serviceCard.locator('label', { hasText: 'Input token limit' }).locator('xpath=following-sibling::*[1]//input');
        const outputLimitField = serviceCard.locator('label', { hasText: 'Output token limit' }).locator('xpath=following-sibling::*[1]//input');

        await expect(inputLimitField).toHaveValue('200000');
        await expect(inputLimitField).toBeDisabled();
        await expect(outputLimitField).toHaveValue('8192');
        await expect(outputLimitField).toBeDisabled();
    });
});
