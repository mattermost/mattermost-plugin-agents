import { expect } from '@playwright/test';
import { containerTest } from 'helpers/container-fixture';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest } from 'helpers/openai-mock';

const username = 'regularuser';
const password = 'regularuser';

const test = containerTest(async (own) => {
    const mattermost = own(await RunContainer());
    const openAIMock = own(await RunOpenAIMocks(mattermost.network));
    return { mattermost, openAIMock };
});

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.beforeAll(async ({ containers }) => {
    ({ mattermost, openAIMock } = containers);
});

async function setupTestPage(page) {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);
    const url = mattermost.url();

    await mmPage.login(url, username, password);

    return { mmPage, aiPlugin };
}

/**
 * Helper: create a custom prompt via the plugin REST API.
 * Returns the created prompt object (with `id`, etc.).
 */
async function createPromptViaAPI(
    client: any,
    data: { name: string; description: string; template: string; is_shared: boolean },
) {
    const baseUrl = mattermost.url();
    const response = await fetch(`${baseUrl}/plugins/mattermost-ai/custom-prompts`, {
        method: 'POST',
        headers: {
            Authorization: `Bearer ${client.getToken()}`,
            'Content-Type': 'application/json',
        },
        body: JSON.stringify(data),
    });
    if (!response.ok) {
        throw new Error(`createPromptViaAPI failed: ${response.status} ${response.statusText}`);
    }
    return response.json();
}

/**
 * Helper: delete a custom prompt via the plugin REST API.
 */
async function deletePromptViaAPI(client: any, promptId: string) {
    const baseUrl = mattermost.url();
    const response = await fetch(`${baseUrl}/plugins/mattermost-ai/custom-prompts/${promptId}`, {
        method: 'DELETE',
        headers: {
            Authorization: `Bearer ${client.getToken()}`,
        },
    });
    if (!response.ok) {
        throw new Error(`deletePromptViaAPI failed: ${response.status} ${response.statusText}`);
    }
}

/**
 * Helper: pin or unpin a prompt via the plugin REST API.
 */
async function setPinViaAPI(client: any, promptId: string, pinned: boolean) {
    const baseUrl = mattermost.url();
    const response = await fetch(`${baseUrl}/plugins/mattermost-ai/custom-prompts/pins`, {
        method: 'PUT',
        headers: {
            Authorization: `Bearer ${client.getToken()}`,
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({ prompt_id: promptId, pinned }),
    });
    if (!response.ok) {
        throw new Error(`setPinViaAPI failed: ${response.status} ${response.statusText}`);
    }
}

/**
 * Helper: list all custom prompts via the plugin REST API.
 */
async function listPromptsViaAPI(client: any) {
    const baseUrl = mattermost.url();
    const response = await fetch(`${baseUrl}/plugins/mattermost-ai/custom-prompts`, {
        method: 'GET',
        headers: {
            Authorization: `Bearer ${client.getToken()}`,
        },
    });
    if (!response.ok) {
        throw new Error(`listPromptsViaAPI failed: ${response.status} ${response.statusText}`);
    }
    return response.json();
}

/**
 * Helper: get pinned prompt IDs via the plugin REST API.
 */
async function getPinsViaAPI(client: any): Promise<string[]> {
    const baseUrl = mattermost.url();
    const response = await fetch(`${baseUrl}/plugins/mattermost-ai/custom-prompts/pins`, {
        method: 'GET',
        headers: {
            Authorization: `Bearer ${client.getToken()}`,
        },
    });
    if (!response.ok) {
        throw new Error(`getPinsViaAPI failed: ${response.status} ${response.statusText}`);
    }
    return response.json();
}

/**
 * Helper: render a custom prompt via the plugin REST API.
 */
async function renderPromptViaAPI(
    client: any,
    promptId: string,
    channelId = '',
    botId = '',
) {
    const baseUrl = mattermost.url();
    const response = await fetch(`${baseUrl}/plugins/mattermost-ai/custom-prompts/${promptId}/render`, {
        method: 'POST',
        headers: {
            Authorization: `Bearer ${client.getToken()}`,
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({ channel_id: channelId, bot_id: botId }),
    });
    if (!response.ok) {
        throw new Error(`renderPromptViaAPI failed: ${response.status} ${response.statusText}`);
    }
    return response.json();
}

/**
 * Helper: open the Custom Prompts management modal directly. The real
 * formatting-bar menu path is covered by the submenu tests below.
 */
async function openCustomPromptsModal(page) {
    await page.evaluate(() => {
        const store = (window as any).store || (window as any).__store;
        if (!store?.dispatch) {
            throw new Error('Mattermost Redux store is unavailable');
        }
        store.dispatch({ type: 'SHOW_CUSTOM_PROMPTS_MODAL', show: true });
    });
    await expect(page.getByRole('dialog', {name: 'Custom Prompts'})).toBeVisible({ timeout: 10000 });
}

// ---------------------------------------------------------------------------
// 1. Custom Prompts Management Modal
// ---------------------------------------------------------------------------
test.describe('Custom Prompts Management Modal', () => {
    let createdPromptIds: string[] = [];
    let userClient: any;

    test.beforeAll(async () => {
        userClient = await mattermost.getClient(username, password);
    });

    test.afterAll(async () => {
        for (const id of createdPromptIds) {
            try {
                await deletePromptViaAPI(userClient, id);
            } catch {
                // best-effort cleanup
            }
        }
    });

    test('modal displays title, tabs, search, and create button', async ({ page }) => {
        await setupTestPage(page);
        await openCustomPromptsModal(page);

        // Verify modal structure
        await expect(page.getByRole('dialog', {name: 'Custom Prompts'})).toBeVisible();
        await expect(page.getByText('All Prompts')).toBeVisible();
        await expect(page.getByText('Your Prompts')).toBeVisible();
        await expect(page.getByPlaceholder('Search prompts')).toBeVisible();
        await expect(page.getByRole('button', { name: 'Create new' })).toBeVisible();
    });

    test('can create a new prompt through the modal form', async ({ page }) => {
        await setupTestPage(page);
        await openCustomPromptsModal(page);

        // Click "Create new" to show the form
        await page.getByRole('button', { name: 'Create new' }).click();
        await expect(page.getByText('New Prompt')).toBeVisible();

        // Fill the form (default visibility is Private, which is fine for this test)
        await page.getByPlaceholder('Enter a title for your prompt').fill('Modal Test Prompt');
        await page.getByPlaceholder('Enter a brief description').fill('Created via modal');
        await page.getByPlaceholder('Enter the system prompt template').fill('Hello from modal test');

        // Save (use exact match to avoid matching "Saved messages" button in header)
        await page.getByRole('button', { name: 'Save', exact: true }).click();

        // The form should collapse and the prompt should appear in the list
        await expect(page.getByText('Modal Test Prompt')).toBeVisible({ timeout: 10000 });

        // Record the created prompt for cleanup
        const prompts = await listPromptsViaAPI(userClient);
        const created = prompts.find((p: any) => p.name === 'Modal Test Prompt');
        if (created) {
            createdPromptIds.push(created.id);
        }
    });

    test('can edit an existing prompt and verify persistence', async ({ page }) => {
        // Seed a prompt via API
        const prompt = await createPromptViaAPI(userClient, {
            name: 'Editable Prompt',
            description: 'Before edit',
            template: 'Before template',
            is_shared: true,
        });
        createdPromptIds.push(prompt.id);

        await setupTestPage(page);
        await openCustomPromptsModal(page);

        // Open the prompt in the edit view (same shell as New Prompt)
        await page.getByText('Editable Prompt').click();

        // The form should show pre-filled values
        const titleInput = page.getByPlaceholder('Enter a title for your prompt');
        await expect(titleInput).toHaveValue('Editable Prompt');

        // Edit the title
        await titleInput.fill('Edited Prompt Title');
        await page.getByRole('button', { name: 'Save', exact: true }).click();

        // The updated name should appear in the list
        await expect(page.getByText('Edited Prompt Title')).toBeVisible({ timeout: 10000 });
        await expect(page.getByText('Editable Prompt')).not.toBeVisible();

        // Verify persistence: close and reopen the modal
        // Dispatch close action to dismiss the modal
        await page.evaluate(() => {
            const store = (window as any).store || (window as any).__store;
            if (store?.dispatch) {
                store.dispatch({type: 'SHOW_CUSTOM_PROMPTS_MODAL', show: false});
            }
        });
        await expect(page.getByRole('dialog', {name: 'Custom Prompts'})).toBeHidden({ timeout: 10000 });

        // Reopen
        await openCustomPromptsModal(page);
        await expect(page.getByText('Edited Prompt Title')).toBeVisible({ timeout: 10000 });
    });

    test('can search for prompts by name', async ({ page }) => {
        // Seed two prompts with distinct names
        const promptA = await createPromptViaAPI(userClient, {
            name: 'Alpha Search Prompt',
            description: 'First',
            template: 'template alpha',
            is_shared: true,
        });
        createdPromptIds.push(promptA.id);

        const promptB = await createPromptViaAPI(userClient, {
            name: 'Beta Search Prompt',
            description: 'Second',
            template: 'template beta',
            is_shared: true,
        });
        createdPromptIds.push(promptB.id);

        await setupTestPage(page);
        await openCustomPromptsModal(page);

        // Both should initially be visible
        await expect(page.getByText('Alpha Search Prompt')).toBeVisible();
        await expect(page.getByText('Beta Search Prompt')).toBeVisible();

        // Search for Alpha only
        await page.getByPlaceholder('Search prompts').fill('Alpha');
        await expect(page.getByText('Alpha Search Prompt')).toBeVisible();
        await expect(page.getByText('Beta Search Prompt')).not.toBeVisible();

        // Clear search to restore both
        await page.getByPlaceholder('Search prompts').fill('');
        await expect(page.getByText('Beta Search Prompt')).toBeVisible();
    });

    test('can switch between All Prompts and Your Prompts tabs', async ({ page }) => {
        // Seed a prompt by regularuser
        const myPrompt = await createPromptViaAPI(userClient, {
            name: 'My Own Prompt',
            description: 'Created by regularuser',
            template: 'my template',
            is_shared: true,
        });
        createdPromptIds.push(myPrompt.id);

        // Seed a prompt by admin (a different user)
        const adminClient = await mattermost.getAdminClient();
        const adminPrompt = await createPromptViaAPI(adminClient, {
            name: 'Admin Created Prompt',
            description: 'Created by admin',
            template: 'admin template',
            is_shared: true,
        });

        await setupTestPage(page);
        await openCustomPromptsModal(page);

        // All Prompts tab: both should be visible
        await expect(page.getByText('My Own Prompt')).toBeVisible();
        await expect(page.getByText('Admin Created Prompt')).toBeVisible();

        // Switch to Your Prompts tab
        await page.getByText('Your Prompts').click();

        // Only the current user's prompt should appear
        await expect(page.getByText('My Own Prompt')).toBeVisible();
        await expect(page.getByText('Admin Created Prompt')).not.toBeVisible();

        // Switch back to All Prompts
        await page.getByText('All Prompts').click();
        await expect(page.getByText('Admin Created Prompt')).toBeVisible();

        // Clean up admin prompt
        try {
            await deletePromptViaAPI(adminClient, adminPrompt.id);
        } catch {
            // best-effort
        }
    });

    test('can pin and unpin a prompt via the API and verify state in modal', async ({ page }) => {
        const prompt = await createPromptViaAPI(userClient, {
            name: 'Pinnable Prompt',
            description: 'For pin test',
            template: 'pin template',
            is_shared: true,
        });
        createdPromptIds.push(prompt.id);

        // Pin via API
        await setPinViaAPI(userClient, prompt.id, true);

        // Verify pinned via API
        const pins = await getPinsViaAPI(userClient);
        expect(pins).toContain(prompt.id);

        // Open modal and verify the prompt is visible (pinned state reflected)
        await setupTestPage(page);
        await openCustomPromptsModal(page);
        await expect(page.getByText('Pinnable Prompt')).toBeVisible();

        // Unpin via API
        await setPinViaAPI(userClient, prompt.id, false);

        // Verify unpinned via API
        const pinsAfter = await getPinsViaAPI(userClient);
        expect(pinsAfter).not.toContain(prompt.id);
    });

    test('empty state shows "No prompts found" message', async ({ page }) => {
        await setupTestPage(page);
        await openCustomPromptsModal(page);

        // Search for something that doesn't exist
        await page.getByPlaceholder('Search prompts').fill('xyznonexistent99999');
        await expect(page.getByText('No prompts found')).toBeVisible({ timeout: 5000 });
    });
});

// ---------------------------------------------------------------------------
// 2. Custom Prompts in AI Actions Submenu (formatting bar)
// ---------------------------------------------------------------------------
test.describe('Custom Prompts in AI Actions Submenu', () => {
    let userClient: any;
    let createdPromptIds: string[] = [];

    test.beforeAll(async () => {
        userClient = await mattermost.getClient(username, password);
    });

    test.afterAll(async () => {
        for (const id of createdPromptIds) {
            try {
                await deletePromptViaAPI(userClient, id);
            } catch {
                // best-effort cleanup
            }
        }
    });

    test('prompts appear in the AI actions submenu and insert text on click', async ({ page }) => {
        const prompt = await createPromptViaAPI(userClient, {
            name: 'Formatting Bar Prompt',
            description: 'Appears in AI actions',
            template: 'Inserted via formatting bar',
            is_shared: true,
        });
        createdPromptIds.push(prompt.id);

        await setupTestPage(page);

        const postTextbox = page.getByTestId('post_textbox');
        await postTextbox.click();

        // The AI actions button is only present when the server includes the
        // pluggable AI actions menu (custom mattermost build). Skip on stock images.
        const aiButton = page.locator('#aiActionsMenu');
        if (!await aiButton.isVisible({ timeout: 5000 }).catch(() => false)) {
            test.skip(true, 'AI Actions menu not available (requires custom server build)');
            return;
        }

        await aiButton.click();
        await page.getByText('Custom prompts').hover();

        await expect(page.getByText('Formatting Bar Prompt')).toBeVisible({ timeout: 10000 });
        await page.getByText('Formatting Bar Prompt').click();

        await expect(postTextbox).toHaveValue(/Inserted via formatting bar/, { timeout: 10000 });
    });

    test('"Manage prompts" in the submenu opens the management modal', async ({ page }) => {
        await setupTestPage(page);

        const postTextbox = page.getByTestId('post_textbox');
        await postTextbox.click();

        const aiButton = page.locator('#aiActionsMenu');
        if (!await aiButton.isVisible({ timeout: 5000 }).catch(() => false)) {
            test.skip(true, 'AI Actions menu not available (requires custom server build)');
            return;
        }

        await aiButton.click();
        await page.getByText('Custom prompts').hover();
        await page.getByText('Manage prompts').click();

        await expect(page.getByRole('dialog', {name: 'Custom Prompts'})).toBeVisible({ timeout: 10000 });
        await expect(page.getByText('All Prompts')).toBeVisible();
    });
});

// ---------------------------------------------------------------------------
// 3. Pinned prompts are not shown as pills in the RHS empty state
// ---------------------------------------------------------------------------
test.describe('Pinned prompts in the RHS empty state', () => {
    let userClient: any;
    let createdPromptIds: string[] = [];

    test.beforeAll(async () => {
        userClient = await mattermost.getClient(username, password);
    });

    test.afterAll(async () => {
        for (const id of createdPromptIds) {
            try {
                await setPinViaAPI(userClient, id, false);
            } catch {
                // best-effort
            }
            try {
                await deletePromptViaAPI(userClient, id);
            } catch {
                // best-effort
            }
        }
    });

    test('a pinned prompt does not appear as a pill button in the RHS new chat', async ({ page }) => {
        const prompt = await createPromptViaAPI(userClient, {
            name: 'RHS Pill Prompt',
            description: 'Pinned, but the RHS empty state shows no prompt pills',
            template: 'Pill template content',
            is_shared: true,
        });
        createdPromptIds.push(prompt.id);
        await setPinViaAPI(userClient, prompt.id, true);

        const { aiPlugin } = await setupTestPage(page);
        await aiPlugin.openRHS();

        // The new chat is up (its composer is there) and no pill was rendered for the pinned prompt.
        await expect(aiPlugin.rhsPostTextarea).toBeVisible({ timeout: 30000 });
        await expect(page.getByRole('button', { name: 'RHS Pill Prompt' })).toHaveCount(0);
    });
});

// ---------------------------------------------------------------------------
// 4. Prompt Visibility (Public / Private)
// ---------------------------------------------------------------------------
test.describe('Prompt Visibility', () => {
    let regularClient: any;
    let secondClient: any;
    let createdPromptIds: string[] = [];

    test.beforeAll(async () => {
        regularClient = await mattermost.getClient(username, password);
        secondClient = await mattermost.getClient('seconduser', 'seconduser');
    });

    test.afterAll(async () => {
        for (const id of createdPromptIds) {
            try {
                await deletePromptViaAPI(regularClient, id);
            } catch {
                // best-effort
            }
        }
    });

    test('private prompt is only visible to its creator', async () => {
        const prompt = await createPromptViaAPI(regularClient, {
            name: 'Private Vis Test',
            description: 'Only for me',
            template: 'secret',
            is_shared: false,
        });
        createdPromptIds.push(prompt.id);

        // Creator should see it via API
        const regularPrompts = await listPromptsViaAPI(regularClient);
        expect(regularPrompts.find((p: any) => p.id === prompt.id)).toBeTruthy();

        // Second user should NOT see it via API
        const secondPrompts = await listPromptsViaAPI(secondClient);
        expect(secondPrompts.find((p: any) => p.id === prompt.id)).toBeUndefined();
    });

    test('public prompt is visible to other users', async () => {
        const prompt = await createPromptViaAPI(regularClient, {
            name: 'Public Vis Test',
            description: 'For everyone',
            template: 'public template',
            is_shared: true,
        });
        createdPromptIds.push(prompt.id);

        // Creator should see it
        const regularPrompts = await listPromptsViaAPI(regularClient);
        expect(regularPrompts.find((p: any) => p.id === prompt.id)).toBeTruthy();

        // Second user should also see it
        const secondPrompts = await listPromptsViaAPI(secondClient);
        expect(secondPrompts.find((p: any) => p.id === prompt.id)).toBeTruthy();
    });

    test('another user cannot delete someone else\'s prompt', async () => {
        const prompt = await createPromptViaAPI(regularClient, {
            name: 'Auth Test Prompt',
            description: 'Owned by regularuser',
            template: 'auth template',
            is_shared: true,
        });
        createdPromptIds.push(prompt.id);

        // Second user tries to delete it -- should fail
        const baseUrl = mattermost.url();
        const response = await fetch(`${baseUrl}/plugins/mattermost-ai/custom-prompts/${prompt.id}`, {
            method: 'DELETE',
            headers: { Authorization: `Bearer ${secondClient.getToken()}` },
        });
        expect(response.ok).toBe(false);

        // Verify prompt still exists
        const prompts = await listPromptsViaAPI(regularClient);
        expect(prompts.find((p: any) => p.id === prompt.id)).toBeTruthy();
    });
});

// ---------------------------------------------------------------------------
// 5. Context Variables (template rendering)
// ---------------------------------------------------------------------------
test.describe('Context Variables', () => {
    let userClient: any;
    let createdPromptIds: string[] = [];

    test.beforeAll(async () => {
        userClient = await mattermost.getClient(username, password);
    });

    test.afterAll(async () => {
        for (const id of createdPromptIds) {
            try {
                await setPinViaAPI(userClient, id, false).catch(() => {});
                await deletePromptViaAPI(userClient, id);
            } catch {
                // best-effort
            }
        }
    });

    test('a custom prompt with a context variable is rendered when picked from the RHS composer\'s AI actions', async ({ page }) => {
        const prompt = await createPromptViaAPI(userClient, {
            name: 'Context Pill',
            description: 'Renders username',
            template: 'Greetings {{.Username}}!',
            is_shared: false,
        });
        createdPromptIds.push(prompt.id);
        await setPinViaAPI(userClient, prompt.id, true);

        const { aiPlugin } = await setupTestPage(page);
        await aiPlugin.openRHS();
        await expect(aiPlugin.rhsPostTextarea).toBeEnabled({ timeout: 30000 });

        // The prompts are the host composer's AI action (registerAIActionMenuItemComponent). The
        // AI actions button is only present when the server includes the pluggable AI actions menu.
        const aiButton = aiPlugin.getRhsContainer().locator('#aiActionsMenu');
        if (!await aiButton.isVisible({ timeout: 5000 }).catch(() => false)) {
            test.skip(true, 'AI Actions menu not available (requires custom server build)');
            return;
        }
        await aiButton.click();
        await page.getByText('Custom prompts').hover();
        await page.getByText('Context Pill').click();

        // The template is rendered with the real username, not shown as a template variable.
        await expect(aiPlugin.rhsPostTextarea).toHaveValue('Greetings regularuser!', { timeout: 30000 });

        // Mock the bot response, then send it.
        await openAIMock.addCompletionMock(responseTest);
        await aiPlugin.rhsSendButton.click();
        await expect(aiPlugin.getRhsContainer().getByTestId('post-message-text').getByText('Greetings regularuser!')).toBeVisible({ timeout: 30000 });
    });
});
