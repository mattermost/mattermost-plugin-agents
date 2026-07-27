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

jest.mock('../post_preview', () => ({
    PostPreview: () => null,
}));

const mockUseSelector = useSelector as unknown as jest.Mock;
const mockUseConversation = useConversation as jest.Mock;

type PostUpdateHandler = (msg: WebSocketMessage<PostUpdateWebsocketMessage>) => void;

// Mattermost IDs are 26 characters of lowercase letters and digits.
const CONVERSATION_ID = 'w4x8t2jr9m1kd6bn5zqh3vscp7';

function makePost(message = '') {
    return {
        id: 'post_1',
        channel_id: 'channel_1',
        root_id: 'root_1',
        message,
        props: {
            conversation_id: CONVERSATION_ID,
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
    const wellFormedSource = {
        postId: 'c7f2m9xq4v1b8n3k6t5w0hzjd2',
        channelId: 'kq3n7vd1x9r4bz2m8sw6t5jhpc',
        userId: 'ehz9k3wqr7t1a5m2xd8pnb4jsc',
        content: 'a matching message',
        score: 0.5,
    };

    function renderWithSearchResults(searchResults: unknown) {
        return renderPost({
            ...makePost('here is what I found'),
            props: {conversation_id: CONVERSATION_ID, search_results: searchResults},
        } as unknown as ReturnType<typeof makePost>);
    }

    // SearchSources renders the surviving source count next to the 'Sources' title.
    function expectSourceCount(count: number) {
        expect(screen.getByText('Sources')).toBeTruthy();
        expect(screen.getByText(String(count))).toBeTruthy();
    }

    test('renders the source list for a well-formed search_results prop', () => {
        renderWithSearchResults(JSON.stringify([wellFormedSource]));

        expectSourceCount(1);
    });

    // search_results is read straight off the post props, so a caller can put anything there.
    test.each([
        {name: 'not JSON at all', searchResults: 'not json'},
        {name: 'a JSON object', searchResults: '{}'},
        {name: 'not a string', searchResults: 5},
        {name: 'an array holding a null element', searchResults: '[null]'},
    ])('renders when search_results is $name', ({searchResults}) => {
        expect(() => renderWithSearchResults(searchResults)).not.toThrow();
    });

    test('keeps a well-formed source alongside an element that is not one', () => {
        renderWithSearchResults(JSON.stringify([wellFormedSource, null]));

        expectSourceCount(1);
    });

    // Mirrors maxMaxResults in api/api_search.go.
    const SERVER_RESULT_CAP = 100;

    test('caps the source list at the most results a search can return', () => {
        const sources = Array.from({length: SERVER_RESULT_CAP * 5}, (unused, index) => ({
            ...wellFormedSource,
            postId: index.toString(36).padStart(26, '0'),
        }));

        renderWithSearchResults(JSON.stringify(sources));

        expectSourceCount(SERVER_RESULT_CAP);
    });
});
