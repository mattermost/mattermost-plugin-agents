// spec: Service Management - Add First Service Through UI
// seed: e2e/tests/seed.spec.ts

import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { chooseCompassOption } from 'helpers/compass-select';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Service Management
 *
 * Tests UI-based service management operations in the system console.
 */

const test = containerTest(async (own) => {
    // Start container with NO pre-configured services
    const mattermost = own(await RunSystemConsoleContainer({
        services: [],
        bots: [],
    }));
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Service Management', () => {
    test.beforeAll(async ({ containers }) => {
        ({ mattermost, openAIMock } = containers);
    });

    test('should add and configure service through card-based UI', async ({ page }) => {
        // Login after a long shard can exceed 60s on CI.
        test.setTimeout(120000);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as admin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword, {
            channelViewTimeoutMs: 90000,
        });

        // Navigate to system console
        await systemConsole.navigateToAgentsSettings(mattermost.url(), 'services');

        // Count existing service cards before adding
        const existingServiceCards = page.locator('[class*="ServiceContainer"]');
        const initialCount = await existingServiceCards.count();

        // Click "Add an AI Service" button - this creates a new collapsed service card
        const addServiceButton = systemConsole.getAddServiceButton();
        await expect(addServiceButton).toBeVisible();
        await addServiceButton.click();

        // Verify a new service card appeared
        await expect(existingServiceCards).toHaveCount(initialCount + 1);

        // Get the newly created service card (last one)
        const serviceCard = page.locator('[class*="ServiceContainer"]').last();
        await expect(serviceCard).toBeVisible();

        // Expand the service card to access edit fields (click on the card)
        await serviceCard.click();

        // Fill in the form fields that are now visible
        // Service Name
        const serviceNameInput = serviceCard.getByPlaceholder(/service name/i).or(serviceCard.getByRole('textbox').first());
        await serviceNameInput.fill('My Test Service');

        // Service Type dropdown - select Anthropic
        const serviceTypeDropdown = serviceCard.getByRole('combobox', { name: 'Service type', exact: true });
        await chooseCompassOption(serviceTypeDropdown, 'Anthropic');

        // API Key
        const apiKeyInput = serviceCard.getByPlaceholder(/api key/i);
        await apiKeyInput.fill('test-api-key-123');

        // Default Model
        const defaultModelInput = serviceCard.getByPlaceholder(/model|default model/i);
        await defaultModelInput.fill('claude-3-opus');

        // Click main Save button at bottom of page
        const saveButton = systemConsole.getSaveButton();
        await expect(saveButton).toBeVisible();
        await Promise.all([
            page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().includes('/admin/config')),
            saveButton.click(),
        ]);

        // Verify service was saved - reload and check
        await page.reload();

        // Verify service appears with configured values
        const servicesSection = page.locator('[class*="ServicesList"]');
        await expect(servicesSection.getByText('My Test Service')).toBeVisible();
    });
});
