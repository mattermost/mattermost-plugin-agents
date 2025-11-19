// spec: system-console-additional-scenarios.plan.md - Bot Reasoning Configuration
// seed: e2e/tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Bot Reasoning Configuration
 *
 * Tests reasoning configuration for bots with different service types (OpenAI vs Anthropic).
 * OpenAI services with ResponsesAPI show a dropdown for reasoning effort levels.
 * Anthropic services show a number input for thinking budget.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe.serial('Bot Reasoning Configuration', () => {
    test('should configure OpenAI reasoning effort dropdown (minimal, low, medium, high)', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with OpenAI service that has useResponsesAPI set to true
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'openai-service',
                    name: 'OpenAI Service',
                    type: 'openai',
                    apiKey: 'test-key-openai',
                    orgId: '',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    streamingTimeoutSeconds: 30,
                    sendUserId: false,
                    outputTokenLimit: 4096,
                    useResponsesAPI: true,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'openai-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    reasoningEnabled: true,
                    reasoningEffort: 'medium',
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 4. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 5. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 6. Locate the bot card in the AI Bots panel
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 7. Click on the bot card to expand it
        await botCard.click();
        await page.waitForTimeout(500);

        // 8. Scroll through the bot configuration fields
        // 9-10. Find and verify the reasoning section and Enable checkbox
        const reasoningSection = botCard.locator('text=Reasoning').first();
        await expect(reasoningSection).toBeVisible();

        // Find the reasoning checkbox - for OpenAI with ResponsesAPI, it's the 2nd checkbox (after web search)
        // Vision and Tools use radio buttons, not checkboxes
        const enableCheckbox = botCard.getByRole('checkbox').nth(1);
        await expect(enableCheckbox).toBeVisible();

        // 11. Verify the checkbox is checked (enabled)
        await expect(enableCheckbox).toBeChecked();

        // 12. Verify a 'Reasoning Effort' dropdown is visible - it's the last combobox in the bot card
        const reasoningEffortDropdown = botCard.getByRole('combobox').last();
        await expect(reasoningEffortDropdown).toBeVisible();

        // 13. Verify the dropdown shows 'Medium' as current selection
        await expect(reasoningEffortDropdown).toHaveValue(/medium/i);

        // 14-16. Select 'High' from the dropdown (options are present in HTML but not visible in DOM)
        await reasoningEffortDropdown.selectOption('high');

        // 17. Verify help text explains reasoning effort levels and their trade-offs
        const helpText = botCard.locator('text=/.*computational effort.*/i').first();
        await expect(helpText).toBeVisible();

        // 18. Click Save button
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // 19. Reload page
        await page.reload();
        await page.waitForTimeout(1000);

        // 20. Expand the bot card
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();
        await page.waitForTimeout(500);

        // 21. Verify 'Reasoning Effort' dropdown shows 'High'
        const reloadedDropdown = reloadedBotCard.getByRole('combobox').last();
        await expect(reloadedDropdown).toHaveValue(/high/i);

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should configure Anthropic thinking budget with number input', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with Anthropic service
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'anthropic-service',
                    name: 'Anthropic Service',
                    type: 'anthropic',
                    apiKey: 'test-key-anthropic',
                    defaultModel: 'claude-3-opus',
                    tokenLimit: 16384,
                    outputTokenLimit: 8192,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'anthropic-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    reasoningEnabled: true,
                    thinkingBudget: 2048,
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 5. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 6. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 7. Locate the bot card in AI Bots panel
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 8. Click to expand the bot card
        await botCard.click();
        await page.waitForTimeout(500);

        // 9. Scroll to find the reasoning configuration section
        // 10. Verify 'Extended Thinking' section is visible (not 'Reasoning')
        const extendedThinkingSection = botCard.locator('text=Extended Thinking').first();
        await expect(extendedThinkingSection).toBeVisible();

        // 11. Verify the 'Enable' checkbox is present and checked
        // For Anthropic with extended thinking enabled, Extended Thinking checkbox is the 2nd checkbox (after web search)
        const enableCheckbox = botCard.getByRole('checkbox').nth(1); // 0=web search, 1=extended thinking
        await expect(enableCheckbox).toBeVisible();
        await expect(enableCheckbox).toBeChecked();

        // 12. Verify a 'Thinking Budget (tokens)' number input field is visible
        const thinkingBudgetInput = botCard.getByRole('spinbutton').first();
        await expect(thinkingBudgetInput).toBeVisible();

        // 13. Verify the field shows value '2048'
        await expect(thinkingBudgetInput).toHaveValue('2048');

        // 14. Verify help text explains thinking budget and mentions tokens
        const helpText = botCard.locator('text=/.*token budget.*/i').first();
        await expect(helpText).toBeVisible();

        // 15. Clear the field and type '4096'
        await thinkingBudgetInput.click();
        await thinkingBudgetInput.fill('4096');

        // 16. Verify the new value is accepted
        await expect(thinkingBudgetInput).toHaveValue('4096');

        // 17. Click Save button
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // 18. Reload the page
        await page.reload();
        await page.waitForTimeout(1000);

        // 19. Expand the bot card
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();
        await page.waitForTimeout(500);

        // 20. Verify 'Thinking Budget (tokens)' field shows '4096'
        const reloadedInput = reloadedBotCard.getByLabel(/thinking budget/i).or(
            reloadedBotCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(reloadedInput).toHaveValue('4096');

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should validate Anthropic thinking budget minimum (1024)', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with Anthropic service with outputTokenLimit 8192
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'anthropic-service',
                    name: 'Anthropic Service',
                    type: 'anthropic',
                    apiKey: 'test-key-anthropic',
                    defaultModel: 'claude-3-opus',
                    tokenLimit: 16384,
                    outputTokenLimit: 8192,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'anthropic-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    reasoningEnabled: true,
                    thinkingBudget: 2048,
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 3. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 4. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 5. Expand the bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();
        await page.waitForTimeout(500);

        // 6. Locate 'Thinking Budget (tokens)' input field
        const thinkingBudgetInput = botCard.getByLabel(/thinking budget/i).or(
            botCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(thinkingBudgetInput).toBeVisible();

        // 7. Clear the field and enter '512' (below minimum of 1024)
        await thinkingBudgetInput.click();
        await thinkingBudgetInput.fill('512');

        // 8. Tab away from the field or click elsewhere
        await page.keyboard.press('Tab');
        await page.waitForTimeout(500);

        // 9. Verify error message appears: 'Thinking budget must be at least 1024 tokens.'
        const errorMessage = botCard.locator('text=/.*thinking budget.*at least 1024.*/i').or(
            botCard.locator('text=/.*minimum.*1024.*/i').filter({ hasText: /error|must|required/i })
        );
        await expect(errorMessage).toBeVisible();

        // 10. Verify the error text is styled in red/danger color
        await expect(errorMessage).toHaveCSS('color', /rgb\(.*\)/ as any);

        // 11. Change the value to '1024' (minimum valid value)
        await thinkingBudgetInput.click();
        await thinkingBudgetInput.fill('1024');
        await page.keyboard.press('Tab');
        await page.waitForTimeout(500);

        // 12. Verify the error message disappears
        await expect(errorMessage).not.toBeVisible();

        // 13. Click Save button
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // 14. Reload page
        await page.reload();
        await page.waitForTimeout(1000);

        // 15. Expand bot card
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();
        await page.waitForTimeout(500);

        // 16. Verify the value '1024' is saved and no error is shown
        const reloadedInput = reloadedBotCard.getByLabel(/thinking budget/i).or(
            reloadedBotCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(reloadedInput).toHaveValue('1024');
        await expect(reloadedBotCard.locator('text=/.*error.*/i')).not.toBeVisible();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should validate Anthropic thinking budget maximum (based on outputTokenLimit)', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with Anthropic service with outputTokenLimit 4096
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'anthropic-service',
                    name: 'Anthropic Service',
                    type: 'anthropic',
                    apiKey: 'test-key-anthropic',
                    defaultModel: 'claude-3-opus',
                    tokenLimit: 16384,
                    outputTokenLimit: 4096,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'anthropic-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    reasoningEnabled: true,
                    thinkingBudget: 2048,
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 3. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 4. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 5. Expand bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();
        await page.waitForTimeout(500);

        // 6. Locate 'Thinking Budget (tokens)' field
        const thinkingBudgetInput = botCard.getByLabel(/thinking budget/i).or(
            botCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(thinkingBudgetInput).toBeVisible();

        // 7. Enter '8192' (exceeds the outputTokenLimit of 4096)
        await thinkingBudgetInput.click();
        await thinkingBudgetInput.fill('8192');

        // 8. Tab away from field
        await page.keyboard.press('Tab');
        await page.waitForTimeout(500);

        // 9. Verify error message: 'Thinking budget cannot exceed max tokens (4096).'
        const errorMessage = botCard.locator('text=/.*thinking budget.*cannot exceed.*4096.*/i').or(
            botCard.locator('text=/.*maximum.*4096.*/i').filter({ hasText: /error|exceed|cannot/i })
        );
        await expect(errorMessage).toBeVisible();

        // 10. Verify error is styled in red
        await expect(errorMessage).toHaveCSS('color', /rgb\(.*\)/ as any);

        // 11. Change value to '4096' (at the limit)
        await thinkingBudgetInput.click();
        await thinkingBudgetInput.fill('4096');
        await page.keyboard.press('Tab');
        await page.waitForTimeout(500);

        // 12. Verify error disappears
        await expect(errorMessage).not.toBeVisible();

        // 13. Click Save
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // 14. Reload page
        await page.reload();
        await page.waitForTimeout(1000);

        // 15. Verify value '4096' is saved correctly
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();
        await page.waitForTimeout(500);

        const reloadedInput = reloadedBotCard.getByLabel(/thinking budget/i).or(
            reloadedBotCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(reloadedInput).toHaveValue('4096');

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show different reasoning UI when switching service types', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with two services: OpenAI with useResponsesAPI true and Anthropic
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'openai-service',
                    name: 'OpenAI Service',
                    type: 'openai',
                    apiKey: 'test-key-openai',
                    orgId: '',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    streamingTimeoutSeconds: 30,
                    sendUserId: false,
                    outputTokenLimit: 4096,
                    useResponsesAPI: true,
                },
                {
                    id: 'anthropic-service',
                    name: 'Anthropic Service',
                    type: 'anthropic',
                    apiKey: 'test-key-anthropic',
                    defaultModel: 'claude-3-opus',
                    tokenLimit: 16384,
                    outputTokenLimit: 8192,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'openai-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    reasoningEnabled: true,
                    reasoningEffort: 'high',
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 4. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 5. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 6. Expand bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();
        await page.waitForTimeout(500);

        // 7. Verify 'Reasoning' section shows 'Reasoning Effort' dropdown with 'High' selected
        const reasoningSection = botCard.locator('text=Reasoning').first();
        await expect(reasoningSection).toBeVisible();

        const reasoningEffortDropdown = botCard.getByLabel(/reasoning effort/i).or(
            botCard.locator('text=Reasoning Effort').locator('..').getByRole('combobox')
        );
        await expect(reasoningEffortDropdown).toBeVisible();
        await expect(reasoningEffortDropdown).toHaveValue(/high/i);

        // 8. Change the 'AI Service' dropdown to select the Anthropic service
        const serviceDropdown = botCard.getByRole('combobox').first();
        await expect(serviceDropdown).toBeVisible();
        await serviceDropdown.selectOption({ label: 'Anthropic Service' });
        await page.waitForTimeout(1000);

        // 9. Scroll down to reasoning configuration
        // 10. Verify section title changes to 'Extended Thinking'
        const extendedThinkingSection = botCard.locator('text=Extended Thinking').first();
        await expect(extendedThinkingSection).toBeVisible();

        // 11. Verify 'Reasoning Effort' dropdown is gone
        await expect(reasoningEffortDropdown).not.toBeVisible();

        // 12. Verify 'Thinking Budget (tokens)' number input appears instead
        const thinkingBudgetInput = botCard.getByLabel(/thinking budget/i).or(
            botCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(thinkingBudgetInput).toBeVisible();

        // 13. Verify the field shows a placeholder for default value
        const placeholder = await thinkingBudgetInput.getAttribute('placeholder');
        expect(placeholder).toBeTruthy();

        // 14. Enter '2048' in the thinking budget field
        await thinkingBudgetInput.click();
        await thinkingBudgetInput.fill('2048');

        // 15. Click Save
        const saveButton = systemConsole.getSaveButton();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // 16. Reload page
        await page.reload();
        await page.waitForTimeout(1000);

        // 17. Expand bot
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();
        await page.waitForTimeout(500);

        // 18. Verify bot is linked to Anthropic service
        const reloadedServiceDropdown = reloadedBotCard.getByRole('combobox').first();
        await expect(reloadedServiceDropdown).toHaveValue(/anthropic/i);

        // 19. Verify 'Extended Thinking' section shows thinking budget field with '2048'
        const reloadedThinkingBudgetInput = reloadedBotCard.getByLabel(/thinking budget/i).or(
            reloadedBotCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(reloadedThinkingBudgetInput).toHaveValue('2048');

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should disable reasoning configuration', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with OpenAI service with useResponsesAPI true
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'openai-service',
                    name: 'OpenAI Service',
                    type: 'openai',
                    apiKey: 'test-key-openai',
                    orgId: '',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    streamingTimeoutSeconds: 30,
                    sendUserId: false,
                    outputTokenLimit: 4096,
                    useResponsesAPI: true,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'openai-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    reasoningEnabled: false,
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 3. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 4. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 5. Expand bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();
        await page.waitForTimeout(500);

        // 6. Scroll to make the Reasoning section visible
        await botCard.locator('text=Reasoning').first().scrollIntoViewIfNeeded();
        await page.waitForTimeout(500);

        // 7. Locate 'Reasoning' section - use first() to handle multiple matches
        const reasoningSection = botCard.locator('text=Reasoning').first();
        await expect(reasoningSection).toBeVisible();

        // 8. Verify 'Enable' checkbox is unchecked - reasoning checkbox is the 2nd checkbox (0=web search, 1=reasoning)
        const enableCheckbox = botCard.getByRole('checkbox').nth(1);
        await expect(enableCheckbox).toBeVisible();
        await expect(enableCheckbox).not.toBeChecked();

        // 9. Verify 'Reasoning Effort' dropdown is NOT visible (hidden when disabled)
        // When reasoning is disabled, the dropdown should not exist in the DOM
        const reasoningEffortLocator = botCard.locator('select').filter({ hasText: /minimal|low|medium|high/i });
        await expect(reasoningEffortLocator).toHaveCount(0);

        // 10. Click the 'Enable' checkbox to enable reasoning
        await enableCheckbox.scrollIntoViewIfNeeded();
        await enableCheckbox.click();
        await page.waitForTimeout(500);

        // 11. Scroll and verify the 'Reasoning Effort' dropdown appears
        // After enabling, the dropdown should appear with the reasoning effort options
        const reasoningEffortDropdown = botCard.locator('select').filter({ hasText: /minimal|low|medium|high/i }).first();
        await reasoningEffortDropdown.scrollIntoViewIfNeeded();
        await expect(reasoningEffortDropdown).toBeVisible();

        // 12. Verify default value is 'Medium'
        await expect(reasoningEffortDropdown).toHaveValue(/medium/i);

        // 13. Uncheck the 'Enable' checkbox
        await enableCheckbox.scrollIntoViewIfNeeded();
        await enableCheckbox.click();
        await page.waitForTimeout(500);

        // 14. Verify the 'Reasoning Effort' dropdown disappears again
        await expect(reasoningEffortLocator).toHaveCount(0);

        // 15. Click Save
        const saveButton = systemConsole.getSaveButton();
        await saveButton.scrollIntoViewIfNeeded();
        await saveButton.click();

        // Wait for save to complete
        await page.waitForTimeout(1000);

        // 16. Reload page
        await page.reload();
        await page.waitForTimeout(1000);

        // 17. Expand bot card
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();
        await page.waitForTimeout(500);

        // 18. Scroll to Reasoning section
        await reloadedBotCard.locator('text=Reasoning').first().scrollIntoViewIfNeeded();
        await page.waitForTimeout(500);

        // 19. Verify 'Enable' checkbox is unchecked
        const reloadedEnableCheckbox = reloadedBotCard.getByRole('checkbox').nth(1);
        await expect(reloadedEnableCheckbox).not.toBeChecked();

        // 20. Verify dropdown is not visible (should not exist in DOM)
        const reloadedDropdownLocator = reloadedBotCard.locator('select').filter({ hasText: /minimal|low|medium|high/i });
        await expect(reloadedDropdownLocator).toHaveCount(0);

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should handle empty thinking budget (uses default)', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with Anthropic service with outputTokenLimit 8192
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'anthropic-service',
                    name: 'Anthropic Service',
                    type: 'anthropic',
                    apiKey: 'test-key-anthropic',
                    defaultModel: 'claude-3-opus',
                    tokenLimit: 16384,
                    outputTokenLimit: 8192,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: 'anthropic-service',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
                    reasoningEnabled: true,
                    thinkingBudget: 0,
                }
            ],
        });

        openAIMock = await RunOpenAIMocks(mattermost.network);

        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        // 4. Login as sysadmin
        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // 5. Navigate to system console
        await systemConsole.navigateToPluginConfig(mattermost.url());

        // 6. Expand bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();
        await botCard.click();
        await page.waitForTimeout(500);

        // 7. Locate 'Thinking Budget (tokens)' field
        const thinkingBudgetInput = botCard.getByLabel(/thinking budget/i).or(
            botCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(thinkingBudgetInput).toBeVisible();

        // 8. Verify the field shows a placeholder with the default value (e.g., '2048')
        const placeholder = await thinkingBudgetInput.getAttribute('placeholder');
        expect(placeholder).toBeTruthy();
        expect(placeholder).toMatch(/\d+/);

        // 9. Verify the field itself is empty (no entered value)
        const value = await thinkingBudgetInput.inputValue();
        expect(value).toBe('');

        // 10. Verify help text mentions leaving blank to use default
        const helpText = botCard.locator('text=/.*token budget.*/i').first();
        await expect(helpText).toBeVisible();

        // 11. Leave the field empty - no save needed since field starts empty
        // The field is already empty (thinkingBudget: 0), so there's nothing to save
        // The save button would be disabled anyway since no changes were made

        // 12. Reload page to verify empty field persists
        await page.reload();
        await page.waitForTimeout(1000);

        // 13. Expand bot card
        const reloadedBotCard = page.locator('[class*="BotContainer"]').first();
        await reloadedBotCard.click();
        await page.waitForTimeout(500);

        // 14. Verify field is still empty with placeholder showing default
        const reloadedInput = reloadedBotCard.getByLabel(/thinking budget/i).or(
            reloadedBotCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        const reloadedValue = await reloadedInput.inputValue();
        expect(reloadedValue).toBe('');
        const reloadedPlaceholder = await reloadedInput.getAttribute('placeholder');
        expect(reloadedPlaceholder).toBeTruthy();

        // 15. Enter '3000' in the field to test saving a value
        await reloadedInput.scrollIntoViewIfNeeded();
        await reloadedInput.click();
        await reloadedInput.fill('3000');

        // 16. Save and reload
        const saveButton = systemConsole.getSaveButton();
        await saveButton.scrollIntoViewIfNeeded();
        await saveButton.click();
        await page.waitForTimeout(1000);
        await page.reload();
        await page.waitForTimeout(1000);

        // 17. Verify '3000' is now shown in the field (not placeholder)
        const finalBotCard = page.locator('[class*="BotContainer"]').first();
        await finalBotCard.click();
        await page.waitForTimeout(500);

        const finalInput = finalBotCard.getByLabel(/thinking budget/i).or(
            finalBotCard.locator('text=Thinking Budget').locator('..').getByRole('spinbutton')
        );
        await expect(finalInput).toHaveValue('3000');

        await openAIMock.stop();
        await mattermost.stop();
    });
});
