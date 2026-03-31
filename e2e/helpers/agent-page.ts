import { Page, Locator, expect } from '@playwright/test';

/**
 * AgentPageHelper — Page object for the agent listing page and config modal.
 * The listing page is a full-page overlay at /plug/mattermost-ai/agents.
 */
export class AgentPageHelper {
    readonly page: Page;

    constructor(page: Page) {
        this.page = page;
    }

    // --- Navigation ---

    /** Navigate to the agents listing page */
    async navigateToAgents(baseUrl: string): Promise<void> {
        await this.page.goto(`${baseUrl}/plug/mattermost-ai/agents`);
        await this.page.waitForLoadState('domcontentloaded');
        // Wait for the create button to be visible (indicates page is loaded)
        await this.getCreateButton().waitFor({ state: 'visible', timeout: 15000 });
    }

    // --- Listing Page Locators ---

    getCreateButton(): Locator {
        return this.page.getByText('Create agent');
    }

    getSearchInput(): Locator {
        return this.page.getByPlaceholder('Search agents...');
    }

    getAllAgentsTab(): Locator {
        return this.page.getByText('All agents');
    }

    getYourAgentsTab(): Locator {
        return this.page.getByText('Your agents');
    }

    getAgentRowByName(displayName: string): Locator {
        // Agent rows contain the display name text; scope narrowly
        return this.page.locator(`text=${displayName}`).first();
    }

    // --- Agent Row Actions ---

    async clickAgentRow(displayName: string): Promise<void> {
        await this.getAgentRowByName(displayName).click();
    }

    async openAgentActions(displayName: string): Promise<void> {
        // The ... menu button has aria-label="Agent actions"
        // Find the row containing the display name, then locate the actions button nearby
        const row = this.page.locator(`text=${displayName}`).locator('..');
        // Walk up to find the row container and then the actions button
        const actionsBtn = row.locator('[aria-label="Agent actions"]')
            .or(row.locator('..').locator('[aria-label="Agent actions"]'))
            .or(row.locator('../..').locator('[aria-label="Agent actions"]'));
        await actionsBtn.first().click();
    }

    async clickEditAction(): Promise<void> {
        await this.page.getByText('Edit', { exact: true }).click();
    }

    async clickDeleteAction(): Promise<void> {
        await this.page.getByText('Delete', { exact: true }).click();
    }

    // --- Config Modal Locators ---

    getModal(): Locator {
        // Modal titles are 'New Agent' (create) or the agent display name (edit)
        // Look for the modal overlay container
        return this.page.locator('[class*="ModalOverlay"]')
            .or(this.page.locator('[class*="modal-content"]'));
    }

    getModalTab(tabName: 'Configuration' | 'Access' | 'MCPs'): Locator {
        return this.page.getByText(tabName, { exact: true });
    }

    getModalSaveButton(): Locator {
        return this.page.getByRole('button', { name: /^Save$|^Create$|^Saving/i });
    }

    getModalCancelButton(): Locator {
        return this.page.getByRole('button', { name: 'Cancel' });
    }

    // --- Configuration Tab Fields ---

    getDisplayNameInput(): Locator {
        return this.page.getByPlaceholder('e.g. Sales Assistant');
    }

    getUsernameInput(): Locator {
        return this.page.getByPlaceholder('Agent username');
    }

    getServiceSelect(): Locator {
        return this.page.locator('select').first();
    }

    getCustomInstructionsInput(): Locator {
        return this.page.getByPlaceholder('How would you like the agent to respond?');
    }

    // --- Delete Dialog ---

    getDeleteDialog(): Locator {
        return this.page.getByText('Are you sure you want to delete').locator('..');
    }

    getDeleteConfirmButton(): Locator {
        // The delete confirm button is inside the delete dialog
        // There are multiple "Delete" texts on screen; use the dialog-scoped one
        return this.page.getByRole('button', { name: 'Delete' }).last();
    }

    // --- MCPs Tab ---

    getMCPSearchInput(): Locator {
        return this.page.getByPlaceholder('Search servers and tools...');
    }

    getToolToggles(): Locator {
        // Tool toggles are custom button elements styled as switches
        return this.page.locator('button[class*="Toggle"]');
    }

    // --- Convenience Methods ---

    /** Fill the Configuration tab for a new agent */
    async fillConfigTab(opts: {
        displayName: string;
        username: string;
        serviceLabel?: string;
        instructions?: string;
    }): Promise<void> {
        await this.getDisplayNameInput().fill(opts.displayName);
        await this.getUsernameInput().fill(opts.username);
        if (opts.serviceLabel) {
            await this.getServiceSelect().selectOption({ label: opts.serviceLabel });
        }
        if (opts.instructions) {
            await this.getCustomInstructionsInput().fill(opts.instructions);
        }
    }

    /** Wait for the config modal to appear */
    async waitForModal(): Promise<void> {
        // Wait for either "New Agent" title or "Configuration" tab to be visible
        await this.page.getByText('Configuration').first().waitFor({ state: 'visible', timeout: 10000 });
    }

    /** Wait for the modal to disappear (after save/cancel) */
    async waitForModalClosed(): Promise<void> {
        // Wait for the display name input to disappear (reliable signal)
        await this.getDisplayNameInput().waitFor({ state: 'hidden', timeout: 10000 });
    }
}
