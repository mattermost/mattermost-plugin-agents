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

/** Semantic alias for Anthropic-real-API migrations; Phase 1 uses openaicompatible only. */
export function buildAnthropicThinkingResponse(options: {
    userMessage: string;
    reasoning: string;
    content: string;
    title?: string;
    chunkSize?: number;
}): AIMockFixtureFile {
    return buildReasoningResponse(options);
}

/**
 * Native URL citations via aimock webSearches. Verified in Phase 1 smoke against
 * Bifrost chat completions (useResponsesAPI: false). If annotations do not render,
 * later citation suites should use deterministic tool-call fallback instead.
 */
export type CitationSource = {
    url: string;
    title: string;
    startIndex?: number;
    endIndex?: number;
};

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

export function buildMultipleCitationResponse(options: {
    userMessage: string;
    toolCallId: string;
    searchQuery: string;
    content: string;
    citations: CitationSource[];
    title?: string;
}): AIMockFixtureFile {
    return buildWebSearchCitationSequence({
        userMessage: options.userMessage,
        toolCallId: options.toolCallId,
        searchQuery: options.searchQuery,
        content: options.content,
        title: options.title,
    });
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

export function buildToolCallAndTextResponse(options: {
    userMessage: string;
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
        match: { userMessage: options.userMessage, hasToolResult: false },
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

export function buildMultiTurnToolSequence(
    rounds: Array<{
        userMessage: string;
        toolCallId: string;
        toolName: string;
        toolArguments: Record<string, unknown>;
        finalContent: string;
        turnIndex?: number;
    }>,
): AIMockFixtureFile {
    const fixtures: AIMockFixture[] = [];

    for (const round of rounds) {
        fixtures.push({
            match: { toolCallId: round.toolCallId },
            response: { content: round.finalContent },
        });

        fixtures.push({
            match: {
                userMessage: round.userMessage,
                hasToolResult: false,
                ...(round.turnIndex !== undefined ? { turnIndex: round.turnIndex } : {}),
            },
            response: {
                toolCalls: [
                    {
                        id: round.toolCallId,
                        name: round.toolName,
                        arguments: round.toolArguments,
                    },
                ],
                finishReason: 'tool_calls',
            },
        });
    }

    return wrapFixtures(fixtures);
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
