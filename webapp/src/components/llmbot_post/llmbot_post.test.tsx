// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';
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

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
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

jest.mock('../tool_approval_set', () => ({
    __esModule: true,
    default: () => null,
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

// What the post shows outside its activity area and any folding ghost.
function mainAreaText(): string {
    const post = screen.getByTestId('llm-bot-post').cloneNode(true) as HTMLElement;
    post.querySelectorAll('[data-testid="llm-bot-tool-activity"], [data-testid="llm-bot-folding-text"]').forEach((node) => node.remove());
    return post.textContent ?? '';
}

// Post text without the collapsed row, whose label repeats the latest invocation.
function textWithoutActivityHeader(container: HTMLElement): string {
    const clone = container.cloneNode(true) as HTMLElement;
    clone.querySelectorAll('[data-testid="llm-bot-tool-activity-header"]').forEach((node) => node.remove());
    return clone.textContent ?? '';
}

function expandToolActivity() {
    act(() => {
        fireEvent.click(screen.getByTestId('llm-bot-tool-activity-header'));
    });
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

    test('folds the reasoning of the answer round into the activity area', () => {
        const conversation = conversationWithToolRound();
        conversation.turns[3] = {
            ...conversation.turns[3],
            content: [{type: 'thinking', text: 'Weighing the result'}, {type: 'text', text: 'Here is the answer'}],
        };
        mockUseConversation.mockReturnValue({conversation, loading: false, error: null});

        renderPost();

        expect(mainAreaText()).toContain('Here is the answer');
        expect(mainAreaText()).not.toContain('Thinking');
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toMatch(/^Used /);

        expandToolActivity();
        expect(within(screen.getByTestId('llm-bot-tool-activity-rounds')).getByText('Thinking')).toBeTruthy();
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

    // ToolRunner re-emits a round's calls with terminal statuses once they ran; that closes the round.
    function resolvedToolCall(id: string, name: string) {
        return {
            control: 'tool_call',
            tool_call: JSON.stringify([{id, name, description: '', status: ToolCallStatus.Success}]),
        };
    }

    function serverTool(...uses: Array<{id: string; query: string}>) {
        return {
            control: 'server_tool',
            server_tool: JSON.stringify(uses.map((use) => ({...use, tool: 'web_search', status: 'success'}))),
        };
    }

    test('streams the first round into the main area and folds it away when a tool call lands', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'Let me look that up'});

        expect(screen.queryByTestId('llm-bot-tool-activity')).toBeNull();
        expect(mainAreaText()).toContain('Let me look that up');

        send(resolvedToolCall('tc_a', 'search_tools'));

        expect(screen.getByTestId('llm-bot-tool-activity')).toBeTruthy();
        expect(screen.getByTestId('llm-bot-folding-text').textContent).toBe('Let me look that up');

        advanceAnimation();
        expect(screen.queryByTestId('llm-bot-folding-text')).toBeNull();
        expect(mainAreaText()).not.toContain('Let me look that up');
    });

    test('streams the answer after a tool call into the main area as it arrives', () => {
        const send = streamingPost();
        send({control: 'start'});
        send(resolvedToolCall('tc_a', 'search_tools'));

        send({next: 'Here is what'});
        expect(mainAreaText()).toContain('Here is what');

        send({next: 'Here is what I found'});
        expect(mainAreaText()).toContain('Here is what I found');
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Search Tools');
    });

    test('folds narration away once the next tool call shows it was not the answer', () => {
        const send = streamingPost();
        send({control: 'start'});
        send(resolvedToolCall('tc_a', 'search_tools'));
        send({next: 'Now let me read the channel'});

        send({control: 'tool_call', tool_call: JSON.stringify([{id: 'tc_b', name: 'read_channel', description: '', status: ToolCallStatus.Pending}])});

        expect(screen.getByTestId('llm-bot-folding-text').textContent).toBe('Now let me read the channel');
        advanceAnimation();
        expect(mainAreaText()).not.toContain('Now let me read the channel');
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Read Channel');
    });

    test('moves narration into the expanded stack without a fold', () => {
        const send = streamingPost();
        send({control: 'start'});
        send(resolvedToolCall('tc_a', 'search_tools'));
        expandToolActivity();
        send({next: 'Now let me read the channel'});

        send({control: 'tool_call', tool_call: JSON.stringify([{id: 'tc_b', name: 'read_channel', description: '', status: ToolCallStatus.Pending}])});

        expect(screen.queryByTestId('llm-bot-folding-text')).toBeNull();
        expect(within(screen.getByTestId('llm-bot-tool-activity-rounds')).getByText('Now let me read the channel')).toBeTruthy();
    });

    test('folds the first round away when a provider tool starts after it', () => {
        const send = streamingPost();
        send({control: 'start'});
        send({next: 'I will search the web'});
        send(serverTool({id: 'srv1', query: 'release notes'}));

        expect(screen.getByTestId('llm-bot-folding-text').textContent).toBe('I will search the web');
        advanceAnimation();
        expect(mainAreaText()).not.toContain('I will search the web');
    });

    test('keeps text after provider tools in the main area until another invocation follows', () => {
        const send = streamingPost();
        send({control: 'start'});
        send(serverTool({id: 'srv1', query: 'first'}));
        send({next: 'That found nothing.'});

        expect(mainAreaText()).toContain('That found nothing.');

        send(serverTool({id: 'srv1', query: 'first'}, {id: 'srv2', query: 'second'}));

        expect(screen.getByTestId('llm-bot-folding-text').textContent).toBe('That found nothing.');
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Searched the web for "second"');
    });

    test('shows a reasoning block after a tool round on the activity line, not below it', () => {
        const send = streamingPost();
        send({control: 'start'});
        send(resolvedToolCall('tc_a', 'search_tools'));
        send({control: 'reasoning_summary', reasoning: 'Weighing the result'});

        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Thinking');
        expect(mainAreaText()).not.toContain('Thinking');

        send({control: 'reasoning_summary_done', reasoning: 'Weighing the result'});
        send({next: 'Here is the answer'});
        send({control: 'end'});

        expect(mainAreaText()).toContain('Here is the answer');
        expect(mainAreaText()).not.toContain('Thinking');
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toMatch(/^Used /);
    });

    test('carries the setup status and the first tool on the same line', () => {
        const send = streamingPost();
        send({control: 'progress', progress_phase: 'connecting_provider', progress_seq: 4});
        const header = screen.getByTestId('llm-bot-tool-activity-header');
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Connecting to provider...');

        send({control: 'start'});
        send({control: 'tool_call', tool_call: JSON.stringify([{id: 'tc_a', name: 'read_channel', description: '', status: ToolCallStatus.Pending}])});

        expect(screen.getByTestId('llm-bot-tool-activity-header')).toBe(header);
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Read Channel');
    });

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
    // A response stopped on a tool call: the anchor round holds the text asking to run it and the call.
    function conversationAwaitingApproval(userId = 'user_1', earlierTurns: Turn[] = []) {
        return makeConversation([
            makeTurn({id: 'u1', post_id: 'user_post', role: 'user', content: [{type: 'text', text: 'post that for me'}]}),
            ...earlierTurns,
            makeTurn({
                id: 'anchor',
                sequence: 3,
                approval_state: 'call',
                content: [
                    {type: 'text', text: 'I will post that'},
                    {type: 'tool_use', id: 'tc_a', name: 'create_post', status: 'pending'},
                ],
            }),
        ], userId);
    }

    test('keeps a round the requester must decide on out of the activity area', () => {
        mockUseConversation.mockReturnValue({conversation: conversationAwaitingApproval(), loading: false, error: null});

        renderPost();

        expect(screen.getByText('I will post that')).toBeTruthy();
        expect(screen.queryByTestId('llm-bot-tool-activity')).toBeNull();
    });

    test('folds the same round into the activity area for a viewer who owes no decision', () => {
        mockUseConversation.mockReturnValue({conversation: conversationAwaitingApproval('someone_else'), loading: false, error: null});

        renderPost();

        expect(screen.queryByText('I will post that')).toBeNull();
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Create Post');
    });

    test('does not summarize the activity row while a decision is pending', () => {
        mockUseConversation.mockReturnValue({
            conversation: conversationAwaitingApproval('user_1', [makeTurn({
                id: 'meta',
                post_id: null,
                sequence: 2,
                content: [{type: 'tool_use', id: 'tc_meta', name: 'search_tools', status: 'auto_approved'}],
            })]),
            loading: false,
            error: null,
        });

        renderPost();

        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Search Tools');
        expect(screen.queryByText('Used 1 tool')).toBeNull();
    });

    // A pending tool_call can land over the websocket before the refetch persists the round.
    test('keeps a live pending tool call out of the activity area for the requester', () => {
        mockUseConversation.mockReturnValue({
            conversation: makeConversation([
                makeTurn({id: 'u1', post_id: 'user_post', role: 'user', content: [{type: 'text', text: 'post that for me'}]}),
            ]),
            loading: false,
            error: null,
        });

        let listener: PostUpdateHandler | undefined;
        renderPost(makePost(), (postID, listenerID, handler) => {
            listener = handler;
        });

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'start'}));
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
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Search Tools');
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

        await waitFor(() => {
            expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Ran code in the provider sandbox');
        });
        expect(mainAreaText()).toContain('All done.');

        expandToolActivity();
        const stack = within(screen.getByTestId('llm-bot-tool-activity-rounds'));
        expect(stack.getByText('Searched the web for "release notes"')).toBeTruthy();
        expect(stack.getByText('Ran code in the provider sandbox')).toBeTruthy();
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
        expandToolActivity();

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
        const rendered = textWithoutActivityHeader(container);
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
        expandToolActivity();

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

    test('reasoning that follows provider activity joins the activity line as a new round', async () => {
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
        expect(screen.getByTestId('llm-bot-tool-activity-current').textContent).toBe('Thinking');
        expect(mainAreaText()).not.toContain('Thinking');

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Final answer.'}));
        });
        await expect(screen.findByText('Final answer.')).resolves.toBeTruthy();
        expect(mainAreaText()).toContain('Final answer.');
        expect(mainAreaText()).not.toContain('Thinking');

        expandToolActivity();
        const stackText = screen.getByTestId('llm-bot-tool-activity-rounds').textContent ?? '';
        expect(stackText.indexOf('Searched the web for "first"')).toBeLessThan(stackText.indexOf('Thinking'));
        expect(stackText).not.toContain('Final answer.');
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
        expandToolActivity();
        const stack = within(screen.getByTestId('llm-bot-tool-activity-rounds'));
        await expect(stack.findByText('Searched the web for "release notes"')).resolves.toBeTruthy();
        expect(stack.queryByText('Searched the web')).toBeNull();
        expect(stack.getByText('Ran code in the provider sandbox')).toBeTruthy();
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

describe('LLMBotPost meetings license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
        mockUseSelector.mockImplementation((selector) => selector({
            entities: {
                channels: {
                    channels: {
                        channel_1: {type: 'D'},
                    },
                },
                posts: {
                    posts: {
                        root_1: {props: {referenced_transcript_post_id: 'transcript_1'}},
                    },
                },
                users: {
                    currentUserId: 'user_1',
                },
            },
        }));
    });

    test('shows Post summary for a transcription result at Enterprise', () => {
        renderPost(makePost('Call summary', {
            conversation_id: '',
            llm_requester_user_id: 'user_1',
        }));

        expect(screen.getByText('Post summary')).not.toBeNull();
    });

    test('hides Post summary below Enterprise', () => {
        useIsLicensedFor.mockReturnValue(false);
        renderPost(makePost('Call summary', {
            conversation_id: '',
            llm_requester_user_id: 'user_1',
        }));

        expect(screen.queryByText('Post summary')).toBeNull();
    });
});
