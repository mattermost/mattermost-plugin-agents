// spec: Service Management - Add First Service Through UI
// seed: e2e/tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Service Management
 *
 * Tests UI-based service management operations in the system console.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Service Management', () => {
    test.beforeAll(async () => {
        // Start container with NO pre-configured services
        mattermost = await RunSystemConsoleContainer({
            services: [],
            bots: [],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should add and configure service through card-based UI', async ({ page }) => {
        // Login + container startup after a long shard can exceed 60s on CI.
        test.setTimeout(120000);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // Login as admin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword, {
            channelViewTimeoutMs: 90000,
        });

        // Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // Count existing service cards before adding
        const existingServiceCards = page.locator('[class*="ServiceContainer"]');
        const initialCount = await existingServiceCards.count();

        // Click "Add an AI Service" button - this creates a new collapsed service card
        const addServiceButton = systemConsole.getAddServiceButton();
        await expect(addServiceButton).toBeVisible();
        await addServiceButton.click();
        await page.waitForTimeout(1000);

        // Verify a new service card appeared
        await expect(existingServiceCards).toHaveCount(initialCount + 1);

        // Get the newly created service card (last one)
        const serviceCard = page.locator('[class*="ServiceContainer"]').last();
        await expect(serviceCard).toBeVisible();

        // Expand the service card to access edit fields (click on the card)
        await serviceCard.click();
        await page.waitForTimeout(500);

        // Fill in the form fields that are now visible
        // Service Name
        const serviceNameInput = serviceCard.getByPlaceholder(/service name/i).or(serviceCard.getByRole('textbox').first());
        await serviceNameInput.fill('My Test Service');

        // Service Type dropdown - select Anthropic
        const serviceTypeDropdown = serviceCard.getByRole('combobox').first();
        await serviceTypeDropdown.selectOption('anthropic');
        await page.waitForTimeout(500);

        // API Key
        const apiKeyInput = serviceCard.getByPlaceholder(/api key/i);
        await apiKeyInput.fill('test-api-key-123');

        // Default Model
        const defaultModelInput = serviceCard.getByPlaceholder(/model|default model/i);
        await defaultModelInput.fill('claude-3-opus');

        // Click main Save button at bottom of page
        const saveButton = systemConsole.getSaveButton();
        await expect(saveButton).toBeVisible();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(2000);

        // Verify service was saved - reload and check
        await page.reload();
        await page.waitForTimeout(1000);

        // Verify service appears with configured values
        const servicesSection = page.locator('[class*="ServicesList"]');
        await expect(servicesSection.getByText('My Test Service')).toBeVisible();
    });

    test('should show dynamically discovered providers and persist advanced bifrost config', async ({ page }) => {
        test.setTimeout(60000);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToPluginConfig(mattermost.url());

        const existingServiceCards = page.locator('[class*="ServiceContainer"]');
        const initialCount = await existingServiceCards.count();

        await systemConsole.clickAddService();
        await expect(existingServiceCards).toHaveCount(initialCount + 1);

        const serviceCard = page.locator('[class*="ServiceContainer"]').last();
        await expect(serviceCard).toBeVisible();
        await serviceCard.click();
        await page.waitForTimeout(500);

        await serviceCard.getByPlaceholder(/service name/i).fill('Vertex Advanced Service');

        const serviceTypeDropdown = serviceCard.getByRole('combobox').first();
        await expect(serviceTypeDropdown.locator('option[value="vertex"]')).toHaveCount(1);
        await serviceTypeDropdown.selectOption('vertex');

        const advancedKeyJSON = '{"vertex_key_config":{"project_id":"project-123","project_number":"123456789","region":"us-central1","auth_credentials":"{}"}}';
        const advancedProviderConfigJSON = '{"network_config":{"base_url":"https://vertex.example.com","default_request_timeout_in_seconds":180}}';

        await serviceCard.getByPlaceholder(/advanced bifrost key json/i).fill(advancedKeyJSON);
        await serviceCard.getByPlaceholder(/advanced bifrost provider config json/i).fill(advancedProviderConfigJSON);
        await serviceCard.getByPlaceholder(/default model/i).fill('gemini-2.5-pro');

        await systemConsole.clickSave();
        await page.reload();
        await page.waitForTimeout(1000);

        const savedServiceCard = page.locator('[class*="ServiceContainer"]').filter({hasText: 'Vertex Advanced Service'}).first();
        await expect(savedServiceCard).toBeVisible();
        await savedServiceCard.click();
        await page.waitForTimeout(500);

        await expect(savedServiceCard.getByRole('combobox').first()).toHaveValue('vertex');
        await expect(savedServiceCard.getByPlaceholder(/advanced bifrost key json/i)).toHaveValue(advancedKeyJSON);
        await expect(savedServiceCard.getByPlaceholder(/advanced bifrost provider config json/i)).toHaveValue(advancedProviderConfigJSON);
    });
});
