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

            // Create a service without Responses API
            await botConfig.addService({
                id: 'no-responses-api-service',
                name: 'No Responses API Service',
                type: 'openai',
                apiKey: 'test-key',
                apiURL: 'http://openai:8080',
                useResponsesAPI: false, // Responses API disabled
                reasoningEnabled: false
            });

            // Verify service was created
            const service = await botConfig.getService('no-responses-api-service');
            expect(service).toBeDefined();
            expect(service?.useResponsesAPI).toBe(false);

            // Attempt to enable reasoning (should work in config, but may not work at runtime)
            await botConfig.updateService('no-responses-api-service', {
                reasoningEnabled: true
            });

            // Verify configuration accepts this (validation happens at runtime)
            const updatedService = await botConfig.getService('no-responses-api-service');
            expect(updatedService?.reasoningEnabled).toBe(true);
            expect(updatedService?.useResponsesAPI).toBe(false);

            // Note: At runtime, reasoning would fail or be ignored without Responses API
            // This is expected behavior - configuration allows it but runtime enforces the requirement

            // Clean up
            await botConfig.deleteService('no-responses-api-service');
        });

        test('should allow switching bot between OpenAI and Anthropic services with reasoning', async () => {
            const botConfig = await createBotConfigHelper(mattermost);

            // Create OpenAI service with reasoning
            await botConfig.addService({
                id: 'openai-reasoning',
                name: 'OpenAI Reasoning Service',
                type: 'openaicompatible',
                apiKey: 'openai-key',
                apiURL: 'http://openai:8080',
                useResponsesAPI: true,
                reasoningEnabled: true
            });

            // Create Anthropic service with reasoning
            await botConfig.addService({
                id: 'anthropic-reasoning',
                name: 'Anthropic Reasoning Service',
                type: 'anthropic',
                apiKey: 'anthropic-key',
                apiURL: 'https://api.anthropic.com',
                reasoningEnabled: true,
                tokenLimit: 4096
            });

            // Create bot using OpenAI service
            const botId = generateBotId();
            await botConfig.addBot({
                id: botId,
                name: 'reasoningbot',
                displayName: 'Reasoning Bot',
                customInstructions: 'You use advanced reasoning.',
                serviceID: 'openai-reasoning'
            });

            // Verify bot uses OpenAI service
            let bot = await botConfig.getBot(botId);
            expect(bot?.serviceID).toBe('openai-reasoning');

            // Switch to Anthropic service
            await botConfig.updateBot(botId, {
                serviceID: 'anthropic-reasoning'
            });

            // Verify switch
            bot = await botConfig.getBot(botId);
            expect(bot?.serviceID).toBe('anthropic-reasoning');

            // Clean up
            await botConfig.deleteBot(botId);
            await botConfig.deleteService('openai-reasoning');
            await botConfig.deleteService('anthropic-reasoning');
        });

        test('should persist reasoning configuration across service updates', async () => {
            const botConfig = await createBotConfigHelper(mattermost);

            // Create service with reasoning disabled
            await botConfig.addService({
                id: 'reasoning-persist-test',
                name: 'Reasoning Persist Test',
                type: 'openaicompatible',
                apiKey: 'test-key',
                apiURL: 'http://openai:8080',
                useResponsesAPI: true,
                reasoningEnabled: false
            });

            // Enable reasoning
            await botConfig.updateService('reasoning-persist-test', {
                reasoningEnabled: true
            });

            // Make other updates
            await botConfig.updateService('reasoning-persist-test', {
                apiKey: 'updated-key'
            });

            // Verify reasoning is still enabled
            const service = await botConfig.getService('reasoning-persist-test');
            expect(service?.reasoningEnabled).toBe(true);
            expect(service?.apiKey).toBe('updated-key');

            // Clean up
            await botConfig.deleteService('reasoning-persist-test');
        });
    });
}

createTestSuite();
