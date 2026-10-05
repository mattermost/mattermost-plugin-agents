import { Page, Locator, expect } from '@playwright/test';

/**
 * AgentPageHelper — Page object for the agent listing page and config view.
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
        await this.getListingHeading().waitFor({ state: 'visible', timeout: 15000 });
        await this.getSearchInput().waitFor({ state: 'visible', timeout: 15000 });
        await expect(this.page.getByText('Loading agents...')).not.toBeVisible({ timeout: 15000 });
    }

    // --- Listing Page Locators ---

    /** Mattermost's global header also renders a screen-reader-only "Agents" h1 once the product loads. */
    getListingHeading(): Locator {
        return this.page.getByRole('heading', { name: 'Agents', exact: true })
            .and(this.page.locator(':not(#global-header *)'));
    }

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
        await rowScope.getByRole('button', { name: 'Edit', exact: true }).click();
    }

    /**
     * Click Delete in the agent row actions menu. Scoped to the row so we do not hit other
     * global "Delete" controls elsewhere in the Mattermost product shell.
     */
    async clickDeleteAction(displayName: string): Promise<void> {
        const rowScope = this.page.getByText(displayName, { exact: true }).locator(
            'xpath=ancestor::div[.//button[@aria-label="Agent actions"]][1]',
        );
        await rowScope.getByRole('button', { name: 'Delete', exact: true }).click();
    }

    /** Click Export in the agent row actions menu (opens the menu first). */
    async clickExportAction(displayName: string): Promise<void> {
        const rowScope = this.page.getByText(displayName, { exact: true }).locator(
            'xpath=ancestor::div[.//button[@aria-label="Agent actions"]][1]',
        );
        await rowScope.getByRole('button', { name: 'Export', exact: true }).click();
    }

    /** Open the editor for an existing agent from its row actions menu. */
    async openAgentEditor(displayName: string): Promise<void> {
        await this.openAgentActions(displayName);
        await this.clickEditAction(displayName);
        await this.waitForModal();
    }

    // --- Config View Locators ---

    getModal(): Locator {
        // Keep the legacy name for existing tests. The agent editor is now a full-page view,
        // but confirmation dialogs still render as dialogs.
        return this.page.locator('.mmAiModal__sheet')
            .or(this.page.locator('[class*="ModalOverlay"]'))
            .or(this.page.locator('[class*="modal-content"]'));
    }

    getBackButton(): Locator {
        return this.page.getByRole('button', {name: 'Back to agents'});
    }

    getModalTab(tabName: 'Configuration' | 'Access' | 'MCPs' | 'History'): Locator {
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
        return this.getExactLabel('Reasoning Effort').locator('xpath=ancestor::div[1]//select[1]');
    }

    getThinkingBudgetInput(): Locator {
        return this.getExactLabel('Thinking Budget (tokens)').locator('xpath=following-sibling::input[1]');
    }

    getAdvancedConfigurationToggle(): Locator {
        return this.page.getByRole('button', {name: /Advanced configuration/i});
    }

    async expandAdvancedConfiguration(): Promise<void> {
        const toggle = this.getAdvancedConfigurationToggle();
        await toggle.waitFor({state: 'visible', timeout: 10000});
        if (await toggle.getAttribute('aria-expanded') !== 'true') {
            await toggle.click();
            await expect(toggle).toHaveAttribute('aria-expanded', 'true');
        }
    }

    // --- Delete Dialog ---

    getDeleteDialog(): Locator {
        return this.page.getByRole('dialog', { name: 'Delete agent' });
    }

    getDeleteConfirmButton(): Locator {
        return this.getDeleteDialog().getByRole('button', { name: 'Delete' });
    }

    /** Unsaved-changes confirmation when leaving the agent config view (MM-68452). */
    getDiscardChangesDialog(): Locator {
        return this.page.getByRole('dialog', { name: 'Discard changes?' });
    }

    getDiscardChangesConfirmButton(): Locator {
        return this.getDiscardChangesDialog().getByRole('button', { name: 'Discard' });
    }

    getDiscardChangesKeepEditingButton(): Locator {
        return this.getDiscardChangesDialog().getByRole('button', { name: 'Keep editing' });
    }

    // --- MCPs Tab ---

    getMCPSearchInput(): Locator {
        return this.page.getByPlaceholder('Search servers and tools...');
    }

    getToolToggles(): Locator {
        // Tool toggles are custom button elements styled as switches
        return this.page.locator('button[class*="Toggle"]');
    }

    // --- Reference documents (Configuration tab) ---

    getDocumentsSection(): Locator {
        return this.page.getByTestId('agent-documents');
    }

    /** Hidden file input behind the "Upload documents" button. */
    getDocumentsFileInput(): Locator {
        return this.page.getByTestId('agent-documents-input');
    }

    /** Rows of the documents in the editor draft, in order. */
    getDocumentRows(): Locator {
        return this.getDocumentsSection().getByRole('list', { name: 'Reference documents', exact: true }).getByRole('listitem');
    }

    getDocumentRow(name: string): Locator {
        return this.getDocumentRows().filter({ has: this.page.getByText(name, { exact: true }) });
    }

    /** Rows of in-flight or failed uploads. */
    getDocumentUploadRow(name: string): Locator {
        return this.getDocumentsSection().getByRole('list', { name: 'Document uploads', exact: true })
            .getByRole('listitem').filter({ has: this.page.getByText(name, { exact: true }) });
    }

    getDocumentsUsage(): Locator {
        return this.page.getByTestId('agent-documents-usage');
    }

    /** The reference documents listed in a History version snapshot. */
    getVersionSnapshotDocuments(): Locator {
        return this.page.getByTestId('version-snapshot-documents');
    }

    // --- Editor header ---

    /** Export button in the editor header (existing agents only). */
    getEditorExportButton(): Locator {
        return this.page.getByRole('button', { name: 'Export', exact: true });
    }

    /** The avatar preview image on the Configuration tab. */
    getAvatarPreview(): Locator {
        return this.getExactLabel('Bot avatar').locator('xpath=following-sibling::*[1]//img[1]');
    }

    /** Hidden file input behind the avatar "Upload Image" button. */
    getAvatarFileInput(): Locator {
        return this.getExactLabel('Bot avatar').locator('xpath=following-sibling::*[1]//input[@type="file"]');
    }

    // --- History Tab ---

    getVersionList(): Locator {
        return this.page.getByTestId('version-list');
    }

    /** All version entries, newest first. */
    getVersionItems(): Locator {
        return this.getVersionList().getByRole('button');
    }

    getVersionItem(version: number): Locator {
        return this.getVersionList().getByRole('button', { name: new RegExp(`^Version ${version}(?!\\d)`) });
    }

    getVersionSnapshot(): Locator {
        return this.page.getByTestId('version-snapshot');
    }

    getVersionSnapshotInstructions(): Locator {
        return this.page.getByTestId('version-snapshot-instructions');
    }

    getRestoreVersionButton(): Locator {
        return this.page.getByRole('button', { name: 'Restore this version' });
    }

    getRestoreDialog(): Locator {
        return this.page.getByRole('dialog', { name: 'Restore this version?' });
    }

    getRestoreConfirmButton(): Locator {
        return this.getRestoreDialog().getByRole('button', { name: 'Restore', exact: true });
    }

    async openHistoryVersion(version: number): Promise<void> {
        await this.getModalTab('History').click();
        await this.getVersionItem(version).click();
        await expect(this.getVersionItem(version)).toHaveAttribute('aria-current', 'true');
        await this.getVersionSnapshot().waitFor({ state: 'visible', timeout: 10000 });
    }

    // --- Import Modal ---

    getImportButton(): Locator {
        return this.page.getByRole('button', { name: 'Import agent' });
    }

    getImportDialog(): Locator {
        return this.page.getByRole('dialog', { name: 'Import agent' });
    }

    getImportSummary(): Locator {
        return this.getImportDialog().getByTestId('import-summary');
    }

    getImportModeRadio(mode: 'create' | 'update'): Locator {
        const label = mode === 'create' ? 'Create a new agent' : 'Update an existing agent';
        return this.getImportDialog().getByRole('radio', { name: label });
    }

    getImportUsernameInput(): Locator {
        return this.getImportDialog().getByLabel('Username', { exact: true });
    }

    getImportDisplayNameInput(): Locator {
        return this.getImportDialog().getByLabel('Display name', { exact: true });
    }

    getImportServiceSelect(): Locator {
        return this.getImportDialog().getByLabel('AI service', { exact: true });
    }

    getImportTargetAgentSelect(): Locator {
        return this.getImportDialog().getByLabel('Agent to update', { exact: true });
    }

    /** The MCP mapping select for a document server, labelled by its source name. */
    getImportMCPMappingSelect(sourceName: string): Locator {
        return this.getImportDialog().getByTestId('import-mcp-mappings').getByLabel(sourceName, { exact: true });
    }

    getImportSubmitButton(): Locator {
        return this.getImportDialog().getByRole('button', { name: /^Import$|^Importing/ });
    }

    async openImportModal(): Promise<void> {
        await this.getImportButton().click();
        await this.getImportDialog().waitFor({ state: 'visible', timeout: 10000 });
    }

    /** Choose an export file in the import modal and wait for the server preview. */
    async chooseImportFile(file: string | { name: string; mimeType: string; buffer: Buffer }): Promise<void> {
        await this.getImportDialog().getByTestId('import-agent-file-input').setInputFiles(file);
        await this.getImportSummary().waitFor({ state: 'visible', timeout: 10000 });
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

    /** Wait for the config view to appear */
    async waitForModal(): Promise<void> {
        await this.getBackButton().waitFor({state: 'visible', timeout: 10000});
        await this.page.getByText('Configuration').first().waitFor({ state: 'visible', timeout: 10000 });
    }

    /** Wait for the config view to disappear (after save/cancel/back) */
    async waitForModalClosed(): Promise<void> {
        // Wait for the display name input to disappear (reliable signal)
        await this.getDisplayNameInput().waitFor({ state: 'hidden', timeout: 10000 });
    }
}
