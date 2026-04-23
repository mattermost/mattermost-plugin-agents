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

    private escapeRegExp(value: string): string {
        return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }

    private getExactLabel(label: string): Locator {
        return this.page.locator('label').filter({
            hasText: new RegExp(`^${this.escapeRegExp(label)}$`),
        }).first();
    }

    private getLabeledSection(label: string): Locator {
        return this.getExactLabel(label).locator('xpath=following-sibling::*[1]');
    }

    // --- Navigation ---

    /** Navigate to the agents listing page */
    async navigateToAgents(baseUrl: string): Promise<void> {
        await this.page.goto(`${baseUrl}/plug/mattermost-ai/agents`);
        await this.page.waitForLoadState('domcontentloaded');
        // Neutral ready: shell (heading + tabs/search) and agents fetch finished — not only the create button
        // (e.g. users without manage permission might differ in future).
        await this.page.getByRole('heading', { name: 'Agents' }).waitFor({ state: 'visible', timeout: 15000 });
        await this.getSearchInput().waitFor({ state: 'visible', timeout: 15000 });
        await expect(this.page.getByText('Loading agents...')).not.toBeVisible({ timeout: 15000 });
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
        return this.page.getByText(displayName, {exact: true}).first();
    }

    // --- Agent Row Actions ---

    async clickAgentRow(displayName: string): Promise<void> {
        await this.getAgentRowByName(displayName).click();
    }

    async openAgentActions(displayName: string): Promise<void> {
        const rowScope = this.page.getByText(displayName, { exact: true }).locator(
            'xpath=ancestor::div[.//button[@aria-label="Agent actions"]][1]',
        );
        await rowScope.getByRole('button', { name: 'Agent actions' }).click();
    }

    /**
     * Click Edit in the agent row actions menu. Scoped to the row so we do not hit other
     * global "Edit" controls elsewhere in the Mattermost product shell.
     */
    async clickEditAction(displayName: string): Promise<void> {
        const rowScope = this.page.getByText(displayName, { exact: true }).locator(
            'xpath=ancestor::div[.//button[@aria-label="Agent actions"]][1]',
        );
        await rowScope.getByRole('button', { name: 'Edit' }).click();
    }

    /**
     * Click Delete in the agent row actions menu. Scoped to the row so we do not hit other
     * global "Delete" controls elsewhere in the Mattermost product shell.
     */
    async clickDeleteAction(displayName: string): Promise<void> {
        const rowScope = this.page.getByText(displayName, { exact: true }).locator(
            'xpath=ancestor::div[.//button[@aria-label="Agent actions"]][1]',
        );
        await rowScope.getByRole('button', { name: 'Delete' }).click();
    }

    // --- Config Modal Locators ---

    getModal(): Locator {
        // Modal titles are 'New Agent' (create) or the agent display name (edit)
        // Look for the modal overlay container
        return this.page.locator('[class*="ModalOverlay"]')
            .or(this.page.locator('[class*="modal-content"]'));
    }

    getModalTab(tabName: 'Configuration' | 'Access' | 'MCPs'): Locator {
        return this.page.getByRole('button', {name: tabName, exact: true});
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

    getAIServiceSelect(): Locator {
        return this.getExactLabel('AI Service').locator('xpath=following-sibling::*[1]//select[1]');
    }

    getServiceSelect(): Locator {
        return this.getAIServiceSelect();
    }

    getCustomInstructionsInput(): Locator {
        return this.page.getByPlaceholder('How would you like the agent to respond?');
    }

    getBooleanFieldRadios(label: string): Locator {
        return this.getLabeledSection(label).locator('input[type="radio"]');
    }

    async setBooleanField(label: string, value: boolean): Promise<void> {
        await this.getBooleanFieldRadios(label).nth(value ? 0 : 1).click();
    }

    getNativeToolsSection(sectionTitle: 'Native Claude Tools' | 'Native OpenAI Tools'): Locator {
        return this.getLabeledSection(sectionTitle);
    }

    /** Web Search is currently the only native tool in the agent builder. */
    getNativeToolCheckbox(_sectionTitle: 'Native Claude Tools' | 'Native OpenAI Tools'): Locator {
        return this.page.getByTestId('native-tool-web_search');
    }

    getReasoningEnableCheckbox(sectionTitle: 'Reasoning' | 'Extended Thinking'): Locator {
        return this.getLabeledSection(sectionTitle).locator('input[type="checkbox"]').first();
    }

    getReasoningEffortSelect(): Locator {
        return this.getExactLabel('Reasoning Effort').locator('xpath=following-sibling::select[1]');
    }

    getThinkingBudgetInput(): Locator {
        return this.getExactLabel('Thinking Budget (tokens)').locator('xpath=following-sibling::input[1]');
    }

    getStructuredOutputNote(): Locator {
        return this.page.getByText('Extended thinking is turned off while structured output is enabled', {exact: false});
    }

    // --- Delete Dialog ---

    getDeleteDialog(): Locator {
        return this.page.getByRole('dialog', { name: 'Delete agent' });
    }

    getDeleteConfirmButton(): Locator {
        return this.getDeleteDialog().getByRole('button', { name: 'Delete' });
    }

    getDiscardChangesDialog(): Locator {
        return this.page.getByRole('dialog', { name: 'Discard changes?' });
    }

    getDiscardChangesButton(): Locator {
        return this.getDiscardChangesDialog().getByRole('button', { name: 'Discard changes' });
    }

    getDiscardChangesConfirmButton(): Locator {
        return this.getDiscardChangesButton();
    }

    getKeepEditingButton(): Locator {
        return this.getDiscardChangesDialog().getByRole('button', { name: 'Keep editing' });
    }

    getDiscardChangesCancelButton(): Locator {
        return this.getKeepEditingButton();
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

    async clickModalBackdrop(): Promise<void> {
        const modal = this.getModal();
        const box = await modal.boundingBox();
        if (!box) {
            throw new Error('Agent config modal is not visible');
        }

        const clickX = Math.max(5, box.x - 20);
        const clickY = Math.max(5, box.y - 20);
        await this.page.mouse.click(clickX, clickY);
    }
}
