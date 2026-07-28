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
