// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {useSelector} from 'react-redux';

import {WebSocketMessage} from '@mattermost/client';

import {useConversation} from '@/hooks/use_conversation';

import {LLMBotPost, PostUpdateWebsocketMessage} from './llmbot_post';

jest.mock('react-redux', () => ({
    useSelector: jest.fn(),
}));

jest.mock('react-intl', () => {
    const ReactLocal = jest.requireActual('react') as typeof React;

    return {
        IntlProvider: ({children}: {children: React.ReactNode}) => ReactLocal.createElement(ReactLocal.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => ReactLocal.createElement(ReactLocal.Fragment, null, defaultMessage),
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

// SearchSources renders this for every source; stub it so the source list
// itself stays real, and so tests can count how many entries it produced.
const mockPostPreview = jest.fn(() => null);

jest.mock('../post_preview', () => ({
    PostPreview: () => mockPostPreview(),
}));

const mockUseSelector = useSelector as unknown as jest.Mock;
const mockUseConversation = useConversation as jest.Mock;

type PostUpdateHandler = (msg: WebSocketMessage<PostUpdateWebsocketMessage>) => void;

function makePost(message = '') {
    return {
        id: 'post_1',
        channel_id: 'channel_1',
        root_id: 'root_1',
        message,
        props: {
            conversation_id: 'conversation_1',
        },
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

function postUpdateMessage(data: PostUpdateWebsocketMessage): WebSocketMessage<PostUpdateWebsocketMessage> {
    return {data} as WebSocketMessage<PostUpdateWebsocketMessage>;
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

    mockPostPreview.mockClear();
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

describe('LLMBotPost search results rendering', () => {
    // Mattermost IDs are 26 characters of lowercase letters and digits.
    const sourcePostId = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';
    const sourceChannelId = 'kq3n7vd1x9r4bz2m8sw6t5jhpc';
    const sourceUserId = 'ehz9k3wqr7t1a5m2xd8pnb4jsc';

    const wellFormedSource = {
        postId: sourcePostId,
        channelId: sourceChannelId,
        userId: sourceUserId,
        content: 'a matching message',
        score: 0.5,
    };

    function renderWithSearchResults(searchResults: unknown) {
        return renderPost({
            ...makePost('here is what I found'),
            props: {conversation_id: 'conversation_1', search_results: searchResults},
        } as unknown as ReturnType<typeof makePost>);
    }

    test('renders the source list for a well-formed search_results prop', () => {
        renderWithSearchResults(JSON.stringify([wellFormedSource]));

        expect(screen.getByText('Sources')).toBeTruthy();
    });

    // search_results is read straight off the post props, so its shape is only
    // as trustworthy as whoever authored the post.
    test.each([
        {name: 'not JSON at all', searchResults: 'not json'},
        {name: 'a truncated JSON array', searchResults: '[{"postId":'},
        {name: 'a bare word', searchResults: 'sources'},
        {name: 'a JSON object', searchResults: '{}'},
        {name: 'a JSON number', searchResults: '5'},
        {name: 'a JSON string', searchResults: '"sources"'},
        {name: 'a JSON boolean', searchResults: 'true'},
        {name: 'not a string', searchResults: 5},
        {name: 'an object', searchResults: {}},
    ])('renders when search_results is $name', ({searchResults}) => {
        expect(() => renderWithSearchResults(searchResults)).not.toThrow();
    });

    // Being a JSON array says nothing about what the array holds, so every
    // element needs the same treatment as the prop itself.
    test.each([
        {name: 'a null element', searchResults: '[null]'},
        {name: 'a string element', searchResults: '["a source"]'},
        {name: 'a number element', searchResults: '[5]'},
        {name: 'an element with no fields', searchResults: '[{}]'},
        {name: 'a well-formed element next to a null element', searchResults: JSON.stringify([wellFormedSource, null])},
    ])('renders when search_results holds $name', ({searchResults}) => {
        expect(() => renderWithSearchResults(searchResults)).not.toThrow();
    });

    // The search API caps a result set at maxMaxResults (api/api_search.go), so
    // a longer list did not come from a search. Each entry mounts a PostPreview
    // that reads a post and a profile on mount, so the length of this array
    // decides how much work every reader's client does.
    const SERVER_RESULT_CAP = 100;

    test('does not render an entry per element when search_results is longer than a search can return', () => {
        const elementCount = SERVER_RESULT_CAP * 5;

        // Each element carries a distinct post id, so the entry count can only
        // be explained by the cap and not by two sources sharing an identity.
        const sources = Array.from({length: elementCount}, (unused, index) => ({
            ...wellFormedSource,
            postId: index.toString(36).padStart(26, '0'),
        }));

        renderWithSearchResults(JSON.stringify(sources));

        expect(mockPostPreview.mock.calls.length).toBeLessThanOrEqual(SERVER_RESULT_CAP);
    });
});
