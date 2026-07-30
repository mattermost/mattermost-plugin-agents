import { Page, Locator, expect } from '@playwright/test';

const panelTestIds = {
    'AI Services': 'system-console-ai-services-panel',
    'AI Bots': 'system-console-ai-bots-panel',
    'AI Functions': 'system-console-ai-functions-panel',
    'Debug': 'system-console-debug-panel',
    'Embedding Search': 'system-console-embedding-search-panel',
    'Web Search': 'system-console-web-search-panel',
    'Model Context Protocol (MCP)': 'system-console-mcp-panel',
} as const;

type PanelTitle = keyof typeof panelTestIds;

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

    private escapeRegExp(value: string): string {
        return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }

    private async waitForPluginConfig(): Promise<void> {
        await this.page.getByText('To report a bug or to provide feedback', {exact: false}).waitFor({
            state: 'visible',
            timeout: 15000,
        });
    }

    /**
     * Navigate to the AI plugin system console page
     * @param baseUrl - Mattermost base URL
     */
    async navigateToPluginConfig(baseUrl: string): Promise<void> {
        // Polyfill crypto.randomUUID for insecure contexts (e.g., Docker test environments
        // where the Mattermost URL uses a non-localhost IP like http://172.17.0.1:PORT).
        // crypto.randomUUID requires a secure context but crypto.getRandomValues does not.
        await this.page.addInitScript(() => {
            if (typeof crypto !== 'undefined' && typeof crypto.randomUUID !== 'function') {
                crypto.randomUUID = function randomUUID() {
                    const bytes = new Uint8Array(16);
                    crypto.getRandomValues(bytes);
                    bytes[6] = (bytes[6] & 0x0f) | 0x40;
                    bytes[8] = (bytes[8] & 0x3f) | 0x80;
                    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
                    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}` as `${string}-${string}-${string}-${string}-${string}`;
                };
            }
        });

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
        await this.waitForPluginConfig();
    }

    async reloadPluginConfig(): Promise<void> {
        await this.page.reload({waitUntil: 'domcontentloaded'});
        await this.waitForPluginConfig();
    }

    getPanel(title: PanelTitle): Locator {
        return this.page.getByTestId(panelTestIds[title]);
    }

    private getLabeledControl(label: string, panelTitle: PanelTitle): Locator {
        const exactLabel = new RegExp(`^${this.escapeRegExp(label)}(?:EXPERIMENTAL)?$`);
        return this.getPanel(panelTitle).
            locator('label').
            filter({hasText: exactLabel}).
            locator('+ *');
    }

    getInput(panelTitle: PanelTitle, label: string): Locator {
        return this.getLabeledControl(label, panelTitle).locator('input, textarea');
    }

    getSelect(panelTitle: PanelTitle, label: string): Locator {
        return this.getLabeledControl(label, panelTitle).getByRole('combobox');
    }

    getBooleanRadio(panelTitle: PanelTitle, label: string, value: boolean): Locator {
        return this.getLabeledControl(label, panelTitle).
            locator(`input[type="radio"][value="${value.toString()}"]`);
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
        // AI Bots panel shows a "moved" notice instead of the legacy bot editor
        const botsPanel = this.getBotsPanel();
        await botsPanel.waitFor({ state: 'visible', timeout: 15000 });
        await this.page.getByText(/AI bot configuration has moved/i).waitFor({ state: 'visible', timeout: 15000 });
    }

    /**
     * Get the Save button
     */
    getSaveButton(): Locator {
        return this.page.getByRole('button', {name: /^save$/i});
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
        return this.getPanel('AI Services').getByText('AI Services', {exact: true});
    }

    /**
     * Get AI Bots panel
     */
    getBotsPanel(): Locator {
        return this.getPanel('AI Bots').getByText('AI Bots', {exact: true});
    }

    /**
     * Get AI Functions panel
     */
    getFunctionsPanel(): Locator {
        return this.getPanel('AI Functions').getByText('AI Functions', {exact: true});
    }

    /**
     * Get Debug panel
     */
    getDebugPanel(): Locator {
        return this.getPanel('Debug').getByText('Debug', {exact: true});
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
        const saveResponsePromise = this.page.waitForResponse((response) => {
            return response.request().method() === 'PUT' &&
                new URL(response.url()).pathname.endsWith('/plugins/mattermost-ai/admin/config');
        });

        await this.getSaveButton().click();
        const saveResponse = await saveResponsePromise;
        if (!saveResponse.ok()) {
            throw new Error(`Failed to save plugin configuration: HTTP ${saveResponse.status()}`);
        }
    }
}
