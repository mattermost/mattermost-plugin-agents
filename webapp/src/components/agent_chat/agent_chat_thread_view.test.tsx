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

let mockGenerating: string | null = null;
jest.mock('./use_thread_generating', () => ({
    useThreadGenerating: () => mockGenerating,
    markGenerationStopped: jest.fn(),
}));

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
        mockGenerating = null;
    });

    // The host's scroll container does not exist in the mocked ThreadViewer: render one by hand.
    const addScroller = (scrollTop: number) => {
        const pane = screen.getByTestId('thread-pane');
        const scroller = document.createElement('div');
        scroller.id = 'threadViewerScrollContainer';
        Object.defineProperty(scroller, 'scrollHeight', {configurable: true, value: 1000});
        Object.defineProperty(scroller, 'clientHeight', {configurable: true, value: 400});
        Object.defineProperty(scroller, 'scrollTop', {configurable: true, writable: true, value: scrollTop});
        scroller.scrollTo = jest.fn();
        pane.querySelector('[data-testid="thread-viewer"]')!.parentElement!.appendChild(scroller);
        return scroller;
    };
    const pill = () => screen.getByTestId('jump-to-latest');

    test.each([
        {name: 'scrolled up while the agent answers', generating: 'post1', scrollTop: 0, visible: 'true', responding: 'true'},
        {name: 'scrolled up with nothing generating', generating: null, scrollTop: 0, visible: 'true', responding: 'false'},
        {name: 'at the bottom while the agent answers', generating: 'post1', scrollTop: 600, visible: 'false', responding: 'true'},
    ])('jump button: $name', async ({generating, scrollTop, visible, responding}) => {
        mockGenerating = generating;
        renderView();
        const scroller = addScroller(scrollTop);

        await act(async () => {
            fireEvent.scroll(scroller);
        });

        expect(pill().getAttribute('data-visible')).toBe(visible);
        expect(pill().querySelector('button')?.getAttribute('data-responding')).toBe(responding);
    });

    test('the jump button turns from the wave into a plain arrow when the reply finishes, scrolls down, and hides at the bottom', async () => {
        mockGenerating = 'post1';
        const {rerender} = renderView();
        const scroller = addScroller(0);
        await act(async () => {
            fireEvent.scroll(scroller);
        });
        expect(pill().getAttribute('data-visible')).toBe('true');
        expect(pill().querySelector('button')?.getAttribute('data-responding')).toBe('true');

        // The reply finishes while the user is still scrolled up.
        mockGenerating = null;
        rerender(
            <Provider store={store}>
                <AgentChatThreadView
                    channelId='dm1'
                    rootId='root1'
                    agentName='Matty'
                    layout='panel'
                    isThreadView={false}
                    testId='thread-pane'
                />
            </Provider>,
        );
        expect(pill().getAttribute('data-visible')).toBe('true');
        const button = screen.getByRole('button', {name: 'Scroll to the latest message'});
        expect(button.getAttribute('data-responding')).toBe('false');

        fireEvent.click(button);
        expect(scroller.scrollTo).toHaveBeenCalledWith({top: 1000, behavior: 'smooth'});

        // Back at the bottom: gone.
        scroller.scrollTop = 600;
        await act(async () => {
            fireEvent.scroll(scroller);
        });
        expect(pill().getAttribute('data-visible')).toBe('false');
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
