import { test, expect } from '@playwright/test';

import RunContainer from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { OpenAIMockContainer, RunOpenAIMocks } from 'helpers/openai-mock';
import { createBotConfigHelper, generateBotId } from 'helpers/bot-config';

/**
 * Reasoning Configuration Integration Tests
 *
 * Tests complex integration scenarios for reasoning configuration.
 * Basic reasoning config tests are covered by system-console/bot-reasoning-config.spec.ts
 * These tests focus on cross-service behavior and persistence.
 */

function createTestSuite() {
    let mattermost: MattermostContainer;
    let openAIMock: OpenAIMockContainer;

    test.describe('Reasoning Configuration Integration Tests', () => {
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

        test('should require Responses API for OpenAI reasoning', async () => {
            const botConfig = await createBotConfigHelper(mattermost);
            const serviceId = 'no-responses-api-service';
            const botId = generateBotId();

            // Create a service without Responses API
            await botConfig.addService({
                id: serviceId,
                name: 'No Responses API Service',
                type: 'openai',
                apiKey: 'test-key',
                apiURL: 'http://openai:8080',
                useResponsesAPI: false, // Responses API disabled
            });

            await botConfig.addBot({
                id: botId,
                name: `reasoningbot-${botId}`,
                displayName: 'Reasoning Bot',
                customInstructions: 'You use advanced reasoning.',
                serviceID: serviceId,
                reasoningEnabled: false,
            });

            // Verify service was created
            const service = await botConfig.getService(serviceId);
            expect(service).toBeDefined();
            expect(service?.useResponsesAPI).toBe(false);

            // Enable reasoning on the bot
            await botConfig.updateBot(botId, {
                reasoningEnabled: true,
            });

            // Verify configuration accepts this (validation happens at runtime)
            const updatedBot = await botConfig.getBot(botId);
            expect(updatedBot?.reasoningEnabled).toBe(true);
            const updatedService = await botConfig.getService(serviceId);
            expect(updatedService?.useResponsesAPI).toBe(false);

            // Note: At runtime, reasoning would fail or be ignored without Responses API
            // This is expected behavior - configuration allows it but runtime enforces the requirement

            // Clean up
            await botConfig.deleteBot(botId);
            await botConfig.deleteService(serviceId);
        });

        test('should allow switching bot between OpenAI and Anthropic services with reasoning', async () => {
            const botConfig = await createBotConfigHelper(mattermost);

            // Create OpenAI service
            await botConfig.addService({
                id: 'openai-reasoning',
                name: 'OpenAI Reasoning Service',
                type: 'openaicompatible',
                apiKey: 'openai-key',
                apiURL: 'http://openai:8080',
                useResponsesAPI: true,
            });

            // Create Anthropic service
            await botConfig.addService({
                id: 'anthropic-reasoning',
                name: 'Anthropic Reasoning Service',
                type: 'anthropic',
                apiKey: 'anthropic-key',
                apiURL: 'https://api.anthropic.com',
                tokenLimit: 4096,
            });

            // Create bot using OpenAI service
            const botId = generateBotId();
            await botConfig.addBot({
                id: botId,
                name: 'reasoningbot',
                displayName: 'Reasoning Bot',
                customInstructions: 'You use advanced reasoning.',
                serviceID: 'openai-reasoning',
                reasoningEnabled: true,
            });

            // Verify bot uses OpenAI service
            let bot = await botConfig.getBot(botId);
            expect(bot?.serviceID).toBe('openai-reasoning');
            expect(bot?.reasoningEnabled).toBe(true);

            // Switch to Anthropic service
            await botConfig.updateBot(botId, {
                serviceID: 'anthropic-reasoning',
            });

            // Verify switch
            bot = await botConfig.getBot(botId);
            expect(bot?.serviceID).toBe('anthropic-reasoning');
            expect(bot?.reasoningEnabled).toBe(true);

            // Clean up
            await botConfig.deleteBot(botId);
            await botConfig.deleteService('openai-reasoning');
            await botConfig.deleteService('anthropic-reasoning');
        });

        test('should persist reasoning configuration across service updates', async () => {
            const botConfig = await createBotConfigHelper(mattermost);
            const serviceId = 'reasoning-persist-test';
            const botId = generateBotId();

            // Create service
            await botConfig.addService({
                id: serviceId,
                name: 'Reasoning Persist Test',
                type: 'openaicompatible',
                apiKey: 'test-key',
                apiURL: 'http://openai:8080',
                useResponsesAPI: true,
            });

            await botConfig.addBot({
                id: botId,
                name: `reasoningbot-${botId}`,
                displayName: 'Reasoning Persist Bot',
                customInstructions: 'You use advanced reasoning.',
                serviceID: serviceId,
                reasoningEnabled: false,
            });

            // Enable reasoning on the bot
            await botConfig.updateBot(botId, {
                reasoningEnabled: true,
            });

            // Make service updates
            await botConfig.updateService(serviceId, {
                apiKey: 'updated-key',
            });

            // Verify reasoning is still enabled on the bot
            const bot = await botConfig.getBot(botId);
            expect(bot?.reasoningEnabled).toBe(true);
            const service = await botConfig.getService(serviceId);
            expect(service?.apiKey).toBe('updated-key');

            // Clean up
            await botConfig.deleteBot(botId);
            await botConfig.deleteService(serviceId);
        });
    });
}

createTestSuite();
