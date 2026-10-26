// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen} from '@testing-library/react';

import AgentChatThreadView from './agent_chat_thread_view';

const mockCreatePost = jest.fn();
jest.mock('@/client', () => ({
    createPost: (...args: unknown[]) => mockCreatePost(...args),
}));

const mockThreadViewerProps = jest.fn();
jest.mock('@/mm_webapp', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');
    return {
        ThreadViewer: (props: Record<string, unknown>) => {
            mockThreadViewerProps(props);
            return ReactActual.createElement('div', {'data-testid': 'thread-viewer'});
        },
    };
});

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl};
});

const store = createStore(() => ({entities: {users: {currentUserId: 'me'}}}));

const renderView = (props: Partial<React.ComponentProps<typeof AgentChatThreadView>> = {}) => render(
    <Provider store={store}>
        <AgentChatThreadView
            channelId='dm1'
            rootId='root1'
            agentName='Matty'
            layout='panel'
            isThreadView={false}
            testId='thread-pane'
            {...props}
        />
    </Provider>,
);

describe('AgentChatThreadView', () => {
    beforeEach(() => {
        jest.clearAllMocks();
    });

    test.each([
        {layout: 'page' as const, isThreadView: true},
        {layout: 'panel' as const, isThreadView: false},
    ])('$layout: renders the host thread and a reply composer that posts into the thread', async ({layout, isThreadView}) => {
        mockCreatePost.mockResolvedValue({id: 'reply1'});
        renderView({layout, isThreadView});

        expect(screen.getByTestId('thread-pane')).toBeTruthy();
        expect(mockThreadViewerProps).toHaveBeenCalledWith(expect.objectContaining({rootPostId: 'root1', isThreadView}));

        const reply = screen.getByRole('textbox', {name: 'Reply to Matty'}) as HTMLTextAreaElement;
        fireEvent.change(reply, {target: {value: 'more please'}});
        await act(async () => {
            fireEvent.keyDown(reply, {key: 'Enter'});
        });
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({channel_id: 'dm1', root_id: 'root1', message: 'more please'}));
        expect(reply.value).toBe('');
        expect(document.activeElement).toBe(reply);
    });

    test('waits for the channel before offering the composer', () => {
        renderView({channelId: ''});
        expect(screen.getByTestId('thread-viewer')).toBeTruthy();
        expect(screen.queryByRole('textbox')).toBeNull();
    });

    test('switching threads starts with an empty composer', () => {
        const {rerender} = renderView();
        fireEvent.change(screen.getByRole('textbox', {name: 'Reply to Matty'}), {target: {value: 'draft'}});
        rerender(
            <Provider store={store}>
                <AgentChatThreadView
                    channelId='dm1'
                    rootId='root2'
                    agentName='Matty'
                    layout='panel'
                    isThreadView={false}
                />
            </Provider>,
        );
        expect((screen.getByRole('textbox', {name: 'Reply to Matty'}) as HTMLTextAreaElement).value).toBe('');
    });
});
