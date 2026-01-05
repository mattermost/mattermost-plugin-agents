import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { OpenAIMockContainer, RunOpenAIMocks, responseTest, responseTestText } from 'helpers/openai-mock';
import { createBotConfigHelper, generateBotId } from 'helpers/bot-config';

// Test configuration
const username = 'regularuser';
const password = 'regularuser';

function createTestSuite() {
    let mattermost: MattermostContainer;
    let openAIMock: OpenAIMockContainer;

    // Common test setup
    async function setupTestPage(page) {
        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);
        const url = mattermost.url();

        await mmPage.login(url, username, password);

        return { mmPage, aiPlugin };
    }

    test.describe('Service Changes Tests', () => {
        // Setup for all tests in the file
        test.beforeAll(async () => {
            mattermost = await RunContainer();
            openAIMock = await RunOpenAIMocks(mattermost.network);
        });

        // Cleanup after all tests
        test.afterAll(async () => {
            await openAIMock.stop();
            await mattermost.stop();
        });

        test.describe('Service Provider Changes', () => {
            test('should switch bot between different services', async ({ page }) => {
                const { aiPlugin } = await setupTestPage(page);
                const botConfig = await createBotConfigHelper(mattermost);

                // Get the original bot configuration
                const originalBot = await botConfig.getBotByName('mock');
                expect(originalBot).toBeDefined();
                expect(originalBot?.serviceID).toBe('mock-service');

                // Switch to second service
                await botConfig.updateBot(originalBot!.id, {
                    serviceID: 'second-service'
                });

                // Verify the change
                const updatedBot = await botConfig.getBot(originalBot!.id);
                expect(updatedBot?.serviceID).toBe('second-service');

                // Test bot functionality with new service
                await aiPlugin.openRHS();
                await openAIMock.addCompletionMock(responseTest, 'second');
                await aiPlugin.sendMessage('Hello with new service');
                await aiPlugin.waitForBotResponse(responseTestText);

                // Restore original service
                await botConfig.updateBot(originalBot!.id, {
                    serviceID: 'mock-service'
                });
            });

            test('should handle bot with non-existent service gracefully', async ({ page }) => {
                const { aiPlugin } = await setupTestPage(page);
                const botConfig = await createBotConfigHelper(mattermost);

                // Create a test bot
                const botId = generateBotId();
                await botConfig.addBot({
                    id: botId,
                    name: 'testorphan',
                    displayName: 'Test Orphan Bot',
                    customInstructions: '',
                    serviceID: 'deleted-service' // Non-existent service
                });

                // Verify bot was created
                const bot = await botConfig.getBot(botId);
                expect(bot).toBeDefined();
                expect(bot?.serviceID).toBe('deleted-service');

                // Verify service doesn't exist
                const service = await botConfig.getService('deleted-service');
                expect(service).toBeUndefined();

                // The bot should exist in configuration even without valid service
                await aiPlugin.openRHS();

                // Bot may or may not appear in selector depending on validation logic
                // The main test is that configuration accepts this state

                // Clean up
                await botConfig.deleteBot(botId);
            });

            test('should persist service change after page reload', async ({ page }) => {
                const { aiPlugin } = await setupTestPage(page);
                const botConfig = await createBotConfigHelper(mattermost);

                // Get the original bot
                const originalBot = await botConfig.getBotByName('mock');
                expect(originalBot).toBeDefined();

                // Switch service
                await botConfig.updateBot(originalBot!.id, {
                    serviceID: 'second-service'
                });

                // Reload page
                await page.reload();
                await page.waitForLoadState('domcontentloaded');

                // Verify service is still changed
                const botAfterReload = await botConfig.getBot(originalBot!.id);
                expect(botAfterReload?.serviceID).toBe('second-service');

                // Test bot works with the service
                await aiPlugin.openRHS();
                await openAIMock.addCompletionMock(responseTest, 'second');
                await aiPlugin.sendMessage('Test after reload');
                await aiPlugin.waitForBotResponse(responseTestText);

                // Restore original service
                await botConfig.updateBot(originalBot!.id, {
                    serviceID: 'mock-service'
                });
            });

            test('should allow multiple bots to share same service', async () => {
                const botConfig = await createBotConfigHelper(mattermost);

                // Create two new bots using same service
                const botId1 = generateBotId();
                const botId2 = generateBotId();

                await botConfig.addBot({
                    id: botId1,
                    name: 'sharedbot1',
                    displayName: 'Shared Bot 1',
                    customInstructions: '',
                    serviceID: 'mock-service'
                });

                await botConfig.addBot({
                    id: botId2,
                    name: 'sharedbot2',
                    displayName: 'Shared Bot 2',
                    customInstructions: '',
                    serviceID: 'mock-service'
                });

                // Verify both bots use same service
                const bot1 = await botConfig.getBot(botId1);
                const bot2 = await botConfig.getBot(botId2);

                expect(bot1?.serviceID).toBe('mock-service');
                expect(bot2?.serviceID).toBe('mock-service');
                expect(bot1?.serviceID).toBe(bot2?.serviceID);

                // Clean up
                await botConfig.deleteBot(botId1);
                await botConfig.deleteBot(botId2);
            });

            test('should handle changing service while bot is in active conversation', async ({ page }) => {
                const { aiPlugin } = await setupTestPage(page);
                const botConfig = await createBotConfigHelper(mattermost);

                // Open RHS and start conversation
                await aiPlugin.openRHS();
                await openAIMock.addCompletionMock(responseTest);
                await aiPlugin.sendMessage('First message with original service');
                await aiPlugin.waitForBotResponse(responseTestText);

                // Get bot config
                const originalBot = await botConfig.getBotByName('mock');
                expect(originalBot).toBeDefined();

                // Change service while conversation is active
                await botConfig.updateBot(originalBot!.id, {
                    serviceID: 'second-service'
                });

                // Send follow-up message - should use new service
                await openAIMock.addCompletionMock(responseTest, 'second');
                await aiPlugin.sendMessage('Second message with new service');
                await aiPlugin.waitForBotResponse(responseTestText);

                // Restore original service
                await botConfig.updateBot(originalBot!.id, {
                    serviceID: 'mock-service'
                });
            });
        });

        test.describe('Service Configuration Changes', () => {
            test('should update service API URL', async () => {
                const botConfig = await createBotConfigHelper(mattermost);

                // Get original service
                const originalService = await botConfig.getService('second-service');
                expect(originalService).toBeDefined();

                const originalURL = originalService!.apiURL;

                // Update API URL
                await botConfig.updateService('second-service', {
                    apiURL: 'http://openai:8080/updated'
                });

                // Verify change
                const updatedService = await botConfig.getService('second-service');
                expect(updatedService?.apiURL).toBe('http://openai:8080/updated');

                // Restore original URL
                await botConfig.updateService('second-service', {
                    apiURL: originalURL
                });
            });

            test('should update service model configuration', async () => {
                const botConfig = await createBotConfigHelper(mattermost);

                // Get original service
                const originalService = await botConfig.getService('mock-service');
                expect(originalService).toBeDefined();

                // Update default model
                await botConfig.updateService('mock-service', {
                    defaultModel: 'gpt-4-turbo'
                });

                // Verify change
                const updatedService = await botConfig.getService('mock-service');
                expect(updatedService?.defaultModel).toBe('gpt-4-turbo');

                // Restore (remove default model)
                await botConfig.updateService('mock-service', {
                    defaultModel: originalService!.defaultModel
                });
            });

            test('should create new service and assign to bot', async ({ page }) => {
                const { aiPlugin } = await setupTestPage(page);
                const botConfig = await createBotConfigHelper(mattermost);

                // Create new service
                await botConfig.addService({
                    id: 'new-test-service',
                    name: 'New Test Service',
                    type: 'openaicompatible',
                    apiKey: 'test-key',
                    apiURL: 'http://openai:8080/test',
                    useResponsesAPI: true,
                    reasoningEnabled: false
                });

                // Verify service was created
                const newService = await botConfig.getService('new-test-service');
                expect(newService).toBeDefined();
                expect(newService?.name).toBe('New Test Service');

                // Create bot using new service
                const botId = generateBotId();
                await botConfig.addBot({
                    id: botId,
                    name: 'newservicebot',
                    displayName: 'New Service Bot',
                    customInstructions: '',
                    serviceID: 'new-test-service'
                });

                // Verify bot uses new service
                const bot = await botConfig.getBot(botId);
                expect(bot?.serviceID).toBe('new-test-service');

                // Clean up
                await botConfig.deleteBot(botId);
                await botConfig.deleteService('new-test-service');
            });

            test('should handle deleting service that is in use by bot', async () => {
                const botConfig = await createBotConfigHelper(mattermost);

                // Create service and bot
                await botConfig.addService({
                    id: 'temp-service',
                    name: 'Temporary Service',
                    type: 'openaicompatible',
                    apiKey: 'temp',
                    apiURL: 'http://openai:8080/temp'
                });

                const botId = generateBotId();
                await botConfig.addBot({
                    id: botId,
                    name: 'tempbot',
                    displayName: 'Temp Bot',
                    customInstructions: '',
                    serviceID: 'temp-service'
                });

                // Delete service (bot will reference non-existent service)
                await botConfig.deleteService('temp-service');

                // Verify service is gone
                const deletedService = await botConfig.getService('temp-service');
                expect(deletedService).toBeUndefined();

                // Bot still exists but references deleted service
                const orphanedBot = await botConfig.getBot(botId);
                expect(orphanedBot).toBeDefined();
                expect(orphanedBot?.serviceID).toBe('temp-service');

                // Clean up
                await botConfig.deleteBot(botId);
            });

            test('should update service affecting all bots using it', async () => {
                const botConfig = await createBotConfigHelper(mattermost);

                // Get both bots using different services
                const bot1 = await botConfig.getBotByName('mock');
                const bot2 = await botConfig.getBotByName('second');

                expect(bot1?.serviceID).toBe('mock-service');
                expect(bot2?.serviceID).toBe('second-service');

                // Update the mock-service
                const originalService = await botConfig.getService('mock-service');
                await botConfig.updateService('mock-service', {
                    apiKey: 'updated-key'
                });

                // Verify service was updated
                const updatedService = await botConfig.getService('mock-service');
                expect(updatedService?.apiKey).toBe('updated-key');

                // Bot1 still references same service (but service has new config)
                const bot1AfterUpdate = await botConfig.getBot(bot1!.id);
                expect(bot1AfterUpdate?.serviceID).toBe('mock-service');

                // Restore original key
                await botConfig.updateService('mock-service', {
                    apiKey: originalService!.apiKey
                });
            });
        });
    });
}

createTestSuite();
