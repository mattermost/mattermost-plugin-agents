// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, test} from '@playwright/test';
import type {Page} from '@playwright/test';

import {AIPlugin} from 'helpers/ai-plugin';
import {
    expectSelectedAgentPreference,
    resetSelectedAgentPreference,
    selectedAgentPreference,
} from 'helpers/agent_preferences';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {
    buildChatCompletionMockRule,
    buildTextResponse,
    buildTitleMockRule,
    OpenAIMockContainer,
    RunOpenAIMocks,
} from 'helpers/openai-mock';
import type {OpenAIChatCompletionRequest, OpenAIChatMessage} from 'helpers/openai-mock';
import {mattermostAIPluginRoutes, PluginRoutesApi} from 'helpers/plugin-http';
import RunContainer from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const mockBotUsername = 'mock';
const mockBotDisplayName = 'Mock Bot';
const secondBotUsername = 'second';
const secondBotDisplayName = 'Second Bot';
const titlePrompt = 'Write a short title for the following request. Include only the title and nothing else, no quotations. Request:';
const defaultProviderPath = /^\/(?:v1\/)?chat\/completions$/;
const secondProviderPath = /^\/second\/(?:v1\/)?chat\/completions$/;

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

type AIThreadSummary = {
    id: string;
    title: string;
    channel_id: string | null;
    bot_id: string;
    root_post_id: string | null;
    turn_count: number;
    update_at: number;
};

test.beforeAll(async () => {
    test.setTimeout(180000);
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.beforeEach(async () => {
    await openAIMock.resetMocks();
    await resetSelectedAgentPreference(mattermost, username, password);
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

function messageText(message: OpenAIChatMessage): string {
    if (typeof message.content === 'string') {
        return message.content;
    }
    if (Array.isArray(message.content)) {
        return message.content.map((part) => part.text ?? '').join('');
    }
    return '';
}

function isChatCompletionRequest(value: unknown): value is OpenAIChatCompletionRequest {
    return typeof value === 'object' && value !== null && Array.isArray((value as OpenAIChatCompletionRequest).messages);
}

function escapeRegExp(value: string): string {
    return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function orderedMessagesPattern(messages: string[]): string {
    return `(?s)${messages.map(escapeRegExp).join('.*')}`;
}

function leakedContextPattern(followUpMessage: string, staleMessages: string[]): string {
    const followUp = escapeRegExp(followUpMessage);
    const stale = staleMessages.map(escapeRegExp).join('|');
    return `(?s)(${followUp}.*(?:${stale})|(?:${stale}).*${followUp})`;
}

async function setupTestPage(page: Page): Promise<AIPlugin> {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);

    await mmPage.login(mattermost.url(), username, password);
    await aiPlugin.openRHS();

    return aiPlugin;
}

async function selectAgent(page: Page, aiPlugin: AIPlugin, displayName: string): Promise<void> {
    const selector = aiPlugin.getRhsContainer().getByTestId('bot-selector-rhs');
    await expect(selector).toBeVisible({timeout: 30000});
    await selector.click();

    const menu = page.getByTestId('dropdownmenu').filter({hasText: 'Choose an Agent'});
    await expect(menu).toBeVisible();
    await menu.getByRole('button', {name: displayName, exact: true}).click();

    await expect(menu).not.toBeVisible();
    await expect(selector).toHaveAttribute('title', displayName);
    await expect(selector).toContainText(displayName);
}

async function expectProviderJourney(userMessage: string, expectedPath: RegExp): Promise<void> {
    let calls: Array<{path: string; request: OpenAIChatCompletionRequest}> = [];

    await expect.poll(async () => {
        calls = (await openAIMock.getHistory()).flatMap((entry) => {
            if (!isChatCompletionRequest(entry.request.body)) {
                return [];
            }
            const requestText = (entry.request.body.messages ?? []).map(messageText).join('\n');
            if (!requestText.includes(userMessage)) {
                return [];
            }
            return [{
                path: entry.request.path ?? '',
                request: entry.request.body,
            }];
        });
        return calls.length;
    }, {
        message: `provider did not receive the completion and title requests for ${userMessage}`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).toBe(2);

    expect(calls).toHaveLength(2);
    expect(calls.every((call) => expectedPath.test(call.path))).toBe(true);
    expect(calls.filter((call) => (call.request.messages ?? []).some((message) => (
        message.role === 'user' && messageText(message) === userMessage
    )))).toHaveLength(1);
    expect(calls.filter((call) => (call.request.messages ?? []).some((message) => (
        message.role === 'user' && messageText(message) === `${titlePrompt}\n${userMessage}`
    )))).toHaveLength(1);
}

async function waitForTitledThreads(
    routes: PluginRoutesApi,
    token: string,
    expectedTitles: string[],
): Promise<AIThreadSummary[]> {
    let threads: AIThreadSummary[] = [];

    await expect.poll(async () => {
        const response = await routes.getJson('ai_threads', token);
        if (!Array.isArray(response)) {
            throw new Error('ai_threads did not return an array');
        }
        threads = response as AIThreadSummary[];
        return expectedTitles.every((title) => threads.filter((thread) => thread.title === title).length === 1);
    }, {
        message: `ai_threads did not persist exact generated titles: ${expectedTitles.join(', ')}`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).toBe(true);

    return expectedTitles.map((title) => {
        const thread = threads.find((candidate) => candidate.title === title);
        if (!thread) {
            throw new Error(`ai_threads lost the synchronized title ${title}`);
        }
        return thread;
    });
}

async function expectFollowUpProviderRequest(
    followUpMessage: string,
    expectedPath: RegExp,
    requiredContext: string[],
    excludedContext: string[],
): Promise<void> {
    let calls: Array<{path: string; request: OpenAIChatCompletionRequest}> = [];

    await expect.poll(async () => {
        calls = (await openAIMock.getHistory()).flatMap((entry) => {
            if (!isChatCompletionRequest(entry.request.body)) {
                return [];
            }
            const hasExactFollowUp = (entry.request.body.messages ?? []).some((message) => (
                message.role === 'user' && messageText(message) === followUpMessage
            ));
            if (!hasExactFollowUp) {
                return [];
            }
            return [{
                path: entry.request.path ?? '',
                request: entry.request.body,
            }];
        });
        return calls.length;
    }, {
        message: `provider did not receive exactly one follow-up request for ${followUpMessage}`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).toBe(1);

    const call = calls[0];
    expect(call.path).toMatch(expectedPath);
    const requestText = (call.request.messages ?? []).map(messageText).join('\n');
    for (const requiredMessage of requiredContext) {
        expect(requestText).toContain(requiredMessage);
    }
    for (const excludedMessage of excludedContext) {
        expect(requestText).not.toContain(excludedMessage);
    }
}

async function expectHistoryEntry(
    aiPlugin: AIPlugin,
    thread: AIThreadSummary,
    agentLabel: string,
): Promise<void> {
    const entry = aiPlugin.threadsListContainer.getByTestId(`rhs-thread-${thread.id}`);
    await expect(entry).toBeVisible({timeout: 30000});
    await expect(entry.getByText(thread.title, {exact: true})).toBeVisible();
    await expect(entry.getByText(agentLabel, {exact: true})).toBeVisible();
}

async function expectConversation(
    aiPlugin: AIPlugin,
    expectedMessages: string[],
    staleMessages: string[],
): Promise<void> {
    const rhs = aiPlugin.getRhsContainer();
    for (const expectedMessage of expectedMessages) {
        await expect(rhs.getByText(expectedMessage, {exact: true})).toBeVisible({timeout: 30000});
    }
    for (const staleMessage of staleMessages) {
        await expect(rhs.getByText(staleMessage, {exact: true})).toHaveCount(0);
    }
}

async function sendFollowUp(
    aiPlugin: AIPlugin,
    followUpMessage: string,
    expectedResponse: string,
    trapResponse: string,
): Promise<void> {
    await aiPlugin.sendMessage(followUpMessage);

    const rhs = aiPlugin.getRhsContainer();
    const expected = rhs.getByText(expectedResponse, {exact: true});
    const trap = rhs.getByText(trapResponse, {exact: true});
    await expect(expected.or(trap)).toBeVisible({timeout: 30000});
    await expect(expected).toBeVisible();
    await expect(trap).toHaveCount(0);
    await aiPlugin.waitForBotResponse(expectedResponse);
}

async function resumeConversation(
    aiPlugin: AIPlugin,
    thread: AIThreadSummary,
    expectedMessages: string[],
    staleMessages: string[],
): Promise<void> {
    await aiPlugin.threadsListContainer.getByTestId(`rhs-thread-${thread.id}`).click();
    await expectConversation(aiPlugin, expectedMessages, staleMessages);
}

test.describe('Multiple bot conversation history', () => {
    test('keeps new conversations with the same agent isolated and resumable', async ({page}) => {
        test.setTimeout(180000);

        const marker = `SAME_AGENT_${Date.now().toString(36)}`;
        const firstUserMessage = `First independent request ${marker}`;
        const firstAssistantMessage = `First independent answer ${marker}`;
        const firstTitle = `First same-agent chat ${marker}`;
        const secondUserMessage = `Second independent request ${marker}`;
        const secondAssistantMessage = `Second independent answer ${marker}`;
        const secondTitle = `Second same-agent chat ${marker}`;
        const followUpMessage = `Continue only the first conversation ${marker}`;
        const followUpResponse = `First conversation continued ${marker}`;
        const leakedContextTrap = `WRONG same-agent context leaked ${marker}`;

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(firstAssistantMessage), {
                bodyContains: firstUserMessage,
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(secondAssistantMessage), {
                bodyContains: secondUserMessage,
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(followUpResponse), {
                bodyMatches: orderedMessagesPattern([
                    firstUserMessage,
                    firstAssistantMessage,
                    followUpMessage,
                ]),
                times: 1,
            }),
            // Narrow title rules must follow their broad initial completion rules:
            // Smocker gives the last matching registration priority.
            buildTitleMockRule(firstTitle, firstUserMessage),
            buildTitleMockRule(secondTitle, secondUserMessage),
            // Register the leak trap last so it overrides the valid follow-up rule.
            buildChatCompletionMockRule(buildTextResponse(leakedContextTrap), {
                bodyMatches: leakedContextPattern(
                    followUpMessage,
                    [secondUserMessage, secondAssistantMessage],
                ),
                times: 1,
            }),
        ]);

        const userClient = await mattermost.getClient(username, password);
        const mockBot = await userClient.getUserByUsername(mockBotUsername);
        const routes = mattermostAIPluginRoutes(mattermost.url());
        const aiPlugin = await setupTestPage(page);
        await selectAgent(page, aiPlugin, mockBotDisplayName);
        await aiPlugin.sendMessage(firstUserMessage);
        await aiPlugin.waitForBotResponse(firstAssistantMessage);

        await page.getByTestId('new-chat').click();
        await expect(aiPlugin.rhsPostTextarea).toHaveValue('');
        const selector = aiPlugin.getRhsContainer().getByTestId('bot-selector-rhs');
        await expect(selector).toHaveAttribute('title', mockBotDisplayName);
        await expect(selector).toContainText(mockBotDisplayName);
        await aiPlugin.sendMessage(secondUserMessage);
        await aiPlugin.waitForBotResponse(secondAssistantMessage);

        const [firstThread, secondThread] = await waitForTitledThreads(
            routes,
            userClient.getToken(),
            [firstTitle, secondTitle],
        );
        expect(firstThread.id).not.toBe(secondThread.id);
        expect(firstThread.bot_id).toBe(mockBot.id);
        expect(secondThread.bot_id).toBe(mockBot.id);

        await expectProviderJourney(firstUserMessage, defaultProviderPath);
        await expectProviderJourney(secondUserMessage, defaultProviderPath);

        await aiPlugin.openChatHistory();
        await expectHistoryEntry(aiPlugin, firstThread, mockBotDisplayName);
        await expectHistoryEntry(aiPlugin, secondThread, mockBotDisplayName);

        await resumeConversation(
            aiPlugin,
            firstThread,
            [firstUserMessage, firstAssistantMessage],
            [secondUserMessage, secondAssistantMessage],
        );
        await sendFollowUp(aiPlugin, followUpMessage, followUpResponse, leakedContextTrap);
        await expectFollowUpProviderRequest(
            followUpMessage,
            defaultProviderPath,
            [firstUserMessage, firstAssistantMessage],
            [secondUserMessage, secondAssistantMessage],
        );

        await aiPlugin.openChatHistory();
        await expectHistoryEntry(aiPlugin, firstThread, mockBotDisplayName);
        await resumeConversation(
            aiPlugin,
            firstThread,
            [firstUserMessage, firstAssistantMessage, followUpMessage, followUpResponse],
            [secondUserMessage, secondAssistantMessage, leakedContextTrap],
        );
        await aiPlugin.openChatHistory();
        await resumeConversation(
            aiPlugin,
            secondThread,
            [secondUserMessage, secondAssistantMessage],
            [firstUserMessage, firstAssistantMessage, followUpMessage, followUpResponse],
        );
    });

    test('switches agents and restores each conversation without stale content', async ({page}) => {
        test.setTimeout(180000);

        const marker = `CROSS_AGENT_${Date.now().toString(36)}`;
        const mockUserMessage = `Mock-agent request ${marker}`;
        const mockAssistantMessage = `Mock-agent answer ${marker}`;
        const mockTitle = `Mock route ${marker}`;
        const secondUserMessage = `Second-agent request ${marker}`;
        const secondAssistantMessage = `Second-agent answer ${marker}`;
        const secondTitle = `Second route ${marker}`;
        const mockFollowUpMessage = `Continue only the Mock conversation ${marker}`;
        const mockFollowUpResponse = `Mock conversation continued ${marker}`;
        const leakedContextTrap = `WRONG cross-agent context leaked ${marker}`;

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildTextResponse(mockAssistantMessage), {
                bodyContains: mockUserMessage,
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(secondAssistantMessage), {
                bodyContains: secondUserMessage,
                botPrefix: secondBotUsername,
                times: 1,
            }),
            buildChatCompletionMockRule(buildTextResponse(mockFollowUpResponse), {
                bodyMatches: orderedMessagesPattern([
                    mockUserMessage,
                    mockAssistantMessage,
                    mockFollowUpMessage,
                ]),
                times: 1,
            }),
            // Narrow title rules follow broad completion rules so they win only
            // for title generation on the matching provider path.
            buildTitleMockRule(mockTitle, mockUserMessage),
            buildTitleMockRule(secondTitle, secondUserMessage, secondBotUsername),
            // Highest priority: fail visibly if the Second Bot conversation
            // contaminates the restored Mock Bot follow-up.
            buildChatCompletionMockRule(buildTextResponse(leakedContextTrap), {
                bodyMatches: leakedContextPattern(
                    mockFollowUpMessage,
                    [secondUserMessage, secondAssistantMessage],
                ),
                times: 1,
            }),
        ]);

        const userClient = await mattermost.getClient(username, password);
        const routes = mattermostAIPluginRoutes(mattermost.url());
        const user = await userClient.getMe();
        const mockBot = await userClient.getUserByUsername(mockBotUsername);
        const secondBot = await userClient.getUserByUsername(secondBotUsername);
        expect(await selectedAgentPreference(userClient, user.id)).toBe('');

        const aiPlugin = await setupTestPage(page);
        await selectAgent(page, aiPlugin, mockBotDisplayName);
        await expectSelectedAgentPreference(userClient, user.id, mockBot.id);
        await aiPlugin.sendMessage(mockUserMessage);
        await aiPlugin.waitForBotResponse(mockAssistantMessage);

        await page.getByTestId('new-chat').click();
        await selectAgent(page, aiPlugin, secondBotDisplayName);
        await expectSelectedAgentPreference(userClient, user.id, secondBot.id);
        await aiPlugin.sendMessage(secondUserMessage);
        await aiPlugin.waitForBotResponse(secondAssistantMessage);

        const [mockThread, secondThread] = await waitForTitledThreads(
            routes,
            userClient.getToken(),
            [mockTitle, secondTitle],
        );
        expect(mockThread.id).not.toBe(secondThread.id);
        expect(mockThread.bot_id).toBe(mockBot.id);
        expect(secondThread.bot_id).toBe(secondBot.id);

        await expectProviderJourney(mockUserMessage, defaultProviderPath);
        await expectProviderJourney(secondUserMessage, secondProviderPath);

        await aiPlugin.openChatHistory();
        await expectHistoryEntry(aiPlugin, mockThread, mockBotDisplayName);
        await expectHistoryEntry(aiPlugin, secondThread, secondBotDisplayName);

        await resumeConversation(
            aiPlugin,
            secondThread,
            [secondUserMessage, secondAssistantMessage],
            [mockUserMessage, mockAssistantMessage],
        );
        await aiPlugin.openChatHistory();
        await resumeConversation(
            aiPlugin,
            mockThread,
            [mockUserMessage, mockAssistantMessage],
            [secondUserMessage, secondAssistantMessage],
        );
        await sendFollowUp(aiPlugin, mockFollowUpMessage, mockFollowUpResponse, leakedContextTrap);
        await expectFollowUpProviderRequest(
            mockFollowUpMessage,
            defaultProviderPath,
            [mockUserMessage, mockAssistantMessage],
            [secondUserMessage, secondAssistantMessage],
        );
        await expectSelectedAgentPreference(userClient, user.id, secondBot.id);

        // Selecting a Mock Bot history item must not replace the explicit
        // Second Bot preference used for the next conversation.
        await page.getByTestId('new-chat').click();
        const selectorBeforeReload = aiPlugin.getRhsContainer().getByTestId('bot-selector-rhs');
        await expect(selectorBeforeReload).toBeVisible();
        await expect(selectorBeforeReload).toHaveAttribute('title', secondBotDisplayName);
        await expect(selectorBeforeReload).toContainText(secondBotDisplayName);
        await expectSelectedAgentPreference(userClient, user.id, secondBot.id);

        // Reload independently proves the server preference survives a new
        // client session rather than only the current Redux state.
        await page.reload({waitUntil: 'domcontentloaded'});
        await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 60000});
        await aiPlugin.openRHS();

        const reloadedSelector = aiPlugin.getRhsContainer().getByTestId('bot-selector-rhs');
        await expect(reloadedSelector).toBeVisible({timeout: 30000});
        await expect(reloadedSelector).toHaveAttribute('title', secondBotDisplayName);
        await expect(reloadedSelector).toContainText(secondBotDisplayName);
        await expectSelectedAgentPreference(userClient, user.id, secondBot.id);

        await aiPlugin.openChatHistory();
        await expectHistoryEntry(aiPlugin, mockThread, mockBotDisplayName);
        await expectHistoryEntry(aiPlugin, secondThread, secondBotDisplayName);
        await resumeConversation(
            aiPlugin,
            mockThread,
            [mockUserMessage, mockAssistantMessage, mockFollowUpMessage, mockFollowUpResponse],
            [secondUserMessage, secondAssistantMessage, leakedContextTrap],
        );
        await aiPlugin.openChatHistory();
        await resumeConversation(
            aiPlugin,
            secondThread,
            [secondUserMessage, secondAssistantMessage],
            [mockUserMessage, mockAssistantMessage, mockFollowUpMessage, mockFollowUpResponse],
        );
    });
});
