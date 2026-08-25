// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import {useConversation} from '@/hooks/use_conversation';
import {PluginWebSocketMessage} from '@/types';
import type {ConversationResponse, Turn} from '@/types/conversation';

import {MAX_SEARCH_SOURCES} from '../search_sources';

import {LLMBotPost, PostUpdateWebsocketMessage} from './llmbot_post';

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

jest.mock('../post_text', () => {
    const ReactLocal = jest.requireActual('react') as typeof React;

    return {
        __esModule: true,
        default: ({message}: {message: string}) => ReactLocal.createElement('div', null, message),
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
    ])('does not show Starting... when conversation already has a $name', ({approvalState, toolStatus, userId}) => {
        mockUseConversation.mockReturnValue({
            conversation: makeToolOnlyConversation(approvalState, toolStatus, userId),
            loading: false,
            error: null,
        });

        renderPost(makePost());

        expect(screen.queryByText('Starting...')).toBeNull();
    });

    test('shows Starting... after continue while generation resumes over persisted rounds', () => {
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

        expect(screen.queryByText('Starting...')).toBeNull();
        expect(listener).toBeDefined();

        act(() => {
            listener?.(postUpdateMessage({post_id: 'post_1', control: 'continue'}));
        });

        expect(screen.getByText('Starting...')).toBeTruthy();
    });

    test('still shows Starting... when the conversation is loaded but this post has no rounds yet', () => {
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

        expect(screen.getByText('Starting...')).toBeTruthy();
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
