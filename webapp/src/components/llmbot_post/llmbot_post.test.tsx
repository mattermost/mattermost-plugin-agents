// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import {useConversation} from '@/hooks/use_conversation';
import {PluginWebSocketMessage} from '@/types';

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

describe('LLMBotPost live activity ordering', () => {
    // Provider activity that starts after the round already has text closes that
    // round, so narration written between two sandbox runs renders between them
    // instead of collapsing into one block above both activity cards.
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
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'That found nothing. Retrying.'}));
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
            listener?.(postUpdateMessage({post_id: 'post_1', next: 'Done.'}));
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

    // The first narration must not be repeated into the round the split creates.
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
    });

    // Activity arriving before any narration must not create an empty round.
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
