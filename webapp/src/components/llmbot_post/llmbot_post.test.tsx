// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import {useConversation} from '@/hooks/use_conversation';
import {PluginWebSocketMessage} from '@/types';
import type {ConversationResponse, Turn} from '@/types/conversation';

import {MAX_SEARCH_SOURCES} from '../search_sources';
import {ToolCallStatus} from '../tool_types';

import {LLMBotPost, PostUpdateWebsocketMessage} from './llmbot_post';
import {advanceAnimation} from './test_support';

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
}));

jest.mock('react-intl', () => {
    const ReactLocal = jest.requireActual('react') as typeof React;

    return {
        IntlProvider: ({children}: {children: React.ReactNode}) => ReactLocal.createElement(ReactLocal.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => ReactLocal.createElement(ReactLocal.Fragment, null, defaultMessage),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, unknown>) => {
                if (!values) {
                    return defaultMessage;
                }
                return defaultMessage.replace(/\{(\w+)\}/g, (match, key) => String(values[key] ?? match));
            },
        }),
    };
});

jest.mock('@/client', () => ({
    doPostbackSummary: jest.fn(),
    doRegenerate: jest.fn(),
    doStopGenerating: jest.fn(),
}));

jest.mock('@/hooks', () => ({
    useSelectNotAIPost: () => jest.fn(),
}));

jest.mock('@/hooks/use_conversation', () => ({
    invalidateConversation: jest.fn(),
    useConversation: jest.fn(),
}));

jest.mock('@/mm_webapp', () => ({
    PostMessagePreview: null,
}));

const mockPostTextRender = jest.fn<void, [string]>();
jest.mock('../post_text', () => {
    const ReactLocal = jest.requireActual('react') as typeof React;

    return {
        __esModule: true,
        default: ({message}: {message: string}) => {
            mockPostTextRender(message);
            return ReactLocal.createElement('div', null, message);
        },
    };
});

// Referenced lazily so the factory does not read it before initialization.
const mockNeedsViewerDecision = jest.fn<boolean, unknown[]>(() => false);
jest.mock('../tool_approval_set', () => ({
    __esModule: true,
    default: () => null,
    needsViewerDecision: (...args: unknown[]) => mockNeedsViewerDecision(...args),
}));

// The preview fetches the source post and profile on mount; stub it out so the
// source list can render without a client.
jest.mock('../post_preview', () => ({
    PostPreview: () => null,
}));

const mockUseSelector = useSelector as unknown as jest.Mock;
const mockUseConversation = useConversation as jest.Mock;

type PostUpdateHandler = (msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => void;

// Mattermost IDs are 26 characters of lowercase letters and digits.
const WELL_FORMED_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';

function makePost(message = '', props: Record<string, unknown> = {}) {
    return {
        id: 'post_1',
        channel_id: 'channel_1',
        root_id: 'root_1',
        message,
        props: {
            conversation_id: WELL_FORMED_ID,
            ...props,
        },
    };
}

function makeTurn(overrides: Partial<Turn> = {}): Turn {
    return {
        id: 'turn_1',
        post_id: 'post_1',
        role: 'assistant',
        content: [],
        tokens_in: 0,
        tokens_out: 0,
        sequence: 1,
        ...overrides,
    };
}

function makeConversation(turns: Turn[], userId = 'user_1'): ConversationResponse {
    return {
        id: WELL_FORMED_ID,
        user_id: userId,
        bot_id: 'bot_1',
        channel_id: 'channel_1',
        root_post_id: 'root_1',
        title: '',
        operation: 'conversation',
        turns,
    };
}

function makeToolOnlyConversation(
    approvalState: NonNullable<Turn['approval_state']>,
    toolStatus: 'rejected' | 'pending',
    userId = 'user_1',
): ConversationResponse {
    const assistantTurn = makeTurn({
        post_id: 'post_1',
        approval_state: approvalState,
        content: [{
            type: 'tool_use',
            id: 'tc_1',
            name: 'mattermost__get_channel_info',
            status: toolStatus,
        }],
    });

    if (toolStatus !== 'rejected') {
        return makeConversation([assistantTurn], userId);
    }

    return makeConversation([
        assistantTurn,
        makeTurn({
            id: 'turn_2',
            post_id: null,
            role: 'tool_result',
            sequence: 2,
            content: [{
                type: 'tool_result',
                tool_use_id: 'tc_1',
                content: 'Tool call rejected by user',
            }],
        }),
    ], userId);
}

// Builds a search source entry with a unique well-formed post id.
function makeSource(i: number) {
    return {
        postId: String(i).padStart(26, '0'),
        channelId: WELL_FORMED_ID,
        userId: WELL_FORMED_ID,
        content: `source message ${i}`,
        score: 0.5,
    };
}

function renderPost(
    post = makePost(),
    websocketRegister?: (postID: string, listenerID: string, handler: PostUpdateHandler) => void,
) {
    return render(
        <IntlProvider locale='en'>
            <LLMBotPost
                post={post}
                websocketRegister={websocketRegister}
                websocketUnregister={jest.fn()}
            />
        </IntlProvider>,
    );
}

function postUpdateMessage(data: PostUpdateWebsocketMessage): PluginWebSocketMessage<PostUpdateWebsocketMessage> {
    return {data} as PluginWebSocketMessage<PostUpdateWebsocketMessage>;
}

beforeEach(() => {
    mockUseSelector.mockImplementation((selector) => selector({
        entities: {
            channels: {
                channels: {
                    channel_1: {type: 'D'},
                },
            },
            posts: {
                posts: {},
            },
            users: {
                currentUserId: 'user_1',
            },
        },
    }));

    mockUseConversation.mockReturnValue({
        conversation: null,
        loading: false,
        error: null,
    });

    mockNeedsViewerDecision.mockReturnValue(false);
    mockPostTextRender.mockClear();
});

describe('LLMBotPost streaming fallback rendering', () => {
    test('keeps streamed error text visible after stream end while refetch is pending', async () => {
        const errorText = 'Sorry! An error occurred while accessing the LLM.';
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        renderPost(makePost(), websocketRegister);

        expect(screen.getByText('Working...')).toBeTruthy();
        expect(listener).toBeDefined();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({post_id: 'post_1', next: errorText}));
        });

        await expect(screen.findByText(errorText)).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'end'}));
        });

        expect(screen.getByText(errorText)).toBeTruthy();
        expect(screen.queryByText('Working...')).toBeNull();
    });

    test('renders updated post message when the streaming text websocket was missed', async () => {
        const errorText = 'Sorry! An error occurred while accessing the LLM.';
        const {rerender} = renderPost(makePost());

        expect(screen.getByText('Working...')).toBeTruthy();

        rerender(
            <IntlProvider locale='en'>
                <LLMBotPost
                    post={makePost(errorText)}
                    websocketUnregister={jest.fn()}
                />
            </IntlProvider>,
        );

        await waitFor(() => {
            expect(screen.getByText(errorText)).toBeTruthy();
        });
        expect(screen.queryByText('Working...')).toBeNull();
    });
});

describe('LLMBotPost setup progress', () => {
    test('advances phases in order and ignores regressions', () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'progress',
                progress_phase: 'checking_mcp',
                progress_seq: 1,
            }));
        });
        expect(screen.getByText('Checking MCP connections and tools...')).toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'progress',
                progress_phase: 'preparing_request',
                progress_seq: 3,
            }));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'progress',
                progress_phase: 'loading_conversation',
                progress_seq: 2,
            }));
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
        });
        expect(screen.getByText('Preparing request...')).toBeTruthy();
        expect(screen.queryByText('Loading conversation context...')).toBeNull();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'progress',
                progress_phase: 'connecting_provider',
                progress_seq: 4,
            }));
        });
        expect(screen.getByText('Connecting to provider...')).toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Hello'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'progress',
                progress_phase: 'connecting_provider',
                progress_seq: 4,
            }));
        });
        expect(screen.getByText('Hello')).toBeTruthy();
        expect(screen.queryByText('Connecting to provider...')).toBeNull();
    });

    test('shows reasoning when start was missed and ignores a late start', () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'progress',
                progress_phase: 'connecting_provider',
                progress_seq: 4,
            }));
        });
        expect(screen.getByText('Connecting to provider...')).toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'reasoning_summary',
                reasoning: 'Reasoning before text',
            }));
        });
        expect(screen.getByText('Thinking')).toBeTruthy();
        expect(screen.queryByText('Connecting to provider...')).toBeNull();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
        });
        expect(screen.getByText('Thinking')).toBeTruthy();
        expect(screen.queryByText('Starting...')).toBeNull();
    });
});

describe('LLMBotPost tool activity area', () => {
    // A response that used a tool: the intermediate round's text folds into
    // the collapsed activity area, the anchor round stays the post message.
    function conversationWithToolRound() {
        return {
            id: WELL_FORMED_ID,
            user_id: 'user_1',
            bot_id: 'bot_1',
            channel_id: 'channel_1',
            root_post_id: 'root_1',
            title: '',
            operation: 'conversation',
            turns: [
                {
                    id: 'u1',
                    post_id: 'user_post',
                    role: 'user',
                    sequence: 1,
                    tokens_in: 0,
                    tokens_out: 0,
                    content: [{type: 'text', text: 'look that up'}],
                },
                {
                    id: 'r1',
                    post_id: null,
                    role: 'assistant',
                    sequence: 2,
                    tokens_in: 0,
                    tokens_out: 0,
                    content: [
                        {type: 'text', text: 'Let me look that up'},
                        {type: 'tool_use', id: 'tc_a', name: 'search_tools', status: 'auto_approved'},
                    ],
                },
                {
                    id: 'tr1',
                    post_id: null,
                    role: 'tool_result',
                    sequence: 3,
                    tokens_in: 0,
                    tokens_out: 0,
                    content: [{type: 'tool_result', tool_use_id: 'tc_a', content: 'ok', status: 'success'}],
                },
                {
                    id: 'anchor',
                    post_id: 'post_1',
                    role: 'assistant',
                    sequence: 4,
                    approval_state: 'done',
                    tokens_in: 0,
                    tokens_out: 0,
                    content: [{type: 'text', text: 'Here is the answer'}],
                },
            ],
        };
    }

    beforeEach(() => {
        mockUseConversation.mockReturnValue({
            conversation: conversationWithToolRound(),
            loading: false,
            error: null,
        });
    });

    test('hides intermediate text behind the collapsed activity row and keeps the answer visible', () => {
        renderPost();

        expect(screen.getByText('Here is the answer')).toBeTruthy();
        expect(screen.queryByText('Let me look that up')).toBeNull();
        expect(screen.getByTestId('llm-bot-tool-activity')).toBeTruthy();
    });

    test('reveals the intermediate round when the activity row is expanded', () => {
        renderPost();

        fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));

        expect(screen.getByText('Let me look that up')).toBeTruthy();
        expect(screen.getByText('Here is the answer')).toBeTruthy();
    });
});

describe('LLMBotPost mid-stream text routing', () => {
    beforeEach(() => {
        jest.useFakeTimers();
    });

    afterEach(() => {
        jest.clearAllTimers();
        jest.useRealTimers();
    });

    // Drives one post over the websocket the way the server does.
    function streamingPost() {
        let listener: PostUpdateHandler | undefined;
        renderPost(makePost(), (postID, listenerID, handler) => {
            listener = handler;
        });

        const send = listener!;
        return (data: Omit<PostUpdateWebsocketMessage, 'post_id'>) => act(() => {
            send(postUpdateMessage({post_id: 'post_1', ...data}));
        });
    }

    // ToolRunner emits the round's calls again with terminal statuses once
    // they have run; that second event is what closes the round.
    function resolvedToolCall(id: string, name: string) {
        return {
            control: 'tool_call',
            tool_call: JSON.stringify([{id, name, description: '', status: ToolCallStatus.Success}]),
        };
    }

    const currentRow = () => screen.getByTestId('llm-bot-tool-activity-current').textContent;

    // What the post shows outside its activity area — the same contract the
    // e2e suite asserts, and the one that says whether text is in the main
    // area or only in the collapsed row.
    function mainAreaText(): string {
        const post = screen.getByTestId('llm-bot-post').cloneNode(true) as HTMLElement;
        post.querySelectorAll('[data-testid="llm-bot-tool-activity"]').forEach((node) => node.remove());
        return post.textContent ?? '';
    }

    // Nothing has called a tool yet, so there is no way to tell this text from
    // an answer: it streams into the main area, and has to leave gracefully.
    test('streams the first round into the main area and folds it away when a tool call lands', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'Let me look that up'});

        expect(screen.queryByTestId('llm-bot-tool-activity')).toBeNull();
        expect(screen.getByText('Let me look that up')).toBeTruthy();

        send(resolvedToolCall('tc_a', 'search_tools'));

        expect(screen.getByTestId('llm-bot-tool-activity')).toBeTruthy();
        expect(screen.getByTestId('llm-bot-folding-text').textContent).toBe('Let me look that up');

        advanceAnimation();
        expect(screen.queryByTestId('llm-bot-folding-text')).toBeNull();
    });

    // The narration of every round after the first goes straight to the row,
    // so the main area never moves.
    test('streams trailing text into the activity row instead of the main area', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'Let me look that up'});
        send(resolvedToolCall('tc_a', 'search_tools'));
        advanceAnimation();

        send({next: 'Here is what'});
        send({next: 'Here is what I found'});

        expect(mainAreaText()).not.toContain('Here is what I found');
        expect(screen.queryByTestId('llm-bot-folding-text')).toBeNull();

        advanceAnimation();
        expect(currentRow()).toBe('Here is what I found');
    });

    test('hands the trailing text back to the main area when the response ends', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'Let me look that up'});
        send(resolvedToolCall('tc_a', 'search_tools'));
        send({next: 'Here is what I found'});

        expect(mainAreaText()).not.toContain('Here is what I found');

        send({control: 'end'});

        expect(screen.getByText('Here is what I found')).toBeTruthy();
        expect(screen.getByTestId('llm-bot-tool-activity')).toBeTruthy();
    });

    // Stopping mid-response settles it just like a natural end, so the text
    // held in the row has to be released rather than stranded there.
    test('hands the trailing text back to the main area when generation is cancelled', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'Let me look that up'});
        send(resolvedToolCall('tc_a', 'search_tools'));
        send({next: 'Here is what I fou'});

        send({control: 'cancel'});

        expect(screen.getByText('Here is what I fou')).toBeTruthy();
    });

    // A reader who expanded the area has asked to watch the whole thing, so
    // nothing is rerouted and the text streams where it always did.
    test('leaves trailing text in the main area while the activity area is expanded', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'Let me look that up'});
        send(resolvedToolCall('tc_a', 'search_tools'));

        act(() => {
            fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
        });
        send({next: 'Here is what I found'});

        expect(mainAreaText()).toContain('Here is what I found');
    });

    // Collapsing mid-stream pulls the text into the row; it must fold on the
    // way rather than blink out, and come back on expanding again.
    test('folds and restores the trailing text as the area is toggled mid-stream', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'Let me look that up'});
        send(resolvedToolCall('tc_a', 'search_tools'));
        act(() => {
            fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
        });
        send({next: 'Here is what I found'});
        advanceAnimation();

        act(() => {
            fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
        });
        expect(screen.getByTestId('llm-bot-folding-text').textContent).toBe('Here is what I found');

        advanceAnimation();
        expect(screen.queryByTestId('llm-bot-folding-text')).toBeNull();
        expect(currentRow()).toBe('Here is what I found');

        act(() => {
            fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
        });
        expect(screen.getByText('Here is what I found')).toBeTruthy();
    });

    // A response that never calls a tool has no activity area to reroute
    // into, so it must keep streaming into the main area untouched.
    test('leaves a response without tool calls streaming in the main area', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'Here is'});
        send({next: 'Here is the answer'});

        expect(screen.queryByTestId('llm-bot-tool-activity')).toBeNull();
        expect(screen.queryByTestId('llm-bot-folding-text')).toBeNull();
        expect(screen.getByText('Here is the answer')).toBeTruthy();
    });
});

describe('LLMBotPost streaming re-renders', () => {
    function conversationWithPersistedAnswer() {
        return {
            id: WELL_FORMED_ID,
            user_id: 'user_1',
            bot_id: 'bot_1',
            channel_id: 'channel_1',
            root_post_id: 'root_1',
            title: '',
            operation: 'conversation',
            turns: [
                {
                    id: 'u1',
                    post_id: 'user_post',
                    role: 'user',
                    sequence: 1,
                    tokens_in: 0,
                    tokens_out: 0,
                    content: [{type: 'text', text: 'hello'}],
                },
                {
                    id: 'anchor',
                    post_id: 'post_1',
                    role: 'assistant',
                    sequence: 2,
                    approval_state: 'done',
                    tokens_in: 0,
                    tokens_out: 0,
                    content: [{type: 'text', text: 'Persisted answer'}],
                },
            ],
        };
    }

    // Every chunk re-renders the post. Rounds that already finished have not
    // changed, so re-rendering their markdown on each chunk is wasted work on
    // the hottest path in the component.
    test('does not re-render a settled round for each streamed chunk', () => {
        mockUseConversation.mockReturnValue({
            conversation: conversationWithPersistedAnswer(),
            loading: false,
            error: null,
        });

        let listener: PostUpdateHandler | undefined;
        renderPost(makePost(), (postID, listenerID, handler) => {
            listener = handler;
        });

        const settledRenders = () => mockPostTextRender.mock.calls.filter(([msg]) => msg === 'Persisted answer').length;

        const send = listener!;
        act(() => {
            send(postUpdateMessage({post_id: 'post_1', next: 'chunk one'}));
        });
        const afterFirstChunk = settledRenders();

        for (const next of ['chunk one two', 'chunk one two three', 'chunk one two three four']) {
            act(() => {
                send(postUpdateMessage({post_id: 'post_1', next}));
            });
        }

        expect(settledRenders()).toBe(afterFirstChunk);
        expect(screen.getByText('chunk one two three four')).toBeTruthy();
    });
});

describe('LLMBotPost rounds awaiting a decision', () => {
    // A response that stopped on a tool call: the anchor round holds both the
    // text asking to run the tool and the call itself.
    function conversationAwaitingApproval() {
        return {
            id: WELL_FORMED_ID,
            user_id: 'user_1',
            bot_id: 'bot_1',
            channel_id: 'channel_1',
            root_post_id: 'root_1',
            title: '',
            operation: 'conversation',
            turns: [
                {
                    id: 'u1',
                    post_id: 'user_post',
                    role: 'user',
                    sequence: 1,
                    tokens_in: 0,
                    tokens_out: 0,
                    content: [{type: 'text', text: 'post that for me'}],
                },
                {
                    id: 'anchor',
                    post_id: 'post_1',
                    role: 'assistant',
                    sequence: 2,
                    approval_state: 'call',
                    tokens_in: 0,
                    tokens_out: 0,
                    content: [
                        {type: 'text', text: 'I will post that'},
                        {type: 'tool_use', id: 'tc_a', name: 'create_post', status: 'pending'},
                    ],
                },
            ],
        };
    }

    beforeEach(() => {
        mockUseConversation.mockReturnValue({
            conversation: conversationAwaitingApproval(),
            loading: false,
            error: null,
        });
    });

    // The approval card needs the request that produced it, so the round
    // renders in full rather than folding into the collapsed row.
    test('keeps a round the viewer must decide on out of the activity area', () => {
        mockNeedsViewerDecision.mockReturnValue(true);

        renderPost();

        expect(screen.getByText('I will post that')).toBeTruthy();
        expect(screen.queryByTestId('llm-bot-tool-activity')).toBeNull();
    });

    // Onlookers are never asked to decide, so the same round is just activity.
    test('folds the same round into the activity area for a viewer who owes no decision', () => {
        mockNeedsViewerDecision.mockReturnValue(false);

        renderPost();

        expect(screen.queryByText('I will post that')).toBeNull();
        expect(screen.getByTestId('llm-bot-tool-activity')).toBeTruthy();
    });

    // A response paused on a decision is not finished, so the row must keep
    // naming what happened last instead of summarizing as if it were done.
    test('does not summarize the activity row while a decision is pending', () => {
        mockNeedsViewerDecision.mockReturnValue(true);
        mockUseConversation.mockReturnValue({
            conversation: {
                ...conversationAwaitingApproval(),
                turns: [
                    {
                        id: 'u1',
                        post_id: 'user_post',
                        role: 'user',
                        sequence: 1,
                        tokens_in: 0,
                        tokens_out: 0,
                        content: [{type: 'text', text: 'post that for me'}],
                    },
                    {
                        id: 'meta',
                        post_id: null,
                        role: 'assistant',
                        sequence: 2,
                        tokens_in: 0,
                        tokens_out: 0,
                        content: [{type: 'tool_use', id: 'tc_meta', name: 'search_tools', status: 'auto_approved'}],
                    },
                    {
                        id: 'anchor',
                        post_id: 'post_1',
                        role: 'assistant',
                        sequence: 3,
                        approval_state: 'call',
                        tokens_in: 0,
                        tokens_out: 0,
                        content: [
                            {type: 'text', text: 'I will post that'},
                            {type: 'tool_use', id: 'tc_a', name: 'create_post', status: 'pending'},
                        ],
                    },
                ],
            },
            loading: false,
            error: null,
        });

        renderPost();

        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Search Tools');
        expect(screen.queryByText('Used 1 tool')).toBeNull();
    });

    // A pending tool_call can land over the websocket before refetch persists
    // the round. That live round must stay out of the activity area so the
    // requester still sees the approval card, not a collapsed "create_post".
    test('keeps a live pending tool call out of the activity area for the requester', () => {
        mockUseConversation.mockReturnValue({
            conversation: {
                id: WELL_FORMED_ID,
                user_id: 'user_1',
                bot_id: 'bot_1',
                channel_id: 'channel_1',
                root_post_id: 'root_1',
                title: '',
                operation: 'conversation',
                turns: [
                    {
                        id: 'u1',
                        post_id: 'user_post',
                        role: 'user',
                        sequence: 1,
                        tokens_in: 0,
                        tokens_out: 0,
                        content: [{type: 'text', text: 'post that for me'}],
                    },
                ],
            },
            loading: false,
            error: null,
        });

        let listener: PostUpdateHandler | undefined;
        renderPost(makePost(), (postID, listenerID, handler) => {
            listener = handler;
        });

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Let me look that up'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'tool_call',
                tool_call: JSON.stringify([{id: 'tc_search', name: 'search_tools', description: '', status: ToolCallStatus.Success}]),
            }));
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'I will post that'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'tool_call',
                tool_call: JSON.stringify([{id: 'tc_a', name: 'create_post', description: '', status: ToolCallStatus.Pending}]),
            }));
        });

        expect(screen.getByText('I will post that')).toBeTruthy();
        expect(screen.getByTestId('llm-bot-tool-activity')).toBeTruthy();
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).not.toBe('Create Post');
    });
});

describe('LLMBotPost server tool activity rendering', () => {
    test('renders provider tool activity from server_tool websocket events', async () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        renderPost(makePost(), websocketRegister);
        expect(listener).toBeDefined();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'in_progress', query: 'release notes'},
                ]),
            }));
        });

        await expect(screen.findByText('Searched the web for "release notes"')).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'success', query: 'release notes'},
                    {id: 'srv2', tool: 'code_interpreter', status: 'success', sub_tool: 'bash', command: 'ls', output: 'file.txt'},
                ]),
            }));
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'All done.'}));
        });

        await expect(screen.findByText('Ran code in the provider sandbox')).resolves.toBeTruthy();
        expect(screen.getByText('Searched the web for "release notes"')).toBeTruthy();
        expect(screen.getByText('All done.')).toBeTruthy();
    });

    test('a fresh stream clears prior server tool activity', async () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_fetch', status: 'success', url: 'https://example.com/doc'},
                ]),
            }));
        });

        await expect(screen.findByText('Fetched example.com')).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
        });

        await waitFor(() => {
            expect(screen.queryByText('Fetched example.com')).toBeNull();
        });
    });

    test('ignores a non-array server_tool payload instead of crashing', () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: 'null',
            }));
        });

        expect(screen.getByText('Error parsing server tool data')).toBeTruthy();
    });
});

describe('LLMBotPost live activity ordering', () => {
    // RoundView renders activity above text, so activity after text starts a new
    // round. Otherwise narration between two sandbox runs collapses above both.
    // `next` payloads are cumulative: the server only resets its message
    // builder at resolved tool_call boundaries, never at activity boundaries.
    test('narration between two sandbox runs renders in arrival order', async () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        const {container} = renderPost(makePost(), websocketRegister);
        expect(listener).toBeDefined();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({post_id: 'post_1', next: "I'll write the script."}));
        });
        await expect(screen.findByText("I'll write the script.")).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'success', query: 'first'},
                ]),
            }));
            listener?.(postUpdateMessage({post_id: 'post_1', next: "I'll write the script.That found nothing. Retrying."}));
        });
        await expect(screen.findByText('That found nothing. Retrying.')).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'success', query: 'first'},
                    {id: 'srv2', tool: 'web_search', status: 'success', query: 'second'},
                ]),
            }));
            listener?.(postUpdateMessage({post_id: 'post_1', next: "I'll write the script.That found nothing. Retrying.Done."}));
        });
        await expect(screen.findByText('Done.')).resolves.toBeTruthy();

        // Assert real DOM order, not just presence — the bug was purely ordering.
        const rendered = container.textContent ?? '';
        const positions = [
            "I'll write the script.",
            'Searched the web for "first"',
            'That found nothing. Retrying.',
            'Searched the web for "second"',
            'Done.',
        ].map((needle) => rendered.indexOf(needle));

        expect(positions.every((pos) => pos >= 0)).toBe(true);
        expect(positions).toEqual([...positions].sort((a, b) => a - b));
    });

    test('splitting does not duplicate the text it closed a round on', async () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        const {container} = renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Only once.'}));
        });
        await expect(screen.findByText('Only once.')).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'success', query: 'q'},
                ]),
            }));
        });
        await expect(screen.findByText('Searched the web for "q"')).resolves.toBeTruthy();

        expect(screen.getAllByText('Only once.')).toHaveLength(1);
        expect((container.textContent ?? '').split('Only once.').length - 1).toBe(1);

        // The next cumulative payload still carries the frozen text; only the
        // remainder may render or the frozen round's text shows twice.
        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Only once.And the rest.'}));
        });
        await expect(screen.findByText('And the rest.')).resolves.toBeTruthy();
        expect((container.textContent ?? '').split('Only once.').length - 1).toBe(1);
    });

    test('activity before any text keeps a single round', async () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        const {container} = renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'success', query: 'q'},
                ]),
            }));
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Here you go.'}));
        });

        await expect(screen.findByText('Here you go.')).resolves.toBeTruthy();

        const rendered = container.textContent ?? '';
        expect(rendered.indexOf('Searched the web for "q"')).toBeLessThan(rendered.indexOf('Here you go.'));
    });

    test('reasoning that follows provider activity starts a new live round', async () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        const {container} = renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'success', query: 'first'},
                ]),
            }));
        });
        await expect(screen.findByText('Searched the web for "first"')).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'reasoning_summary',
                reasoning: 'Considering the result',
            }));
        });
        await expect(screen.findByText('Thinking')).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Final answer.'}));
        });
        await expect(screen.findByText('Final answer.')).resolves.toBeTruthy();

        const rendered = container.textContent ?? '';
        expect(rendered.indexOf('Searched the web for "first"')).toBeLessThan(rendered.indexOf('Thinking'));
        expect(rendered.indexOf('Thinking')).toBeLessThan(rendered.indexOf('Final answer.'));
    });

    // Matches splitTurnIntoRounds: a thinking block after text starts a new
    // round even with no activity in between, so live and persisted rendering
    // agree and the refetch does not reflow the post.
    test('reasoning that follows text starts a new live round', async () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        const {container} = renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Intro.'}));
        });
        await expect(screen.findByText('Intro.')).resolves.toBeTruthy();

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'reasoning_summary',
                reasoning: 'Weighing the options',
            }));
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Intro.Answer.'}));
        });
        await expect(screen.findByText('Answer.')).resolves.toBeTruthy();

        const rendered = container.textContent ?? '';
        expect(rendered.indexOf('Intro.')).toBeLessThan(rendered.indexOf('Thinking'));
        expect(rendered.indexOf('Thinking')).toBeLessThan(rendered.indexOf('Answer.'));
        expect(rendered.split('Intro.').length - 1).toBe(1);
    });

    // Snapshots are cumulative and invocations update in place, so a status
    // change must reach an invocation already frozen into an earlier round or
    // its spinner sticks until the refetch.
    test('a status update reaches an invocation frozen into an earlier round', async () => {
        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        renderPost(makePost(), websocketRegister);

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'in_progress'},
                ]),
            }));
        });
        await expect(screen.findByText('Searched the web')).resolves.toBeTruthy();

        // Reasoning after activity freezes srv1 into a completed live round.
        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'reasoning_summary',
                reasoning: 'Looking at the results',
            }));
        });

        act(() => {
            listener?.(postUpdateMessage({
                post_id: 'post_1',
                control: 'server_tool',
                server_tool: JSON.stringify([
                    {id: 'srv1', tool: 'web_search', status: 'success', query: 'release notes'},
                    {id: 'srv2', tool: 'code_interpreter', status: 'in_progress'},
                ]),
            }));
        });

        // The frozen round's card must pick up the completed query/status.
        await expect(screen.findByText('Searched the web for "release notes"')).resolves.toBeTruthy();
        expect(screen.queryByText('Searched the web')).toBeNull();
        expect(screen.getByText('Ran code in the provider sandbox')).toBeTruthy();
    });
});

describe('LLMBotPost remount of empty-message tool-only posts', () => {
    test.each([
        {
            name: 'rejected tool_use',
            approvalState: 'done' as const,
            toolStatus: 'rejected' as const,
            userId: 'user_1',
        },
        {
            name: 'pending tool_use',
            approvalState: 'call' as const,
            toolStatus: 'pending' as const,
            userId: 'user_1',
        },
        {
            name: 'pending tool_use as an onlooker',
            approvalState: 'call' as const,
            toolStatus: 'pending' as const,
            userId: 'other_user',
        },
    ])('does not show Working... when conversation already has a $name', ({approvalState, toolStatus, userId}) => {
        mockUseConversation.mockReturnValue({
            conversation: makeToolOnlyConversation(approvalState, toolStatus, userId),
            loading: false,
            error: null,
        });

        renderPost(makePost());

        expect(screen.queryByText('Working...')).toBeNull();
    });

    test('shows Working... after continue while generation resumes over persisted rounds', () => {
        mockUseConversation.mockReturnValue({
            conversation: makeToolOnlyConversation('call', 'pending', 'other_user'),
            loading: false,
            error: null,
        });

        let listener: PostUpdateHandler | undefined;
        const websocketRegister = jest.fn((postID, listenerID, handler) => {
            listener = handler;
        });

        renderPost(makePost(), websocketRegister);

        expect(screen.queryByText('Working...')).toBeNull();
        expect(listener).toBeDefined();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'continue'}));
        });

        expect(screen.getByText('Working...')).toBeTruthy();
    });

    test('still shows Working... when the conversation is loaded but this post has no rounds yet', () => {
        mockUseConversation.mockReturnValue({
            conversation: makeConversation([
                makeTurn({
                    id: 'user_turn',
                    post_id: 'root_1',
                    role: 'user',
                    sequence: 1,
                    content: [{type: 'text', text: 'hello'}],
                }),
            ]),
            loading: false,
            error: null,
        });

        renderPost(makePost());

        expect(screen.getByText('Working...')).toBeTruthy();
    });
});

describe('LLMBotPost conversation_id prop handling', () => {
    // Keep call assertions scoped to each test; the file-level beforeEach only
    // re-stubs the return value.
    beforeEach(() => {
        mockUseConversation.mockClear();
    });

    test('passes a well-formed conversation_id to useConversation', () => {
        renderPost();

        expect(mockUseConversation).toHaveBeenCalledWith(WELL_FORMED_ID);
    });

    // Post props are free-form JSON, so the prop can hold anything; a value
    // that is not a well-formed id must be treated as absent.
    test.each([
        {name: 'relative path segments', value: '../../some/other/route'},
        {name: 'too short', value: 'abc'},
        {name: 'right length but contains a separator', value: 'abcdefghijklmnopqrstuvwxy/'},
        {name: 'not a string', value: 42},
        {name: 'null', value: null},
    ])('ignores a conversation_id that is not well-formed: $name', ({value}) => {
        expect(() => renderPost(makePost('', {conversation_id: value}))).not.toThrow();

        expect(mockUseConversation).toHaveBeenCalledWith(void 0); // eslint-disable-line no-void
    });
});

describe('LLMBotPost search_results prop handling', () => {
    test('renders the source list for a well-formed search_results prop', () => {
        const sources = [makeSource(1), makeSource(2)];
        renderPost(makePost('hello', {search_results: JSON.stringify(sources)}));

        expect(screen.getByText('Sources')).toBeTruthy();
        expect(screen.getByText('2')).toBeTruthy();
    });

    test('bounds the rendered source list to the server maximum result count', () => {
        const sources = Array.from({length: MAX_SEARCH_SOURCES + 50}, (_, i) => makeSource(i));
        renderPost(makePost('hello', {search_results: JSON.stringify(sources)}));

        expect(screen.getByText(String(MAX_SEARCH_SOURCES))).toBeTruthy();
    });

    // Post props are free-form JSON; none of these values may throw during
    // render, and none should produce a source list.
    test.each([
        {name: 'not a string', value: 42},
        {name: 'an object instead of a JSON string', value: {postId: WELL_FORMED_ID}},
        {name: 'not valid JSON', value: '{not json'},
        {name: 'a JSON object instead of an array', value: '{"postId":"x"}'},
        {name: 'a JSON string instead of an array', value: '"just a string"'},
        {name: 'entries that are not objects', value: '[null, "x", 7]'},
        {name: 'entries without well-formed ids', value: JSON.stringify([{postId: 'short', channelId: 'short', userId: 'short', content: 'hi', score: 1}])},
    ])('renders no source list when search_results is malformed: $name', ({value}) => {
        expect(() => renderPost(makePost('hello', {search_results: value}))).not.toThrow();

        expect(screen.queryByText('Sources')).toBeNull();
    });
});
