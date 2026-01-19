// spec: system-console-additional-scenarios.plan.md - Bot Validation Badges
// seed: e2e/tests/seed.spec.ts

import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { SystemConsoleHelper } from 'helpers/system-console';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import RunSystemConsoleContainer, { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Bot Validation Badges
 *
 * Tests validation badges that appear in bot card headers for invalid configurations.
 * - "No Username" badge when bot.name is empty or whitespace
 * - "Invalid Username" badge when username doesn't match regex or starts with number
 * - "No Service Selected" badge when serviceID is empty or service doesn't exist
 * - Multiple validation badges can appear simultaneously
 * - Badges appear in collapsed card state with red/danger styling and alert icon
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe.serial('Bot Validation Badges', () => {
    test('should show "No Username" badge when username is empty', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with bot configured with empty name
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'service-1',
                    name: 'Test Service',
                    type: 'openai',
                    apiKey: 'test-key',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    outputTokenLimit: 4096,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: '',
                    displayName: 'Test Bot',
                    serviceID: 'service-1',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
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

        // 5. Locate the bot card in AI Bots panel
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 6. Verify the bot card header shows the display name 'Test Bot'
        await expect(botCard.getByText('Test Bot')).toBeVisible();

        // 7. Verify a red/danger badge with text 'No Username' is visible in the header
        const noUsernameBadge = botCard.locator('text=No Username');
        await expect(noUsernameBadge).toBeVisible();

        // 8. Verify the badge includes an alert icon
        // Badge should have alert/warning icon styling (DangerPill component)
        const badgeContainer = botCard.locator('[class*="DangerPill"]').filter({ hasText: 'No Username' });
        await expect(badgeContainer).toBeVisible();

        // 9. Click on the bot card to expand it
        await botCard.click();

        // Wait for the card to fully expand and form fields to render
        // Use a longer wait for CI environments where rendering can be slower
        await page.waitForTimeout(1000);

        // 10. Enter a valid username 'testbot' in the 'Agent Username' field
        const usernameField = botCard.getByRole('textbox', { name: /(bot|agent) username/i });
        await usernameField.waitFor({ state: 'visible', timeout: 10000 });
        await usernameField.fill('testbot');

        // 11. Click outside the card to collapse it
        await systemConsole.getBotsPanel().click();

        // 12. Verify the 'No Username' badge is no longer visible in the header
        await expect(noUsernameBadge).not.toBeVisible();

        // 13. Clear the username field again
        await botCard.click();
        await usernameField.clear();

        // 14. Verify the 'No Username' badge reappears
        await systemConsole.getBotsPanel().click();
        await expect(noUsernameBadge).toBeVisible();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show "Invalid Username" badge for username with spaces', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with bot configured with name containing space
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'service-1',
                    name: 'Test Service',
                    type: 'openai',
                    apiKey: 'test-key',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    outputTokenLimit: 4096,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'test bot',
                    displayName: 'Test Bot',
                    serviceID: 'service-1',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
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

        // 5. Locate bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 6. Verify red/danger badge with text 'Invalid Username' is visible in header
        const invalidUsernameBadge = botCard.locator('text=Invalid Username');
        await expect(invalidUsernameBadge).toBeVisible();

        // 7. Verify badge includes alert icon
        const badgeContainer = botCard.locator('[class*="DangerPill"]').filter({ hasText: 'Invalid Username' });
        await expect(badgeContainer).toBeVisible();

        // 8. Expand bot card
        await botCard.click();

        // Wait for the card to fully expand and form fields to render
        await page.waitForTimeout(1000);

        // 9. Verify the 'Agent Username' field contains 'test bot'
        const usernameField = botCard.getByRole('textbox', { name: /(bot|agent) username/i });
        await usernameField.waitFor({ state: 'visible', timeout: 10000 });
        await expect(usernameField).toHaveValue('test bot');

        // 10. Change username to 'testbot' (no space, all lowercase)
        await usernameField.fill('testbot');

        // 11. Collapse card
        await systemConsole.getBotsPanel().click();

        // 12. Verify 'Invalid Username' badge is gone
        await expect(invalidUsernameBadge).not.toBeVisible();

        // 13. Expand card again
        await botCard.click();

        // 14. Change username to 'Test Bot' (with space)
        await usernameField.fill('Test Bot');

        // 15. Verify badge reappears after losing focus on field
        await systemConsole.getBotsPanel().click();
        await expect(invalidUsernameBadge).toBeVisible();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show "Invalid Username" badge for username starting with number', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with bot configured with name starting with number
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'service-1',
                    name: 'Test Service',
                    type: 'openai',
                    apiKey: 'test-key',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    outputTokenLimit: 4096,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: '1testbot',
                    displayName: 'Test Bot',
                    serviceID: 'service-1',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
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

        // 5. Locate bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 6. Verify 'Invalid Username' badge is visible
        const invalidUsernameBadge = botCard.locator('text=Invalid Username');
        await expect(invalidUsernameBadge).toBeVisible();

        // 7. Expand bot card
        await botCard.click();

        // Wait for the card to fully expand and form fields to render
        await page.waitForTimeout(1000);

        // 8. Verify the username field contains '1testbot'
        const usernameField = botCard.getByRole('textbox', { name: /(bot|agent) username/i });
        await usernameField.waitFor({ state: 'visible', timeout: 10000 });
        await expect(usernameField).toHaveValue('1testbot');

        // 9. Change username to 'testbot1' (starts with letter)
        await usernameField.fill('testbot1');

        // 10. Collapse card
        await systemConsole.getBotsPanel().click();

        // 11. Verify badge disappears
        await expect(invalidUsernameBadge).not.toBeVisible();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show "Invalid Username" badge for uppercase letters', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with bot configured with uppercase name
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'service-1',
                    name: 'Test Service',
                    type: 'openai',
                    apiKey: 'test-key',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    outputTokenLimit: 4096,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'TestBot',
                    displayName: 'Test Bot',
                    serviceID: 'service-1',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
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

        // 5. Locate bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 6. Verify 'Invalid Username' badge is visible
        const invalidUsernameBadge = botCard.locator('text=Invalid Username');
        await expect(invalidUsernameBadge).toBeVisible();

        // 7. Expand bot card
        await botCard.click();

        // Wait for the card to fully expand and form fields to render
        await page.waitForTimeout(1000);

        // 8. Change username to 'testbot' (all lowercase)
        const usernameField = botCard.getByRole('textbox', { name: /(bot|agent) username/i });
        await usernameField.waitFor({ state: 'visible', timeout: 10000 });
        await usernameField.fill('testbot');

        // 9. Verify badge disappears
        await systemConsole.getBotsPanel().click();
        await expect(invalidUsernameBadge).not.toBeVisible();

        // 10. Change username to 'TESTBOT' (all uppercase)
        await botCard.click();
        await usernameField.fill('TESTBOT');

        // 11. Verify badge reappears
        await systemConsole.getBotsPanel().click();
        await expect(invalidUsernameBadge).toBeVisible();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show "No Service Selected" badge when service is missing', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with bot configured with empty serviceID
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'service-1',
                    name: 'Test Service',
                    type: 'openai',
                    apiKey: 'test-key',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    outputTokenLimit: 4096,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: 'testbot',
                    displayName: 'Test Bot',
                    serviceID: '',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
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

        // 5. Locate bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 6. Verify red/danger badge with text 'No Service Selected' is visible
        const noServiceBadge = botCard.locator('text=No Service Selected');
        await expect(noServiceBadge).toBeVisible();

        // 7. Verify badge includes alert icon
        const badgeContainer = botCard.locator('[class*="DangerPill"]').filter({ hasText: 'No Service Selected' });
        await expect(badgeContainer).toBeVisible();

        // 8. Expand bot card
        await botCard.click();

        // Wait for the card to fully expand and form fields to render
        await page.waitForTimeout(1000);

        // 9. Locate 'AI Service' dropdown
        // 10. Verify dropdown shows 'Select a service' placeholder
        const serviceDropdown = botCard.getByLabel(/ai service/i).or(
            botCard.locator('text=AI Service').locator('..').locator('select')
        );
        await serviceDropdown.waitFor({ state: 'visible', timeout: 10000 });

        // 11. Select the available service from dropdown
        await serviceDropdown.selectOption({ label: 'Test Service' });

        // 12. Collapse card
        await systemConsole.getBotsPanel().click();

        // 13. Verify 'No Service Selected' badge is gone
        await expect(noServiceBadge).not.toBeVisible();

        // 14. Expand card
        await botCard.click();

        // 15. Change dropdown back to 'Select a service'
        await serviceDropdown.selectOption({ value: '' });

        // 16. Verify badge reappears
        await systemConsole.getBotsPanel().click();
        await expect(noServiceBadge).toBeVisible();

        await openAIMock.stop();
        await mattermost.stop();
    });

    test('should show multiple validation badges simultaneously', async ({ page }) => {
        test.setTimeout(60000);

        // Start container with bot configured with both empty name and empty serviceID
        mattermost = await RunSystemConsoleContainer({
            services: [
                {
                    id: 'service-1',
                    name: 'Test Service',
                    type: 'openai',
                    apiKey: 'test-key',
                    defaultModel: 'gpt-4',
                    tokenLimit: 16384,
                    outputTokenLimit: 4096,
                }
            ],
            bots: [
                {
                    id: 'bot-1',
                    name: '',
                    displayName: 'Broken Bot',
                    serviceID: '',
                    customInstructions: 'You are a helpful assistant',
                    enableVision: false,
                    enableTools: false,
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

        // 5. Locate bot card
        const botCard = page.locator('[class*="BotContainer"]').first();
        await expect(botCard).toBeVisible();

        // 6. Verify TWO badges are visible in header: 'No Service Selected' and 'No Username'
        const noServiceBadge = botCard.locator('text=No Service Selected');
        const noUsernameBadge = botCard.locator('text=No Username');
        await expect(noServiceBadge).toBeVisible();
        await expect(noUsernameBadge).toBeVisible();

        // 7. Verify both badges are red/danger styled
        // 8. Verify both badges have alert icons
        const noServiceBadgeContainer = botCard.locator('[class*="DangerPill"]').filter({ hasText: 'No Service Selected' });
        const noUsernameBadgeContainer = botCard.locator('[class*="DangerPill"]').filter({ hasText: 'No Username' });
        await expect(noServiceBadgeContainer).toBeVisible();
        await expect(noUsernameBadgeContainer).toBeVisible();

        // 9. Expand bot card
        await botCard.click();

        // Wait for the card to fully expand and form fields to render
        await page.waitForTimeout(1000);

        // 10. Add valid username 'testbot'
        const usernameField = botCard.getByRole('textbox', { name: /(bot|agent) username/i });
        await usernameField.waitFor({ state: 'visible', timeout: 10000 });
        await usernameField.fill('testbot');

        // 11. Verify 'No Username' badge disappears but 'No Service Selected' remains
        await systemConsole.getBotsPanel().click();
        await expect(noUsernameBadge).not.toBeVisible();
        await expect(noServiceBadge).toBeVisible();

        // 12. Select a service from dropdown
        await botCard.click();
        const serviceDropdown = botCard.getByLabel(/ai service/i).or(
            botCard.locator('text=AI Service').locator('..').locator('select')
        );
        await serviceDropdown.selectOption({ label: 'Test Service' });

        // 13. Verify both badges are now gone
        await systemConsole.getBotsPanel().click();
        await expect(noServiceBadge).not.toBeVisible();
        await expect(noUsernameBadge).not.toBeVisible();

        // 14. Clear username field
        await botCard.click();
        await usernameField.clear();

        // 15. Verify only 'No Username' badge reappears
        await systemConsole.getBotsPanel().click();
        await expect(noUsernameBadge).toBeVisible();
        await expect(noServiceBadge).not.toBeVisible();

        await openAIMock.stop();
        await mattermost.stop();
    });
});
