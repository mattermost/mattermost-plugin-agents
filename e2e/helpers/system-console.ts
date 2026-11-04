import { Page, Locator, expect } from '@playwright/test';
import { MATTERMOST_AI_PLUGIN_ID } from './plugin-http';

/** A section of the Agents product's Settings view; also its URL segment. */
export type SettingsSection = 'general' | 'services' | 'mcp' | 'search' | 'web-search' | 'debug';

const SECTION_TABS: Record<SettingsSection, string> = {
    general: 'General',
    services: 'AI services',
    mcp: 'MCP servers',
    search: 'Embedding search',
    'web-search': 'Web search',
    debug: 'Debug',
};

/**
 * SystemConsoleHelper - Page object for the AI plugin settings: the Settings
 * view of the Agents product, where the configuration lives, and the plugin's
 * System Console page, which only lists who else may view or change it.
 */
export class SystemConsoleHelper {
    readonly page: Page;

    constructor(page: Page) {
        this.page = page;
    }

    private async polyfillRandomUUID(): Promise<void> {
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
    }

    private async dismissViewInBrowser(): Promise<void> {
        // Handle "View in Browser" button if it appears (mobile preview page)
        const viewInBrowserButton = this.page.getByRole('button', { name: /view in browser/i });
        const isVisible = await viewInBrowserButton.isVisible().catch(() => false);
        if (isVisible) {
            await viewInBrowserButton.click();
            await this.page.waitForLoadState('domcontentloaded');
        }
    }

    /**
     * Open a section of the Agents settings.
     * @param baseUrl - Mattermost base URL
     */
    async navigateToAgentsSettings(baseUrl: string, section: SettingsSection = 'general'): Promise<void> {
        await this.polyfillRandomUUID();
        await this.page.goto(`${baseUrl}/plug/${MATTERMOST_AI_PLUGIN_ID}/agents/settings/${section}`);
        await this.page.waitForLoadState('domcontentloaded');
        await this.dismissViewInBrowser();

        // The beta message is always present and indicates the React components have loaded
        await this.page.waitForSelector('text=To report a bug or to provide feedback', { timeout: 15000 });
    }

    /** Switch the Agents settings to another section through its tab. */
    async openSection(section: SettingsSection): Promise<void> {
        await this.getSectionTab(section).click();
        await expect(this.getSectionTab(section)).toHaveAttribute('aria-selected', 'true');
    }

    getSectionTab(section: SettingsSection): Locator {
        return this.page.getByRole('tab', { name: SECTION_TABS[section], exact: true });
    }

    /** Open the plugin's System Console page (who may view or change the Agents settings). */
    async navigateToSystemConsole(baseUrl: string): Promise<void> {
        await this.polyfillRandomUUID();
        await this.page.goto(`${baseUrl}/admin_console/plugins/plugin_${MATTERMOST_AI_PLUGIN_ID}`);
        await this.page.waitForLoadState('domcontentloaded');
        await this.dismissViewInBrowser();
        await this.page.getByText('Agents settings have moved').waitFor({ state: 'visible', timeout: 15000 });
    }

    /**
     * Get the "Add Service" button on the no services page
     */
    getAddServiceButton(): Locator {
        return this.page.getByRole('button', { name: /add.*ai.*service/i });
    }

    /**
     * Get the Save button. It shows once there is something to save.
     */
    getSaveButton(): Locator {
        return this.page.getByRole('button', { name: 'Save', exact: true });
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
     * Get AI Services panel
     */
    getServicesPanel(): Locator {
        return this.page.getByText('AI Services').first();
    }

    /**
     * Get AI Functions panel
     */
    getFunctionsPanel(): Locator {
        return this.page.getByText('AI Functions').first();
    }

    /**
     * Get the Debug section's panel (not its tab)
     */
    getDebugPanel(): Locator {
        return this.page.getByText('Trace Output').first();
    }

    /**
     * Click add service button
     */
    async clickAddService(): Promise<void> {
        await this.getAddServiceButton().click();
    }

    /**
     * Click save button
     */
    async clickSave(): Promise<void> {
        await Promise.all([
            this.page.waitForResponse((response) => (
                response.request().method() === 'PUT' &&
                response.url().includes(`/plugins/${MATTERMOST_AI_PLUGIN_ID}/admin/config`)
            )),
            this.getSaveButton().click(),
        ]);
    }
}
