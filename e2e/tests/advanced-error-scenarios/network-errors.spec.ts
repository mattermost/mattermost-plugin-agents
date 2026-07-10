// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {expect, test} from '@playwright/test';
import type {Page} from '@playwright/test';
import type {Client4} from '@mattermost/client';
import type {Post} from '@mattermost/types/posts';

import {AIPlugin} from 'helpers/ai-plugin';
import {MattermostPage} from 'helpers/mm';
import MattermostContainer from 'helpers/mmcontainer';
import {
    buildChatCompletionMockRule,
    OpenAIMockContainer,
    RunOpenAIMocks,
} from 'helpers/openai-mock';
import type {OpenAIChatCompletionRequest, OpenAIChatMessage} from 'helpers/openai-mock';
import {mattermostAIPluginRoutes, PluginRoutesApi} from 'helpers/plugin-http';
import RunContainer from 'helpers/plugincontainer';

const username = 'regularuser';
const password = 'regularuser';
const botUsername = 'mock';
const providerPath = /^\/(?:v1\/)?chat\/completions$/;
const terminalError = 'Sorry! An error occurred while accessing the LLM. See server logs for details.';
const titlePrompt = 'Write a short title for the following request. Include only the title and nothing else, no quotations. Request:';

let mattermost: MattermostContainer;
let openAIMock: OpenAIMockContainer;

type ProviderRequest = {
    path: string;
    body: OpenAIChatCompletionRequest;
};

type PersistedExchange = {
    conversationID: string;
    rootPostID: string;
};

type PersistedConversation = {
    turns: Array<{
        role: string;
        content: Array<{
            type: string;
            text?: string;
        }>;
    }>;
};

type AIThreadSummary = {
    id: string;
    title: string;
};

type ExpectedTurn = {
    role: string;
    text: string;
};

type ProviderJourney = {
    mainRequests: ProviderRequest[];
    titleRequests: ProviderRequest[];
};

type ProviderJourneyExpectation = {
    exactMainRequests?: number;
    minimumMainRequests?: number;
    expectsTitle: boolean;
};

type ConversationHarness = {
    userClient: Client4;
    botID: string;
    channelID: string;
    routes: PluginRoutesApi;
    aiPlugin: AIPlugin;
};

test.beforeAll(async () => {
    test.setTimeout(180000);
    mattermost = await RunContainer();
    openAIMock = await RunOpenAIMocks(mattermost.network);
});

test.beforeEach(async () => {
    await openAIMock.resetMocks();
});

test.afterAll(async () => {
    await openAIMock.stop();
    await mattermost.stop();
});

function buildExactTextResponse(text: string): string {
    return [
        'data: {"id":"chatcmpl-error-recovery","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}',
        `data: {"id":"chatcmpl-error-recovery","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":${JSON.stringify(text)}},"finish_reason":null}]}`,
        'data: {"id":"chatcmpl-error-recovery","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}',
        'data: [DONE]',
    ].join('\n\n') + '\n\n';
}

function buildProviderErrorRule(
    statusCode: number,
    providerMessage: string,
    errorType: string,
    bodyMatchers: Record<string, unknown>,
    times: number,
) {
    const rule = buildChatCompletionMockRule('', {times});
    rule.request.body = bodyMatchers;
    rule.response = {
        status: statusCode,
        headers: {
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({
            error: {
                message: providerMessage,
                type: errorType,
            },
        }),
    };
    return rule;
}

function buildTitleRule(title: string, prompt: string) {
    return buildChatCompletionMockRule(buildExactTextResponse(title), {
        bodyContains: `${titlePrompt}\\n${prompt}`,
        times: 1,
    });
}

function withHighestPriorityTitle(mainRules: any[], title: string, prompt: string): any[] {
    // Smocker gives the last matching rule priority. Title requests include the
    // original prompt, so their narrow rule must follow every main-request rule.
    return [...mainRules, buildTitleRule(title, prompt)];
}

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
    return typeof value === 'object' &&
        value !== null &&
        Array.isArray((value as OpenAIChatCompletionRequest).messages);
}

function finalUserMessage(request: OpenAIChatCompletionRequest): string {
    const userMessages = (request.messages ?? []).filter((message) => message.role === 'user');
    if (userMessages.length === 0) {
        return '';
    }
    return messageText(userMessages[userMessages.length - 1]);
}

function requestContainsText(request: OpenAIChatCompletionRequest, text: string): boolean {
    return (request.messages ?? []).some((message) => messageText(message).includes(text));
}

function isTitleRequest(request: OpenAIChatCompletionRequest, prompt: string): boolean {
    return (request.messages ?? []).some((message) => (
        message.role === 'user' &&
        messageText(message) === `${titlePrompt}\n${prompt}`
    ));
}

function escapeRegex(value: string): string {
    return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function messageObjectPattern(role: string, text: string): string {
    return [
        '\\{[^{}]*',
        `"role"\\s*:\\s*${escapeRegex(JSON.stringify(role))}`,
        '[^{}]*',
        `"content"\\s*:\\s*${escapeRegex(JSON.stringify(text))}`,
        '[^{}]*\\}',
    ].join('');
}

function orderedTrailingContextPattern(messages: ExpectedTurn[]): string {
    const suffix = messages.
        map((message) => messageObjectPattern(message.role, message.text)).
        join('\\s*,\\s*');
    return `(?s)"messages"\\s*:\\s*\\[.*${suffix}\\s*\\]`;
}

function postsFrom(response: {posts: Record<string, Post>}): Post[] {
    return Object.values(response.posts);
}

async function setupTestPage(page: Page): Promise<AIPlugin> {
    const mmPage = new MattermostPage(page);
    const aiPlugin = new AIPlugin(page);

    await mmPage.login(mattermost.url(), username, password);
    await aiPlugin.openRHS();

    return aiPlugin;
}

async function setupConversationHarness(page: Page): Promise<ConversationHarness> {
    const userClient = await mattermost.getClient(username, password);
    const user = await userClient.getMe();
    const bot = await userClient.getUserByUsername(botUsername);
    const channel = await userClient.createDirectChannel([user.id, bot.id]);
    const routes = mattermostAIPluginRoutes(mattermost.url());
    const aiPlugin = await setupTestPage(page);

    return {
        userClient,
        botID: bot.id,
        channelID: channel.id,
        routes,
        aiPlugin,
    };
}

async function expectProviderJourney(
    prompt: string,
    expectation: ProviderJourneyExpectation,
): Promise<ProviderJourney> {
    let mainRequests: ProviderRequest[] = [];
    let titleRequests: ProviderRequest[] = [];
    let unmatchedRequests: ProviderRequest[] = [];

    await expect.poll(async () => {
        const promptRequests = (await openAIMock.getHistory()).flatMap((entry) => {
            if (!isChatCompletionRequest(entry.request.body)) {
                return [];
            }
            if (typeof entry.request.path !== 'string' || !providerPath.test(entry.request.path)) {
                return [];
            }
            if (!requestContainsText(entry.request.body, prompt)) {
                return [];
            }
            return [{
                path: entry.request.path,
                body: entry.request.body,
            }];
        });

        mainRequests = promptRequests.filter((request) => finalUserMessage(request.body) === prompt);
        titleRequests = promptRequests.filter((request) => isTitleRequest(request.body, prompt));
        unmatchedRequests = promptRequests.filter((request) => (
            finalUserMessage(request.body) !== prompt &&
            !isTitleRequest(request.body, prompt)
        ));

        const mainCountMatches = expectation.exactMainRequests === undefined ?
            mainRequests.length >= (expectation.minimumMainRequests ?? 1) :
            mainRequests.length === expectation.exactMainRequests;
        const titleCountMatches = titleRequests.length === (expectation.expectsTitle ? 1 : 0);
        return mainCountMatches && titleCountMatches && unmatchedRequests.length === 0;
    }, {
        message: `provider main/title request accounting did not settle for ${prompt}`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).toBe(true);

    expect(unmatchedRequests).toHaveLength(0);
    if (expectation.exactMainRequests !== undefined) {
        expect(mainRequests).toHaveLength(expectation.exactMainRequests);
    } else {
        expect(mainRequests.length).toBeGreaterThanOrEqual(expectation.minimumMainRequests ?? 1);
    }
    expect(titleRequests).toHaveLength(expectation.expectsTitle ? 1 : 0);
    expect([...mainRequests, ...titleRequests].every((request) => providerPath.test(request.path))).toBe(true);

    return {mainRequests, titleRequests};
}

async function waitForPersistedExchange(
    client: Client4,
    channelID: string,
    botID: string,
    prompt: string,
    responseText: string,
): Promise<PersistedExchange> {
    let userPost: Post | undefined;
    let responsePost: Post | undefined;

    await expect.poll(async () => {
        const posts = postsFrom(await client.getPosts(channelID, 0, 200));
        userPost = posts.find((post) => post.user_id !== botID && post.message === prompt);
        const expectedRootID = userPost?.root_id || userPost?.id;
        responsePost = userPost ? posts.find((post) => (
            post.user_id === botID &&
            post.message === responseText &&
            post.root_id === expectedRootID
        )) : undefined;
        return responsePost?.id ?? '';
    }, {
        message: `exact provider outcome for ${prompt} did not persist`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).not.toBe('');

    const conversationID = responsePost?.props?.conversation_id;
    if (
        !userPost ||
        !responsePost ||
        typeof conversationID !== 'string' ||
        conversationID === ''
    ) {
        throw new Error(`persisted provider outcome for ${prompt} was incomplete`);
    }

    return {
        conversationID,
        rootPostID: responsePost.root_id,
    };
}

function conversationTurns(conversation: PersistedConversation): ExpectedTurn[] {
    return conversation.turns.map((turn) => ({
        role: turn.role,
        text: turn.content.
            filter((block) => block.type === 'text').
            map((block) => block.text ?? '').
            join(''),
    }));
}

async function expectPersistedTurns(
    routes: PluginRoutesApi,
    token: string,
    conversationID: string,
    expectedTurns: ExpectedTurn[],
): Promise<void> {
    await expect.poll(async () => {
        const conversation = await routes.getJson(`conversations/${conversationID}`, token) as PersistedConversation;
        return conversationTurns(conversation);
    }, {
        message: `conversation ${conversationID} did not persist its terminal turns`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).toEqual(expectedTurns);
}

async function expectPersistedTitle(
    routes: PluginRoutesApi,
    token: string,
    conversationID: string,
    expectedTitle: string,
): Promise<AIThreadSummary> {
    let thread: AIThreadSummary | undefined;

    await expect.poll(async () => {
        const response = await routes.getJson('ai_threads', token);
        if (!Array.isArray(response)) {
            throw new Error('ai_threads did not return an array');
        }

        const matchingThreads = (response as AIThreadSummary[]).filter((candidate) => candidate.id === conversationID);
        thread = matchingThreads.length === 1 ? matchingThreads[0] : undefined;
        return thread?.title;
    }, {
        message: `conversation ${conversationID} did not persist exact title ${expectedTitle}`,
        timeout: 30000,
        intervals: [250, 500, 1000],
    }).toBe(expectedTitle);

    if (!thread) {
        throw new Error(`ai_threads lost conversation ${conversationID}`);
    }
    return thread;
}

async function expectTerminalUI(
    aiPlugin: AIPlugin,
    providerMessage: string,
): Promise<void> {
    const rhs = aiPlugin.getRhsContainer();
    const errorPost = rhs.getByTestId('llm-bot-post').filter({hasText: terminalError});

    await expect(errorPost).toHaveCount(1, {timeout: 30000});
    await expect(errorPost.getByText(terminalError, {exact: true})).toBeVisible();
    await expect(rhs.getByText(providerMessage, {exact: true})).toHaveCount(0);
    await expect(rhs.getByTestId('stop-generating-button')).not.toBeVisible();
    await expect(rhs.getByText('Starting...', {exact: true})).not.toBeVisible();
    await expect(aiPlugin.rhsPostTextarea).toBeVisible();
    await expect(aiPlugin.rhsPostTextarea).toBeEnabled();
}

async function expectSuccessfulUI(
    aiPlugin: AIPlugin,
    responseText: string,
    unexpectedMessages: string[],
): Promise<void> {
    const rhs = aiPlugin.getRhsContainer();
    const successPost = rhs.getByTestId('llm-bot-post').filter({hasText: responseText});

    await expect(successPost).toHaveCount(1, {timeout: 30000});
    await expect(successPost.getByText(responseText, {exact: true})).toBeVisible();
    await expect(successPost.getByText(terminalError, {exact: true})).toHaveCount(0);
    for (const unexpectedMessage of unexpectedMessages) {
        await expect(rhs.getByText(unexpectedMessage, {exact: true})).toHaveCount(0);
    }
    await expect(rhs.getByTestId('stop-generating-button')).not.toBeVisible();
    await expect(rhs.getByText('Starting...', {exact: true})).not.toBeVisible();
    await expect(aiPlugin.rhsPostTextarea).toBeEnabled();
}

async function expectPersistedConversation(
    harness: ConversationHarness,
    prompt: string,
    responseText: string,
    expectedTurns: ExpectedTurn[],
    expectedTitle: string,
): Promise<{exchange: PersistedExchange; thread: AIThreadSummary}> {
    const exchange = await waitForPersistedExchange(
        harness.userClient,
        harness.channelID,
        harness.botID,
        prompt,
        responseText,
    );
    await expectPersistedTurns(
        harness.routes,
        harness.userClient.getToken(),
        exchange.conversationID,
        expectedTurns,
    );
    const thread = await expectPersistedTitle(
        harness.routes,
        harness.userClient.getToken(),
        exchange.conversationID,
        expectedTitle,
    );
    return {exchange, thread};
}

async function expectRestoredConversation(
    page: Page,
    aiPlugin: AIPlugin,
    thread: AIThreadSummary,
    expectedMessages: string[],
): Promise<void> {
    await page.reload({waitUntil: 'domcontentloaded'});
    await expect(page.getByTestId('channel_view')).toBeVisible({timeout: 60000});
    await aiPlugin.openRHS();
    await aiPlugin.openChatHistory();

    const historyItem = aiPlugin.threadsListContainer.getByTestId(`rhs-thread-${thread.id}`);
    await expect(historyItem).toBeVisible({timeout: 30000});
    await expect(historyItem.getByText(thread.title, {exact: true})).toBeVisible();
    await historyItem.click();

    const rhs = aiPlugin.getRhsContainer();
    for (const message of expectedMessages) {
        await expect(rhs.getByText(message, {exact: true})).toBeVisible({timeout: 30000});
    }
    await expect(rhs.getByTestId('stop-generating-button')).not.toBeVisible();
    await expect(aiPlugin.rhsPostTextarea).toBeEnabled();
}

test.describe('Advanced Error Scenarios - Network Errors', () => {
    // Future coverage gap: Smocker returns HTTP responses but cannot produce an
    // actual socket reset, transport timeout, or partial-stream interruption.
    // Those need a transport-fault fixture.
    test('persists an auth failure after exactly one main request', async ({page}) => {
        test.setTimeout(180000);

        const marker = Date.now().toString(36);
        const prompt = `Trigger deterministic auth failure ${marker}`;
        const providerMessage = `Invalid provider credentials ${marker}`;
        const title = `Auth failure ${marker}`;
        const harness = await setupConversationHarness(page);

        await openAIMock.addMocks(withHighestPriorityTitle([
            buildProviderErrorRule(
                401,
                providerMessage,
                'authentication_error',
                {'messages[1].content': prompt},
                10,
            ),
        ], title, prompt));

        await harness.aiPlugin.sendMessage(prompt);
        await expectTerminalUI(harness.aiPlugin, providerMessage);

        await expectProviderJourney(prompt, {
            exactMainRequests: 1,
            expectsTitle: true,
        });
        const {thread} = await expectPersistedConversation(
            harness,
            prompt,
            terminalError,
            [
                {role: 'user', text: prompt},
                {role: 'assistant', text: terminalError},
            ],
            title,
        );
        await expectRestoredConversation(page, harness.aiPlugin, thread, [prompt, terminalError]);
    });

    test('persists a terminal rate-limit failure after retrying', async ({page}) => {
        test.setTimeout(180000);

        const marker = Date.now().toString(36);
        const prompt = `Trigger deterministic rate-limit failure ${marker}`;
        const providerMessage = `Provider rate limit exceeded ${marker}`;
        const title = `Rate limit failure ${marker}`;
        const harness = await setupConversationHarness(page);

        await openAIMock.addMocks(withHighestPriorityTitle([
            buildProviderErrorRule(
                429,
                providerMessage,
                'rate_limit_error',
                {'messages[1].content': prompt},
                10,
            ),
        ], title, prompt));

        await harness.aiPlugin.sendMessage(prompt);
        await expectTerminalUI(harness.aiPlugin, providerMessage);

        await expectProviderJourney(prompt, {
            minimumMainRequests: 2,
            expectsTitle: true,
        });
        const {thread} = await expectPersistedConversation(
            harness,
            prompt,
            terminalError,
            [
                {role: 'user', text: prompt},
                {role: 'assistant', text: terminalError},
            ],
            title,
        );
        await expectRestoredConversation(page, harness.aiPlugin, thread, [prompt, terminalError]);
    });

    test('succeeds when a retryable request recovers on its third attempt', async ({page}) => {
        test.setTimeout(180000);

        const marker = Date.now().toString(36);
        const prompt = `Recover during provider retries ${marker}`;
        const retryMessage = `Transient provider failure ${marker}`;
        const response = `Retry eventually succeeded ${marker}`;
        const title = `Retry success ${marker}`;
        const harness = await setupConversationHarness(page);

        await openAIMock.addMocks(withHighestPriorityTitle([
            // Broad success is lower priority. The two-use error rule wins first,
            // then expires so the same main request can recover on its next retry.
            buildChatCompletionMockRule(buildExactTextResponse(response), {
                bodyContains: prompt,
                times: 1,
            }),
            buildProviderErrorRule(
                429,
                retryMessage,
                'rate_limit_error',
                {'messages[1].content': prompt},
                2,
            ),
        ], title, prompt));

        await harness.aiPlugin.sendMessage(prompt);
        await harness.aiPlugin.waitForBotResponse(response);
        await expectSuccessfulUI(harness.aiPlugin, response, [retryMessage, terminalError]);

        await expectProviderJourney(prompt, {
            minimumMainRequests: 3,
            expectsTitle: true,
        });
        const {thread} = await expectPersistedConversation(
            harness,
            prompt,
            response,
            [
                {role: 'user', text: prompt},
                {role: 'assistant', text: response},
            ],
            title,
        );
        await expectRestoredConversation(page, harness.aiPlugin, thread, [prompt, response]);
        await expect(harness.aiPlugin.getRhsContainer().getByText(terminalError, {exact: true})).toHaveCount(0);
    });

    test('recovers after a persisted 500 response in the same conversation', async ({page}) => {
        test.setTimeout(180000);

        const marker = Date.now().toString(36);
        const failurePrompt = `Trigger retried upstream 500 ${marker}`;
        const providerFailure = `Deterministic upstream failure ${marker}`;
        const recoveryPrompt = `Recover this exact conversation ${marker}`;
        const recoveryResponse = `Recovery completed successfully ${marker}`;
        const wrongContextResponse = `Recovery context was incomplete ${marker}`;
        const title = `Server recovery ${marker}`;
        const harness = await setupConversationHarness(page);

        await openAIMock.addMocks(withHighestPriorityTitle([
            buildProviderErrorRule(
                500,
                providerFailure,
                'api_error',
                {'messages[1].content': failurePrompt},
                10,
            ),
        ], title, failurePrompt));

        await harness.aiPlugin.sendMessage(failurePrompt);
        await expectTerminalUI(harness.aiPlugin, providerFailure);
        await expectProviderJourney(failurePrompt, {
            minimumMainRequests: 2,
            expectsTitle: true,
        });

        const {exchange: failedExchange, thread} = await expectPersistedConversation(
            harness,
            failurePrompt,
            terminalError,
            [
                {role: 'user', text: failurePrompt},
                {role: 'assistant', text: terminalError},
            ],
            title,
        );

        await openAIMock.addMocks([
            buildChatCompletionMockRule(buildExactTextResponse(wrongContextResponse), {
                bodyContains: recoveryPrompt,
                times: 1,
            }),
            // Smocker gives the last matching rule priority. The ordered-context
            // success must follow the prompt-only trap so only correct replay recovers.
            buildChatCompletionMockRule(buildExactTextResponse(recoveryResponse), {
                bodyMatches: orderedTrailingContextPattern([
                    {role: 'user', text: failurePrompt},
                    {role: 'assistant', text: terminalError},
                    {role: 'user', text: recoveryPrompt},
                ]),
                times: 1,
            }),
        ]);

        await harness.aiPlugin.sendMessage(recoveryPrompt);
        await harness.aiPlugin.waitForBotResponse(recoveryResponse);
        await expectSuccessfulUI(harness.aiPlugin, recoveryResponse, [wrongContextResponse]);

        const recoveryJourney = await expectProviderJourney(recoveryPrompt, {
            exactMainRequests: 1,
            expectsTitle: false,
        });
        const recoveryRequest = recoveryJourney.mainRequests[0];
        expect((recoveryRequest.body.messages ?? []).slice(-3).map((message) => ({
            role: message.role,
            text: messageText(message),
        }))).toEqual([
            {role: 'user', text: failurePrompt},
            {role: 'assistant', text: terminalError},
            {role: 'user', text: recoveryPrompt},
        ]);

        const recoveredExchange = await waitForPersistedExchange(
            harness.userClient,
            harness.channelID,
            harness.botID,
            recoveryPrompt,
            recoveryResponse,
        );
        expect(recoveredExchange.conversationID).toBe(failedExchange.conversationID);
        expect(recoveredExchange.rootPostID).toBe(failedExchange.rootPostID);
        await expectPersistedTurns(
            harness.routes,
            harness.userClient.getToken(),
            failedExchange.conversationID,
            [
                {role: 'user', text: failurePrompt},
                {role: 'assistant', text: terminalError},
                {role: 'user', text: recoveryPrompt},
                {role: 'assistant', text: recoveryResponse},
            ],
        );
        const recoveredThread = await expectPersistedTitle(
            harness.routes,
            harness.userClient.getToken(),
            failedExchange.conversationID,
            title,
        );
        expect(recoveredThread.id).toBe(thread.id);
        expect(recoveredThread.title).toBe(thread.title);

        await expectRestoredConversation(
            page,
            harness.aiPlugin,
            recoveredThread,
            [failurePrompt, terminalError, recoveryPrompt, recoveryResponse],
        );

        const restoredSuccessPost = harness.aiPlugin.getRhsContainer().
            getByTestId('llm-bot-post').
            filter({hasText: recoveryResponse});
        await expect(restoredSuccessPost.getByText(recoveryResponse, {exact: true})).toBeVisible();
        await expect(restoredSuccessPost.getByText(terminalError, {exact: true})).toHaveCount(0);
        await expect(harness.aiPlugin.getRhsContainer().getByTestId('stop-generating-button')).not.toBeVisible();
        await expect(harness.aiPlugin.rhsPostTextarea).toBeEnabled();
    });
});
