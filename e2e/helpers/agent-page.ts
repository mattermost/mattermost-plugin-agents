import { Page, Locator, expect } from '@playwright/test';
import { chooseCompassOption } from './compass-select';

/**
 * AgentPageHelper — Page object for the agent listing page and config view.
 * The listing page is a full-page overlay at /plug/mattermost-ai/agents/all.
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

    /** Navigate to the agents listing page (/agents/all; bare /agents now opens a new chat) */
    async navigateToAgents(baseUrl: string): Promise<void> {
        await this.page.goto(`${baseUrl}/plug/mattermost-ai/agents/all`);
        await this.page.waitForLoadState('domcontentloaded');
        // Neutral ready: shell (heading + tabs/search) and agents fetch finished — not only the create button
        // (e.g. users without manage permission might differ in future).
        await this.getListingHeading().waitFor({ state: 'visible', timeout: 15000 });
        await this.getSearchInput().waitFor({ state: 'visible', timeout: 15000 });
        await expect(this.page.getByText('Loading agents...')).not.toBeVisible({ timeout: 15000 });
    }

    // --- Listing Page Locators ---

    /** The listing's own title. The global header and the product sidebar also render an "Agents" heading, so scope to <main>. */
    getListingHeading(): Locator {
        return this.page.getByRole('main').getByRole('heading', { name: 'Agents', exact: true });
    }

    getCreateButton(): Locator {
        return this.page.getByText('Create agent');
    }

    getSearchInput(): Locator {
        return this.page.getByPlaceholder('Search agents...');
    }

    getAllAgentsTab(): Locator {
        return this.page.getByRole('tab', { name: 'All agents' });
    }

    getYourAgentsTab(): Locator {
        return this.page.getByRole('tab', { name: 'Your agents' });
    }

    getAgentRowByName(displayName: string): Locator {
        return this.page.getByRole('main').getByText(displayName, {exact: true}).first();
    }

    // --- Agent Row Actions ---

    /** The row itself: its primary action starts a new chat with the agent (editing lives in the actions menu). */
    getAgentRowButton(displayName: string): Locator {
        return this.page.getByRole('button', { name: `Start a new chat with ${displayName}`, exact: true });
    }

    /**
     * The whole row: the primary action button and its sibling controls (actions menu, badges).
     * Matched by the visible name, not the row button's role: an open actions menu hides the rest of
     * the row from the accessibility tree, which would drop the row from a role-based match.
     */
    getAgentRow(displayName: string): Locator {
        return this.page.getByTestId('agent-row').filter({has: this.page.getByText(displayName, {exact: true})});
    }

    /** Click the row's primary action, which opens a new chat with that agent. */
    async clickAgentRow(displayName: string): Promise<void> {
        await this.getAgentRowButton(displayName).click();
    }

    async openAgentActions(displayName: string): Promise<void> {
        const rowScope = this.getAgentRow(displayName);
        await rowScope.getByRole('button', { name: 'Agent actions' }).click();
    }

    /**
     * Click Edit in the agent row actions menu. Scoped to the row so we do not hit other
     * global "Edit" controls elsewhere in the Mattermost product shell.
     */
    async clickEditAction(displayName: string): Promise<void> {
        const rowScope = this.getAgentRow(displayName);
        await rowScope.getByRole('button', { name: 'Edit', exact: true }).click();
    }

    /**
     * Click Delete in the agent row actions menu. Scoped to the row so we do not hit other
     * global "Delete" controls elsewhere in the Mattermost product shell.
     */
    async clickDeleteAction(displayName: string): Promise<void> {
        const rowScope = this.getAgentRow(displayName);
        await rowScope.getByRole('button', { name: 'Delete', exact: true }).click();
    }

    /** Click Export in the agent row actions menu (opens the menu first). */
    async clickExportAction(displayName: string): Promise<void> {
        const rowScope = this.getAgentRow(displayName);
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

    getModalTab(tabName: 'Configuration' | 'Access' | 'MCPs'): Locator {
        return this.page.getByRole('tab', {name: tabName, exact: true});
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
        return this.page.getByRole('combobox', {name: 'AI Service', exact: true});
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
        return this.page.getByRole('combobox', {name: 'Reasoning Effort', exact: true});
    }

    getThinkingEffortSelect(): Locator {
        return this.page.getByRole('combobox', {name: 'Thinking Effort', exact: true});
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

    getRenameDialog(): Locator {
        return this.page.getByRole('dialog', { name: 'Change agent username?' });
    }

    getRenameConfirmButton(): Locator {
        return this.getRenameDialog().getByRole('button', { name: 'Change username' });
    }

    // --- MCPs Tab ---

    getMCPSearchInput(): Locator {
        return this.page.getByPlaceholder('Search servers and tools...');
    }

    getToolToggles(): Locator {
        return this.page.getByRole('switch');
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

    // --- Version history (header version line + modal) ---

    /** The version line next to the agent title, e.g. "v7 · Dana Lee, 2 days ago". */
    getVersionLine(): Locator {
        return this.page.getByRole('button', { name: /^Version history, v\d+/ });
    }

    /** The update icon button to the left of Import and Export. */
    getVersionHistoryButton(): Locator {
        return this.page.getByRole('button', { name: 'Version history', exact: true });
    }

    getVersionHistoryDialog(): Locator {
        return this.page.getByRole('dialog', { name: 'Version history', exact: true });
    }

    async openVersionHistory(): Promise<void> {
        await this.getVersionLine().click();
        await this.getVersionList().waitFor({ state: 'visible', timeout: 10000 });
    }

    async closeVersionHistory(): Promise<void> {
        await this.getVersionHistoryDialog().getByRole('button', { name: 'Close', exact: true }).click();
        await expect(this.getVersionHistoryDialog()).toBeHidden({ timeout: 10000 });
    }

    getVersionList(): Locator {
        return this.page.getByTestId('version-list');
    }

    /** All version entries, newest first (row menus are not entries). */
    getVersionItems(): Locator {
        return this.getVersionList().getByRole('button', { name: /^Version \d+/ });
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

    /** The field name cell for a setting in the selected version's read-only configuration. */
    getVersionFieldLabel(name: string): Locator {
        return this.getVersionSnapshot().locator('dt').filter({ hasText: name });
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

    /** Opens the history modal (if needed) and selects a version. */
    async openHistoryVersion(version: number): Promise<void> {
        if (!(await this.getVersionList().isVisible())) {
            await this.openVersionHistory();
        }
        await this.getVersionItem(version).click();
        await expect(this.getVersionItem(version)).toHaveAttribute('aria-current', 'true');
        await this.getVersionSnapshot().waitFor({ state: 'visible', timeout: 10000 });
    }

    /** The "..." menu on a previous version's row (the current version has none). */
    getVersionActionsButton(version: number): Locator {
        return this.getVersionList().getByRole('button', { name: `Actions for version ${version}`, exact: true });
    }

    /** Opens a previous version's row menu and picks an item such as "Delete this version". */
    async clickVersionAction(version: number, action: 'Restore this version' | 'Delete this version'): Promise<void> {
        await this.getVersionItem(version).hover();
        await this.getVersionActionsButton(version).click();
        await this.page.getByTestId('dropdownmenu').getByRole('button', { name: action, exact: true }).click();
    }

    /** The "..." menu at the top of the version list. */
    getVersionHistoryMenuButton(): Locator {
        return this.getVersionHistoryDialog().getByRole('button', { name: 'More version history actions', exact: true });
    }

    /** Turns on multi-select, which shows a checkbox on every previous version. */
    async startSelectingVersions(): Promise<void> {
        await this.getVersionHistoryMenuButton().click();
        await this.page.getByTestId('dropdownmenu').getByRole('button', { name: 'Select multiple versions', exact: true }).click();
    }

    /** Checkbox that selects a previous version for deletion. */
    getVersionCheckbox(version: number): Locator {
        return this.getVersionList().getByRole('checkbox', { name: `Select version ${version}`, exact: true });
    }

    getDeleteSelectedVersionsButton(): Locator {
        return this.getVersionHistoryDialog().getByRole('button', { name: 'Delete selected', exact: true });
    }

    async clickDeleteAllPreviousVersions(): Promise<void> {
        await this.getVersionHistoryMenuButton().click();
        await this.page.getByTestId('dropdownmenu').getByRole('button', { name: 'Delete all previous versions', exact: true }).click();
    }

    /** Confirms the open version delete dialog. */
    async confirmVersionDelete(): Promise<void> {
        const dialog = this.page.getByRole('dialog', { name: /^Delete / });
        await dialog.getByRole('button', { name: 'Delete', exact: true }).click();
        await expect(dialog).toBeHidden({ timeout: 10000 });
    }

    // --- Import ---

    /** Import button on the Agents list. */
    getImportButton(): Locator {
        return this.page.getByRole('button', { name: 'Import', exact: true });
    }

    /** Import button in the editor header. */
    getEditorImportButton(): Locator {
        return this.page.getByRole('button', { name: 'Import', exact: true });
    }

    /** Clicks an import button and picks file in the file chooser it opens. */
    async importAgentFile(button: Locator, file: string | { name: string; mimeType: string; buffer: Buffer }): Promise<void> {
        const [chooser] = await Promise.all([
            this.page.waitForEvent('filechooser'),
            button.click(),
        ]);
        await chooser.setFiles(file);
    }

    /** The notice the editor shows once an agent file is loaded into the form. */
    getImportedNotice(): Locator {
        return this.page.getByRole('status').filter({ hasText: 'Settings loaded from the agent file' });
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
            await chooseCompassOption(this.getServiceSelect(), opts.serviceLabel);
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
