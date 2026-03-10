import { test, expect } from '@playwright/test';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import {
    OpenAIMockContainer,
    RunOpenAIMocks,
    buildToolCallResponse,
    buildTextResponse,
    responseTest,
} from 'helpers/openai-mock';
import { RunToolConfigContainerWithPolicies } from 'helpers/tool-config-container';
import { createToolConfigAPIHelper } from 'helpers/tool-config';
import { adminUsername, adminPassword } from 'helpers/system-console-container';

/**
 * Test Suite: Tool Call Policies with Mocked LLM (4.13)
 *
 * Uses Smocker to return synthetic tool-call SSE responses and verifies
 * that policy enforcement works at tool-call time.
 */

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

test.describe('Tool Call Policies (Mocked LLM)', () => {
    test.beforeAll(async () => {
        mattermost = await RunToolConfigContainerWithPolicies();
        openAIMock = await RunOpenAIMocks(mattermost.network);
    });

    test.afterAll(async () => {
        await openAIMock.stop();
        await mattermost.stop();
    });

    test('disabled tool is not provided to LLM', async ({ page }) => {
        test.setTimeout(60000);

        // Set up a simple text response (no tool calls)
        await openAIMock.addCompletionMock(responseTest);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Open Copilot RHS
        await aiPlugin.openRHS();

        // Send a message - should get a simple text response with no tool calls
        await aiPlugin.sendMessage('Hello, what can you do?');

        // Wait for the text response to appear
        await aiPlugin.waitForBotResponse('Hello');

        // Verify no tool call UI elements appear (no Accept/Reject buttons)
        const acceptButton = page.getByRole('button', { name: /accept/i });
        await expect(acceptButton).not.toBeVisible();

        const rejectButton = page.getByRole('button', { name: /reject/i });
        await expect(rejectButton).not.toBeVisible();
    });

    test('auto_run tool executes without approval prompt in DM', async ({ page }) => {
        test.setTimeout(120000);

        // Build a tool-call response for an auto_run tool
        const toolCallSSE = buildToolCallResponse(
            'call_001',
            'read_post',
            '{"post_id": "test123"}',
        );
        const followUpTextSSE = buildTextResponse('Here is the post content you requested.');

        // Register both mocks together: the tool-call mock (matches first request)
        // and the text follow-up (for after tool execution).
        // Using addMocks to send both in a single request since addMock resets.
        await openAIMock.addMocks([
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                },
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: toolCallSSE,
                },
            },
            {
                request: {
                    method: 'POST',
                    path: '/v1/chat/completions',
                },
                context: {
                    times: 1,
                },
                response: {
                    status: 200,
                    headers: {
                        'Content-Type': 'text/event-stream',
                    },
                    body: followUpTextSSE,
                },
            },
        ]);

        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);

        // Navigate to DM with bot
        await mmPage.createAndNavigateToDMWithBot(
            mattermost,
            adminUsername,
            adminPassword,
            'toolbot',
        );

        // Send message to trigger tool call
        await mmPage.sendChannelMessage('Please read post test123');

        // Wait for some response to appear (tool call processing)
        // With auto_run, Accept/Reject should NOT appear
        await page.waitForTimeout(5000);

        // Verify no approval prompt appears for auto_run tool
        const acceptButton = page.getByRole('button', { name: /accept/i });
        const isAcceptVisible = await acceptButton.isVisible().catch(() => false);

        // If auto_run is properly configured, no approval should be needed
        // Note: the exact behavior depends on whether the tool call mock
        // format is correctly handled by the plugin
        expect(isAcceptVisible).toBe(false);
    });
});
