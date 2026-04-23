import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';
import {
    RunAgentContainer,
    agentAdminUsername, agentAdminPassword,
    agentRegularUsername, agentRegularPassword,
    agentUnprivilegedUsername, agentUnprivilegedPassword,
    mockServiceId,
} from 'helpers/agent-container';
import { AgentAPIHelper } from 'helpers/agent-api';
import { AgentPageHelper } from 'helpers/agent-page';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Agent CRUD', () => {
    test.beforeAll(async () => {
        mattermost = await RunAgentContainer();
        openAIMock = await RunOpenAIMocks(mattermost.network);
        await openAIMock.addCompletionMock(responseTest);
    }, { timeout: 180000 });

    test.afterAll(async () => {
        await openAIMock?.stop();
        await mattermost?.stop();
    });

    test('should create a new agent via UI', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        // Click create button
        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        // Fill Configuration tab
        await agentPage.fillConfigTab({
            displayName: 'My Test Agent',
            username: 'mytestagent',
            serviceLabel: 'Mock Service',
            instructions: 'You are a helpful test agent.',
        });

        // Save
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        // Verify agent appears in listing
        await expect(agentPage.getAgentRowByName('My Test Agent')).toBeVisible({ timeout: 10000 });
    });

    test('should edit an existing agent', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        const agentApi = new AgentAPIHelper(mattermost.url());

        // Create agent via API for test setup
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();
        await agentApi.createTestAgent(token, {
            displayName: 'Edit Me',
            username: 'editmeagent',
            serviceID: mockServiceId,
        });

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        // Open edit via actions menu
        await agentPage.openAgentActions('Edit Me');
        await agentPage.clickEditAction('Edit Me');
        await agentPage.waitForModal();

        // Change display name
        await agentPage.getDisplayNameInput().clear();
        await agentPage.getDisplayNameInput().fill('Edited Agent');

        // Save
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        // Verify updated name in listing
        await expect(agentPage.getAgentRowByName('Edited Agent')).toBeVisible({ timeout: 10000 });
        await expect(agentPage.getAgentRowByName('Edit Me')).not.toBeVisible();
    });

    test('should prompt before closing the agent modal with unsaved changes', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        const agentApi = new AgentAPIHelper(mattermost.url());

        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();
        await agentApi.createTestAgent(token, {
            displayName: 'Unsaved Prompt',
            username: 'unsavedpromptagent',
            serviceID: mockServiceId,
        });

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        await agentPage.openAgentActions('Unsaved Prompt');
        await agentPage.clickEditAction('Unsaved Prompt');
        await agentPage.waitForModal();

        await agentPage.getDisplayNameInput().fill('Unsaved Prompt Changed');
        await agentPage.getModalCancelButton().click();

        await expect(agentPage.getDiscardChangesDialog()).toBeVisible();
        await agentPage.getDiscardChangesKeepEditingButton().click();
        await expect(agentPage.getDiscardChangesDialog()).not.toBeVisible();
        await expect(agentPage.getDisplayNameInput()).toHaveValue('Unsaved Prompt Changed');

        await agentPage.getModalCancelButton().click();
        await expect(agentPage.getDiscardChangesDialog()).toBeVisible();
        await agentPage.getDiscardChangesConfirmButton().click();
        await agentPage.waitForModalClosed();
    });

    test('should delete an agent with confirmation', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        const agentApi = new AgentAPIHelper(mattermost.url());

        // Create agent via API
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();
        await agentApi.createTestAgent(token, {
            displayName: 'Delete Me',
            username: 'deletemeagent',
            serviceID: mockServiceId,
        });

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        // Open actions menu and click delete
        await agentPage.openAgentActions('Delete Me');
        await agentPage.clickDeleteAction('Delete Me');

        // Confirm deletion dialog is visible
        await expect(agentPage.getDeleteDialog()).toBeVisible();
        await agentPage.getDeleteConfirmButton().click();

        // Verify agent removed from listing
        await expect(agentPage.getAgentRowByName('Delete Me')).not.toBeVisible({ timeout: 10000 });
    });

    test('should reject duplicate username with error', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        const agentApi = new AgentAPIHelper(mattermost.url());

        // Create existing agent via API
        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();
        await agentApi.createTestAgent(token, {
            displayName: 'Existing Agent',
            username: 'existingagent',
            serviceID: mockServiceId,
        });

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        // Try to create agent with same username
        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        await agentPage.fillConfigTab({
            displayName: 'Duplicate Agent',
            username: 'existingagent',
            serviceLabel: 'Mock Service',
        });

        await agentPage.getModalSaveButton().click();

        await expect(page.getByText('This username is already taken')).toBeVisible({ timeout: 15000 });
        await expect(agentPage.getDisplayNameInput()).toBeVisible();
    });

    test('should show agent in "Your agents" tab for creator', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        // Create agent via UI
        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();
        await agentPage.fillConfigTab({
            displayName: 'My Personal Agent',
            username: 'mypersonalagent',
            serviceLabel: 'Mock Service',
        });
        await agentPage.getModalSaveButton().click();
        await agentPage.waitForModalClosed();

        // Switch to "Your agents" tab
        await agentPage.getYourAgentsTab().click();

        // Verify agent appears in Your agents tab
        await expect(agentPage.getAgentRowByName('My Personal Agent')).toBeVisible({ timeout: 10000 });
    });

    test('regular user sees UserAccessLevel=All agents in the listing', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);
        const agentApi = new AgentAPIHelper(mattermost.url());
        const suffix = Date.now().toString(36);

        const adminClient = await mattermost.getClient(agentAdminUsername, agentAdminPassword);
        const token = adminClient.getToken();
        await agentApi.createTestAgent(token, {
            displayName: `Visible To Regular ${suffix}`,
            username: `visibletoreg${suffix}`,
            serviceID: mockServiceId,
            userAccessLevel: 0,
        });

        await mmPage.login(mattermost.url(), agentRegularUsername, agentRegularPassword);
        await agentPage.navigateToAgents(mattermost.url());
        await expect(agentPage.getAgentRowByName(`Visible To Regular ${suffix}`)).toBeVisible({ timeout: 10000 });
    });

    test('denies create in UI when user lacks manage_own_agent permission', async ({ page }) => {
        test.setTimeout(60000);
        await mattermost.revokeManageOwnAgentFromSystemUser();
        try {
            const mmPage = new MattermostPage(page);
            const agentPage = new AgentPageHelper(page);

            await mmPage.login(mattermost.url(), agentUnprivilegedUsername, agentUnprivilegedPassword);
            await agentPage.navigateToAgents(mattermost.url());

            await expect(agentPage.getCreateButton()).not.toBeVisible({ timeout: 15000 });
        } finally {
            await mattermost.grantSelfServiceAgentPermissions();
        }
    });

    test('search shows no-results message when nothing matches', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        await agentPage.getSearchInput().fill('zzzznonexistentquery9999');
        await expect(page.getByText('No agents match "zzzznonexistentquery9999"')).toBeVisible({
            timeout: 10000,
        });
    });

    test('disables MCPs tab when Enable Tools is off', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        await agentPage.fillConfigTab({
            displayName: 'MCP Tab Test',
            username: 'mcptabtest',
            serviceLabel: 'Mock Service',
        });

        const enableToolsLabel = page.getByText('Enable Tools').first();
        await expect(enableToolsLabel).toBeVisible({ timeout: 10000 });
        await enableToolsLabel.locator('xpath=following-sibling::*[1]').locator('input[type="radio"]').nth(1).click();

        const mcpsTab = page.getByRole('button', { name: 'MCPs' });
        await expect(mcpsTab).toBeDisabled();
    });

    test('warns before discarding unsaved agent modal changes', async ({ page }) => {
        test.setTimeout(60000);
        const mmPage = new MattermostPage(page);
        const agentPage = new AgentPageHelper(page);

        await mmPage.login(mattermost.url(), agentAdminUsername, agentAdminPassword);
        await agentPage.navigateToAgents(mattermost.url());

        await agentPage.getCreateButton().click();
        await agentPage.waitForModal();

        await agentPage.fillConfigTab({
            displayName: 'Unsaved Agent',
            username: 'unsavedagent',
            serviceLabel: 'Mock Service',
        });

        await agentPage.clickModalBackdrop();
        await expect(agentPage.getDiscardChangesDialog()).toBeVisible({timeout: 10000});

        await agentPage.getDiscardChangesKeepEditingButton().click();
        await expect(agentPage.getDiscardChangesDialog()).not.toBeVisible({timeout: 10000});
        await expect(agentPage.getDisplayNameInput()).toHaveValue('Unsaved Agent');

        await agentPage.clickModalBackdrop();
        await expect(agentPage.getDiscardChangesDialog()).toBeVisible({timeout: 10000});

        await agentPage.getDiscardChangesConfirmButton().click();
        await agentPage.waitForModalClosed();
    });
});
