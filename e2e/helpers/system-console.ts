import { Page, Locator, expect } from '@playwright/test';

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
        await this.page.waitForLoadState('domcontentloaded');

        // Handle "View in Browser" button if it appears (mobile preview page)
        const viewInBrowserButton = this.page.getByRole('button', { name: /view in browser/i });
        const isVisible = await viewInBrowserButton.isVisible().catch(() => false);
        if (isVisible) {
            await viewInBrowserButton.click();
            await this.page.waitForLoadState('domcontentloaded');
        }

        // Wait for the plugin configuration UI to fully render
        // The beta message is always present and indicates the React components have loaded
        await this.page.waitForSelector('text=To report a bug or to provide feedback', { timeout: 15000 });
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
        return this.page.getByRole('button', { name: /add.*ai.*(agent|bot)/i });
    }

    /**
     * Wait for the AI Agents panel to be fully loaded
     * This ensures the bots list or "no bots" message is visible
     */
    async waitForBotsPanel(): Promise<void> {
        // Wait for either the bots list or the "no bots" message to appear
        // This indicates the panel has finished loading its content
        const botsPanel = this.getBotsPanel();
        await botsPanel.waitFor({ state: 'visible', timeout: 15000 });

        // Wait for either bot containers OR the add bot button to appear
        // This ensures the panel content has rendered
        const botContainers = this.page.locator('[class*="BotContainer"]');
        const addBotButton = this.getAddBotButton();

        // Wait for one of these to be visible (either existing bots or add button)
        await expect.poll(async () => {
            const hasBot = await botContainers.first().isVisible().catch(() => false);
            if (hasBot) {
                return true;
            }

            return addBotButton.isVisible().catch(() => false);
        }, { timeout: 15000 }).toBe(true);
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
     * Get the no agents message
     */
    getNoBotsMessage(): Locator {
        return this.page.locator('text=/no ai agents/i');
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
        return this.page.getByText(/AI (Bots|Agents)/i).first();
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
