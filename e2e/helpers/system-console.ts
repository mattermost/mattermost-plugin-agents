import { Page, Locator } from '@playwright/test';

/**
 * SystemConsoleHelper - Page object for System Console AI Plugin configuration
 *
 * Provides navigation and locators for testing the system console UI
 */
export class SystemConsoleHelper {
    readonly page: Page;

    constructor(page: Page) {
        this.page = page;
    }

    /**
     * Navigate to the AI plugin system console page
     * @param baseUrl - Mattermost base URL
     */
    async navigateToPluginConfig(baseUrl: string): Promise<void> {
        await this.page.goto(`${baseUrl}/admin_console/plugins/plugin_mattermost-ai`);
        await this.page.waitForLoadState('networkidle');

        // Handle "View in Browser" button if it appears (mobile preview page)
        const viewInBrowserButton = this.page.getByRole('button', { name: /view in browser/i });
        const isVisible = await viewInBrowserButton.isVisible().catch(() => false);
        if (isVisible) {
            await viewInBrowserButton.click();
            await this.page.waitForLoadState('networkidle');
        }
    }

    /**
     * Get the "Add Service" button on the no services page
     */
    getAddServiceButton(): Locator {
        return this.page.getByRole('button', { name: /add.*ai.*service/i });
    }

    /**
     * Get the "Add Bot" button on the no bots page
     */
    getAddBotButton(): Locator {
        return this.page.getByRole('button', { name: /add.*ai.*agent/i });
    }

    /**
     * Get the Save button
     */
    getSaveButton(): Locator {
        return this.page.getByRole('button', { name: /save/i });
    }

    /**
     * Get the beta feedback message
     */
    getBetaMessage(): Locator {
        return this.page.locator('text=To report a bug or to provide feedback');
    }

    /**
     * Get the no services message
     */
    getNoServicesMessage(): Locator {
        return this.page.locator('text=/no.*ai.*services/i');
    }

    /**
     * Get the no bots message
     */
    getNoBotsMessage(): Locator {
        return this.page.locator('text=/no.*ai.*bots/i');
    }

    /**
     * Get AI Services panel
     */
    getServicesPanel(): Locator {
        return this.page.getByText('AI Services').first();
    }

    /**
     * Get AI Bots panel
     */
    getBotsPanel(): Locator {
        return this.page.getByText('AI Bots').first();
    }

    /**
     * Get AI Functions panel
     */
    getFunctionsPanel(): Locator {
        return this.page.getByText('AI Functions').first();
    }

    /**
     * Get Debug panel
     */
    getDebugPanel(): Locator {
        return this.page.getByText('Debug').first();
    }

    /**
     * Click add service button
     */
    async clickAddService(): Promise<void> {
        await this.getAddServiceButton().click();
    }

    /**
     * Click add bot button
     */
    async clickAddBot(): Promise<void> {
        await this.getAddBotButton().click();
    }

    /**
     * Click save button
     */
    async clickSave(): Promise<void> {
        await this.getSaveButton().click();
        await this.page.waitForTimeout(1000);
    }
}
