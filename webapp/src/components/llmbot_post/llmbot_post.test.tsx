// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import {useConversation} from '@/hooks/use_conversation';
import {PluginWebSocketMessage} from '@/types';

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

        expect(screen.getByText('Starting...')).toBeTruthy();
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
        expect(screen.queryByText('Starting...')).toBeNull();
    });

    test('renders updated post message when the streaming text websocket was missed', async () => {
        const errorText = 'Sorry! An error occurred while accessing the LLM.';
        const {rerender} = renderPost(makePost());

        expect(screen.getByText('Starting...')).toBeTruthy();

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

        // The final snapshot replaces the in-progress one and adds the sandbox run.
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
