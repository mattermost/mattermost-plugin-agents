import { Page, Locator, expect } from '@playwright/test';
import { Client4 } from '@mattermost/client';
import MattermostContainer from './mmcontainer';

function escapeRegExp(value: string): string {
    return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * ToolConfigUIHelper - Page object for Tools tab in System Console
 */
export class ToolConfigUIHelper {
    readonly page: Page;

    constructor(page: Page) {
        this.page = page;
    }

    /** Navigate to System Console > Agents > Tools tab */
    async navigateToToolsTab(baseUrl: string): Promise<void> {
        await this.page.goto(`${baseUrl}/admin_console/plugins/plugin_mattermost-ai`);
        await this.page.waitForLoadState('domcontentloaded');

        // Handle mobile "View in Browser" if present
        const viewBtn = this.page.getByRole('button', { name: /view in browser/i });
        if (await viewBtn.isVisible().catch(() => false)) {
            await viewBtn.click();
            await this.page.waitForLoadState('domcontentloaded');
        }

        // Wait for plugin UI to render
        await this.page.waitForSelector('text=To report a bug or to provide feedback', { timeout: 15000 });

        // Click Tools tab
        const toolsTab = this.page.getByRole('button', { name: 'Tools' });
        await toolsTab.click();

        // Wait for the tools content to load
        await this.page.waitForSelector('text=MCP Tools Configuration', { timeout: 15000 });
    }

    /** Navigate to System Console plugin config page (Configuration tab) */
    async navigateToPluginConfig(baseUrl: string): Promise<void> {
        await this.page.goto(`${baseUrl}/admin_console/plugins/plugin_mattermost-ai`);
        await this.page.waitForLoadState('domcontentloaded');

        // Handle mobile "View in Browser" if present
        const viewBtn = this.page.getByRole('button', { name: /view in browser/i });
        if (await viewBtn.isVisible().catch(() => false)) {
            await viewBtn.click();
            await this.page.waitForLoadState('domcontentloaded');
        }

        // Wait for plugin UI to render
        await this.page.waitForSelector('text=To report a bug or to provide feedback', { timeout: 15000 });
    }

    /** Get all tab buttons visible in the plugin config */
    getTabButtons(): Locator {
        // The tab buttons are rendered by the TabButton styled component
        // They are direct children of TabsContainer, which is a div with flex layout
        // Use role-based selectors for stability
        return this.page.locator('button').filter({ hasText: /^(Configuration|Tools)$/ });
    }

    /** Get a specific tab by name */
    getTab(name: string): Locator {
        return this.page.getByRole('button', { name, exact: true });
    }

    /** Expand a server row by clicking on it to show its tools */
    async expandServer(serverName: string): Promise<void> {
        // The server row header is clickable to expand
        const serverRow = this.page.locator('div').filter({ hasText: new RegExp(escapeRegExp(serverName)) }).filter({ hasText: /tools? enabled/ });
        await serverRow.first().click();

        // Wait for the tool rows to appear
        await this.page.waitForTimeout(500);
    }

    /** Get the tool count text for a server (e.g. "8/8 tools enabled") */
    getServerToolCount(serverName: string): Locator {
        return this.page.locator('div')
            .filter({ hasText: new RegExp(escapeRegExp(serverName)) })
            .getByText(/\d+\/\d+ tools? enabled/)
            .first();
    }

    /** Get all tool name elements visible in the expanded tools list */
    getToolNames(): Locator {
        return this.page.locator('div').filter({ has: this.page.locator('select') }).locator('div').filter({ hasText: /^[A-Za-z_][A-Za-z0-9_]*$/ });
    }

    /** Get the policy dropdown (select element) for a specific tool */
    getToolPolicyDropdown(toolName: string): Locator {
        const toolRow = this.page.locator('div')
            .filter({ has: this.page.getByText(toolName, { exact: true }) })
            .filter({ has: this.page.locator('select') })
            .last();
        return toolRow.locator('select').first();
    }

    /** Set tool policy via dropdown */
    async setToolPolicy(toolName: string, policy: 'Auto Run' | 'Ask Every Time'): Promise<void> {
        const dropdown = this.getToolPolicyDropdown(toolName);
        await dropdown.selectOption({ label: policy });
    }

    /** Get current tool policy value from dropdown */
    async getToolPolicyValue(toolName: string): Promise<string> {
        const dropdown = this.getToolPolicyDropdown(toolName);
        return await dropdown.inputValue();
    }

    /** Get the enable/disable toggle (checkbox input) for a tool */
    getToolToggle(toolName: string): Locator {
        const toolRow = this.page.locator('div')
            .filter({ has: this.page.getByText(toolName, { exact: true }) })
            .filter({ has: this.page.locator('select') })
            .last();
        return toolRow.locator('input[type="checkbox"]').first();
    }

    /** Toggle a tool on or off */
    async toggleTool(toolName: string, enabled: boolean): Promise<void> {
        const toggle = this.getToolToggle(toolName);
        const isCurrentlyChecked = await toggle.isChecked();
        if (isCurrentlyChecked !== enabled) {
            await toggle.evaluate((el) => (el as HTMLInputElement).click());
        }
    }

    /** Check if a tool toggle is currently checked */
    async isToolEnabled(toolName: string): Promise<boolean> {
        const toggle = this.getToolToggle(toolName);
        return await toggle.isChecked();
    }

    /** Get the Refresh Tools button */
    getRefreshButton(): Locator {
        return this.page.getByRole('button', { name: /refresh tools/i });
    }

    /** Get the Clear Cache button */
    getClearCacheButton(): Locator {
        return this.page.getByRole('button', { name: /clear cache/i });
    }

    /** Get the Save button (page-level) */
    getSaveButton(): Locator {
        return this.page.getByRole('button', { name: /save/i });
    }

    /** Click save and wait */
    async clickSave(): Promise<void> {
        await this.getSaveButton().click();
        await this.page.waitForTimeout(1000);
    }
}

/**
 * ToolConfigAPIHelper - Programmatic config read/write for tool configs
 */
export class ToolConfigAPIHelper {
    private client: Client4;
    private pluginId = 'mattermost-ai';

    constructor(client: Client4) {
        this.client = client;
    }

    /** Get current plugin config */
    async getPluginConfig(): Promise<any> {
        const systemConfig = await this.client.getConfig();
        return systemConfig.PluginSettings?.Plugins?.[this.pluginId] || {};
    }

    /** Get MCP config from plugin settings */
    async getMCPConfig(): Promise<any> {
        const pluginConfig = await this.getPluginConfig();
        return pluginConfig?.config?.mcp || {};
    }

    /** Update MCP server tool configs by server index */
    async setServerToolConfigs(
        serverIndex: number,
        toolConfigs: Array<{ name: string; policy: string; enabled: boolean }>,
    ): Promise<void> {
        const pluginConfig = await this.getPluginConfig();
        if (!pluginConfig.config?.mcp?.servers?.[serverIndex]) {
            throw new Error(`Server at index ${serverIndex} not found`);
        }
        pluginConfig.config.mcp.servers[serverIndex].tool_configs = toolConfigs;

        await this.client.patchConfig({
            PluginSettings: {
                Plugins: {
                    [this.pluginId]: pluginConfig,
                },
            },
        });
        await new Promise((resolve) => setTimeout(resolve, 500));
    }

    /** Replace embedded MCP server tool configs (full list). */
    async setEmbeddedServerToolConfigs(
        toolConfigs: Array<{ name: string; policy: string; enabled: boolean }>,
    ): Promise<void> {
        const pluginConfig = await this.getPluginConfig();
        if (!pluginConfig.config?.mcp) {
            throw new Error('MCP config missing');
        }
        pluginConfig.config.mcp.embeddedServer = {
            ...(pluginConfig.config.mcp.embeddedServer || {}),
            enabled: true,
            tool_configs: toolConfigs,
        };

        await this.client.patchConfig({
            PluginSettings: {
                Plugins: {
                    [this.pluginId]: pluginConfig,
                },
            },
        });
        await new Promise((resolve) => setTimeout(resolve, 500));
    }

    /** Get tool configs for a specific server */
    async getServerToolConfigs(
        serverIndex: number,
    ): Promise<Array<{ name: string; policy: string; enabled: boolean }>> {
        const mcpConfig = await this.getMCPConfig();
        return mcpConfig.servers?.[serverIndex]?.tool_configs || [];
    }

    /** Call the user-facing GET /mcp/tools endpoint */
    async getUserMCPTools(baseUrl: string, authToken: string): Promise<any> {
        const response = await fetch(
            `${baseUrl}/plugins/mattermost-ai/mcp/tools`,
            {
                headers: {
                    Authorization: `Bearer ${authToken}`,
                },
            },
        );
        if (!response.ok) {
            throw new Error(`getUserMCPTools failed: ${response.status} ${response.statusText}`);
        }
        return response.json();
    }

    /** Call GET /mcp/user-preferences */
    async getUserPreferences(baseUrl: string, authToken: string): Promise<any> {
        const response = await fetch(
            `${baseUrl}/plugins/mattermost-ai/mcp/user-preferences`,
            {
                headers: {
                    Authorization: `Bearer ${authToken}`,
                },
            },
        );
        if (!response.ok) {
            throw new Error(`getUserPreferences failed: ${response.status} ${response.statusText}`);
        }
        return response.json();
    }

    /** Call PUT /mcp/user-preferences */
    async setUserPreferences(
        baseUrl: string,
        authToken: string,
        prefs: any,
    ): Promise<any> {
        const response = await fetch(
            `${baseUrl}/plugins/mattermost-ai/mcp/user-preferences`,
            {
                method: 'PUT',
                headers: {
                    Authorization: `Bearer ${authToken}`,
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify(prefs),
            },
        );
        if (!response.ok) {
            throw new Error(`setUserPreferences failed: ${response.status} ${response.statusText}`);
        }
        return response.json();
    }
}

/** Factory to create API helper from container */
export async function createToolConfigAPIHelper(
    mattermost: MattermostContainer,
): Promise<ToolConfigAPIHelper> {
    const adminClient = await mattermost.getAdminClient();
    return new ToolConfigAPIHelper(adminClient);
}
