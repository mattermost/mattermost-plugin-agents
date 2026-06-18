import type {Page} from '@playwright/test';

export type AIMockMatch = {
    userMessage?: string;
    inputText?: string;
    toolCallId?: string;
    toolName?: string;
    model?: string;
    responseFormat?: string;
    turnIndex?: number;
    hasToolResult?: boolean;
    endpoint?: 'chat' | 'embedding';
    context?: string;
};

export type AIMockToolCall = {
    id?: string;
    name: string;
    arguments: Record<string, unknown> | string;
};

export type AIMockResponse = {
    content?: string | null;
    role?: 'assistant';
    error?: { message: string; code?: string; type?: string };
    status?: number;
    reasoning?: string | { text: string; signature?: string };
    webSearches?: Array<{
        query?: string;
        results: Array<{ url: string; title?: string; snippet?: string }>;
    }>;
    toolCalls?: AIMockToolCall[];
    finishReason?: 'stop' | 'tool_calls' | 'length' | 'content_filter';
    id?: string;
    model?: string;
    usage?: Record<string, number>;
};

export type AIMockFixture = {
    match: AIMockMatch;
    response: AIMockResponse;
    latency?: number;
    chunkSize?: number;
    streamingProfile?: { ttft?: number; tps?: number; jitter?: number };
};

export type AIMockFixtureFile = {
    fixtures: AIMockFixture[];
};

export type AimockModelInfo = {
    id: string;
    displayName: string;
    inputTokenLimit?: number;
    outputTokenLimit?: number;
    contextLength?: number;
};

export const AIMOCK_COMPATIBLE_SERVICE = {
    id: 'aimock-service',
    name: 'Aimock Service',
    type: 'openaicompatible',
    apiKey: 'mock',
    apiURL: 'http://openai:8080',
    defaultModel: 'gpt-mock',
    tokenLimit: 16384,
    outputTokenLimit: 4096,
    streamingTimeoutSeconds: 30,
    useResponsesAPI: false,
} as const;

export const EMBEDDED_GET_CHANNEL_INFO_TOOL = 'mattermost__get_channel_info';
export const EMBEDDED_CREATE_POST_TOOL = 'mattermost__create_post';

export const TITLE_GENERATION_PROMPT_PREFIX =
    'Write a short title for the following request. Include only the title and nothing else, no quotations. Request:';

const DEFAULT_TITLE_CONTENT = 'Aimock E2E';

function wrapFixtures(fixtures: AIMockFixture[]): AIMockFixtureFile {
    return { fixtures };
}

export function buildTitleFixture(title: string = DEFAULT_TITLE_CONTENT): AIMockFixture {
    return {
        match: { userMessage: TITLE_GENERATION_PROMPT_PREFIX },
        response: { content: title },
    };
}

export function buildTextResponse(options: {
    userMessage: string;
    content: string;
    title?: string;
    chunkSize?: number;
    turnIndex?: number;
}): AIMockFixtureFile {
    const fixtures: AIMockFixture[] = [];

    if (options.title !== undefined) {
        fixtures.push(buildTitleFixture(options.title));
    }

    fixtures.push({
        match: {
            userMessage: options.userMessage,
            ...(options.turnIndex !== undefined ? { turnIndex: options.turnIndex } : {}),
        },
        response: {
            content: options.content,
        },
        ...(options.chunkSize !== undefined ? { chunkSize: options.chunkSize } : {}),
    });

    return wrapFixtures(fixtures);
}

export function buildReasoningResponse(options: {
    userMessage: string;
    reasoning: string;
    content: string;
    title?: string;
    chunkSize?: number;
}): AIMockFixtureFile {
    const fixtures: AIMockFixture[] = [];

    if (options.title !== undefined) {
        fixtures.push(buildTitleFixture(options.title));
    }

    fixtures.push({
        match: { userMessage: options.userMessage },
        response: {
            reasoning: options.reasoning,
            content: options.content,
        },
        ...(options.chunkSize !== undefined ? { chunkSize: options.chunkSize } : {}),
    });

    return wrapFixtures(fixtures);
}

export function buildTitleResponse(userMessage: string, title: string): AIMockFixture {
    return {
        match: { userMessage: `${TITLE_GENERATION_PROMPT_PREFIX} ${userMessage}` },
        response: { content: title },
    };
}

export function buildWebSearchCitationSequence(options: {
    userMessage: string;
    toolCallId: string;
    searchQuery: string;
    content: string;
    reasoning?: string;
    title?: string;
    chunkSize?: number;
}): AIMockFixtureFile {
    const fixtures: AIMockFixture[] = [
        {
            match: { toolCallId: options.toolCallId },
            response: {
                ...(options.reasoning !== undefined ? { reasoning: options.reasoning } : {}),
                content: options.content,
            },
            ...(options.chunkSize !== undefined ? { chunkSize: options.chunkSize } : {}),
        },
    ];

    if (options.title !== undefined) {
        fixtures.push(buildTitleResponse(options.userMessage, options.title));
    }

    fixtures.push({
        match: { userMessage: options.userMessage, hasToolResult: false },
        response: {
            toolCalls: [
                {
                    id: options.toolCallId,
                    name: 'WebSearch',
                    arguments: { query: options.searchQuery },
                },
            ],
            finishReason: 'tool_calls',
        },
    });

    return wrapFixtures(fixtures);
}

export function buildCombinedReasoningCitationResponse(options: {
    userMessage: string;
    toolCallId: string;
    searchQuery: string;
    reasoning: string;
    content: string;
    title?: string;
}): AIMockFixtureFile {
    return buildWebSearchCitationSequence({
        userMessage: options.userMessage,
        toolCallId: options.toolCallId,
        searchQuery: options.searchQuery,
        reasoning: options.reasoning,
        content: options.content,
        title: options.title,
    });
}

export function buildRegenerateCitationResponse(options: {
    userMessage: string;
    toolCallId: string;
    regenerateToolCallId: string;
    searchQuery: string;
    reasoning: string;
    content: string;
    title?: string;
}): AIMockFixtureFile {
    return mergeFixtureFiles(
        buildCombinedReasoningCitationResponse({
            userMessage: options.userMessage,
            toolCallId: options.toolCallId,
            searchQuery: options.searchQuery,
            reasoning: options.reasoning,
            content: options.content,
            title: options.title,
        }),
        buildWebSearchCitationSequence({
            userMessage: options.userMessage,
            toolCallId: options.regenerateToolCallId,
            searchQuery: options.searchQuery,
            reasoning: options.reasoning,
            content: options.content,
        }),
    );
}

/**
 * Native URL citations via aimock webSearches. Verified in Phase 1 smoke against
 * Bifrost chat completions (useResponsesAPI: false). If annotations do not render,
 * later citation suites should use deterministic tool-call fallback instead.
 */
export function buildCitationResponse(options: {
    userMessage: string;
    content: string;
    citations: Array<{ url: string; title: string; startIndex?: number; endIndex?: number }>;
    title?: string;
}): AIMockFixtureFile {
    const fixtures: AIMockFixture[] = [];

    if (options.title !== undefined) {
        fixtures.push(buildTitleFixture(options.title));
    }

    fixtures.push({
        match: { userMessage: options.userMessage },
        response: {
            content: options.content,
            webSearches: [
                {
                    query: options.citations[0]?.title ?? 'citation search',
                    results: options.citations.map((citation) => ({
                        url: citation.url,
                        title: citation.title,
                    })),
                },
            ],
        },
    });

    return wrapFixtures(fixtures);
}

function buildToolCallThenTextFile(options: {
    firstTurnMatch: Pick<AIMockMatch, 'userMessage' | 'toolName'>;
    toolCallId: string;
    toolName: string;
    toolArguments: Record<string, unknown>;
    finalContent: string;
    title?: string;
}): AIMockFixtureFile {
    const fixtures: AIMockFixture[] = [
        {
            match: { toolCallId: options.toolCallId },
            response: { content: options.finalContent },
        },
    ];

    if (options.title !== undefined) {
        fixtures.push(buildTitleFixture(options.title));
    }

    fixtures.push({
        match: { ...options.firstTurnMatch, hasToolResult: false },
        response: {
            toolCalls: [
                {
                    id: options.toolCallId,
                    name: options.toolName,
                    arguments: options.toolArguments,
                },
            ],
            finishReason: 'tool_calls',
        },
    });

    return wrapFixtures(fixtures);
}

export function buildToolCallAndTextResponse(options: {
    userMessage: string;
    toolCallId: string;
    toolName: string;
    toolArguments: Record<string, unknown>;
    finalContent: string;
    title?: string;
}): AIMockFixtureFile {
    return buildToolCallThenTextFile({
        firstTurnMatch: { userMessage: options.userMessage },
        toolCallId: options.toolCallId,
        toolName: options.toolName,
        toolArguments: options.toolArguments,
        finalContent: options.finalContent,
        title: options.title,
    });
}

/**
 * Two-turn tool fixture matched on the offered tool name rather than the user
 * message. Use when the model turn is driven by an available tool (e.g. channel
 * analysis binds bare `read_channel`) and there is no stable user message to match.
 */
export function buildToolNameAndTextResponse(options: {
    toolName: string;
    toolCallId: string;
    finalContent: string;
    toolArguments?: Record<string, unknown>;
    title?: string;
}): AIMockFixtureFile {
    return buildToolCallThenTextFile({
        firstTurnMatch: { toolName: options.toolName },
        toolCallId: options.toolCallId,
        toolName: options.toolName,
        toolArguments: options.toolArguments ?? {},
        finalContent: options.finalContent,
        title: options.title,
    });
}

export type MultiTurnToolSequenceOptions = {
    title?: string;
    userPromptMarker: string;
    steps: MultiTurnToolSequenceStep[];
};

export type ChainedToolCallStep = {
    toolCallId: string;
    toolName: string;
    args: Record<string, unknown>;
    matchAfterToolCallId?: string;
};

export type ChainedTextStep = {
    matchAfterToolCallId: string;
    text: string;
    hasToolResult?: boolean;
};

export type MultiTurnToolSequenceStep = ChainedToolCallStep | ChainedTextStep;

function isChainedTextStep(step: MultiTurnToolSequenceStep): step is ChainedTextStep {
    return 'text' in step;
}

/** Chains tool-call rounds keyed by prompt marker and prior toolCallId matches. */
export function buildMultiTurnToolSequence(
    options: MultiTurnToolSequenceOptions,
): AIMockFixtureFile {
    const fixtures: AIMockFixture[] = [];

    if (options.title !== undefined) {
        fixtures.push(buildTitleFixture(options.title));
    }

    for (let index = options.steps.length - 1; index >= 0; index--) {
        const step = options.steps[index];

        if (isChainedTextStep(step)) {
            fixtures.unshift({
                match: {
                    toolCallId: step.matchAfterToolCallId,
                    ...(step.hasToolResult ? {hasToolResult: true} : {}),
                },
                response: {content: step.text},
            });
            continue;
        }

        const match = step.matchAfterToolCallId
            ? {toolCallId: step.matchAfterToolCallId}
            : {userMessage: options.userPromptMarker, hasToolResult: false};

        fixtures.unshift({
            match,
            response: {
                toolCalls: [
                    {
                        id: step.toolCallId,
                        name: step.toolName,
                        arguments: step.args,
                    },
                ],
                finishReason: 'tool_calls',
            },
        });
    }

    return wrapFixtures(fixtures);
}

export function buildRejectAfterFirstToolSequence(options: {
    title?: string;
    userPromptMarker: string;
    toolCallId: string;
    toolName: string;
    toolArguments: Record<string, unknown>;
    finalContent: string;
}): AIMockFixtureFile {
    const sequence = buildMultiTurnToolSequence({
        title: options.title,
        userPromptMarker: options.userPromptMarker,
        steps: [
            {
                toolCallId: options.toolCallId,
                toolName: options.toolName,
                args: options.toolArguments,
            },
        ],
    });

    // If the plugin sends a continuation after rejection, match it by the rejected
    // tool call id alone so it works whether or not a tool-result marker is present.
    sequence.fixtures.push({
        match: {toolCallId: options.toolCallId},
        response: {content: options.finalContent},
    });

    return sequence;
}

export function buildPostToolSequence(options: {
    title?: string;
    userPromptMarker: string;
    infoCallId: string;
    createCallId: string;
    channelId: string;
    channelDisplayName: string;
    teamDisplayName: string;
    postText: string;
    finalText: string;
}): AIMockFixtureFile {
    return buildMultiTurnToolSequence({
        title: options.title,
        userPromptMarker: options.userPromptMarker,
        steps: [
            {
                toolCallId: options.infoCallId,
                toolName: EMBEDDED_GET_CHANNEL_INFO_TOOL,
                args: {channel_name: options.channelDisplayName},
            },
            {
                matchAfterToolCallId: options.infoCallId,
                toolCallId: options.createCallId,
                toolName: EMBEDDED_CREATE_POST_TOOL,
                args: {
                    channel_id: options.channelId,
                    channel_display_name: options.channelDisplayName,
                    team_display_name: options.teamDisplayName,
                    message: options.postText,
                },
            },
            {
                matchAfterToolCallId: options.createCallId,
                text: options.finalText,
            },
        ],
    });
}

const DEFAULT_AIMOCK_MODELS: AimockModelInfo[] = [
    {
        id: 'gpt-mock',
        displayName: 'gpt-mock',
        inputTokenLimit: 16384,
        outputTokenLimit: 4096,
        contextLength: 16384,
    },
];

export async function stubAimockModelFetch(
    page: Page,
    models: AimockModelInfo[] = DEFAULT_AIMOCK_MODELS,
): Promise<void> {
    const body = JSON.stringify(models);

    const fulfillModelsFetch = async (route: Parameters<Parameters<Page['route']>[1]>[0]) => {
        if (route.request().method() !== 'POST') {
            await route.continue();
            return;
        }

        await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body,
        });
    };

    await page.context().route(/\/plugins\/mattermost-ai\/admin\/models\/fetch(\?.*)?$/, fulfillModelsFetch);
    await page.context().route(/\/plugins\/mattermost-ai\/agents\/models\/fetch(\?.*)?$/, fulfillModelsFetch);
}

export function normalizeFixtureInput(
    input: AIMockFixtureFile | AIMockFixture[],
): AIMockFixtureFile {
    if (Array.isArray(input)) {
        return wrapFixtures(input);
    }

    return input;
}

export function mergeFixtureFiles(...files: AIMockFixtureFile[]): AIMockFixtureFile {
    return {
        fixtures: files.flatMap((file) => file.fixtures),
    };
}
