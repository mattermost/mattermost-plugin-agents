// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen} from '@testing-library/react';

import PostEventListener from '@/websocket';

import AgentChatComposer from './agent_chat_composer';
import {resetGeneratingPosts} from './use_thread_generating';

const mockCreatePost = jest.fn();
const mockStop = jest.fn();
jest.mock('@/client', () => ({
    createPost: (...args: unknown[]) => mockCreatePost(...args),
    doStopGenerating: (...args: unknown[]) => mockStop(...args),
    uploadFile: jest.fn(),
    getProfilesByIds: () => Promise.resolve([]),
    getFileThumbnailUrl: (id: string) => `/api/v4/files/${id}/thumbnail`,
    FileUploadAbortedError: class extends Error {},
    Client4: {executeCommand: jest.fn()},
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string | number>) => (
            defaultMessage.replace(/\{(\w+)(?:, ?\w+)?\}/g, (_m, k) => String(values?.[k] ?? ''))
        ),
        formatNumber: (n: number) => String(n),
    };
    return {...actual, useIntl: () => intl};
});

// The plugin's websocket handler, as index.tsx registers it (fresh per test:
// it remembers per-post progress).
let websocket = new PostEventListener();
const event = (data: Record<string, unknown>) => act(() => {
    websocket.handlePostUpdateWebsockets({data} as never);
});

type TestPost = {id: string; channel_id: string; root_id: string; user_id: string; props: Record<string, unknown>};

const botPost = (overrides: Partial<TestPost> = {}): TestPost => ({
    id: 'bot1',
    channel_id: 'dm1',
    root_id: 'root0',
    user_id: 'agent',
    props: {llm_requester_user_id: 'me'},
    ...overrides,
});

const setup = ({posts = [botPost()], channelId = 'dm1', rootId = 'root0'}: {posts?: TestPost[]; channelId?: string; rootId?: string} = {}) => {
    const state = {
        entities: {
            users: {currentUserId: 'me', profiles: {me: {id: 'me'}, agent: {id: 'agent'}}},
            posts: {posts: Object.fromEntries([{id: 'root0', channel_id: channelId, root_id: '', user_id: 'me', props: {}}, ...posts].map((p) => [p.id, p])), postsInThread: {}},
            general: {config: {EnableFileAttachments: 'true', MaxFileSize: '1000'}},
        },
    };
    render(
        <Provider store={createStore(() => state)}>
            <AgentChatComposer
                channelId={channelId}
                rootId={rootId}
                agentName='Matty'
            />
        </Provider>,
    );
    return {textbox: screen.getByRole('textbox') as HTMLTextAreaElement};
};

const sendButton = () => screen.queryByRole('button', {name: 'Send message'});
const stopButton = () => screen.queryByRole('button', {name: 'Stop generating'});

describe('AgentChatComposer Stop', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        window.sessionStorage.clear();
        resetGeneratingPosts();
        websocket = new PostEventListener();
        jest.spyOn(console, 'error').mockImplementation(() => null);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('Send is a round 40px compass icon button', () => {
        setup();
        expect(sendButton()?.className).toMatch(/icon-button--size-medium/);
        expect(sendButton()?.className).toMatch(/icon-button--rounded/);
    });

    test.each([
        {name: 'start', events: [{post_id: 'bot1', control: 'start'}], stop: true},
        {name: 'a text chunk', events: [{post_id: 'bot1', next: 'Par'}], stop: true},
        {name: 'progress', events: [{post_id: 'bot1', control: 'progress', progress_seq: 1}], stop: true},
        {name: 'start, then end', events: [{post_id: 'bot1', control: 'start'}, {post_id: 'bot1', control: 'end'}], stop: false},
        {name: 'start, then cancel', events: [{post_id: 'bot1', control: 'start'}, {post_id: 'bot1', control: 'cancel'}], stop: false},
        {name: 'a tool status update only', events: [{post_id: 'bot1', control: 'tool_call'}], stop: false},
    ])('after $name the button is Stop: $stop', ({events, stop}) => {
        setup();
        events.forEach(event);
        expect(Boolean(stopButton())).toBe(stop);
        expect(Boolean(sendButton())).toBe(!stop);

        // Both round 40px buttons; only Send has the primary recolor.
        const button = (stop ? stopButton() : sendButton())!;
        expect(button.className).toMatch(/icon-button--size-medium/);
        expect(button.className).toMatch(/icon-button--rounded/);
        expect(button.getAttribute('data-emphasis')).toBe(stop ? null : 'primary');
        expect(button.getAttribute('aria-pressed')).toBeNull();
    });

    test.each([
        {name: 'another thread', post: botPost({root_id: 'root9'})},
        {name: 'another channel', post: botPost({channel_id: 'dm2'})},
        {name: 'another user\'s request', post: botPost({props: {llm_requester_user_id: 'someone'}})},
        {name: 'a post not loaded here', post: botPost({id: 'elsewhere'})},
    ])('a generation in $name keeps Send', ({post}) => {
        setup({posts: [post]});
        event({post_id: 'bot1', control: 'start'});
        expect(stopButton()).toBeNull();
    });

    test('a channel thread the agent was mentioned in gets Stop too', () => {
        setup({channelId: 'town', posts: [botPost({channel_id: 'town'})]});
        event({post_id: 'bot1', control: 'start'});
        expect(stopButton()).toBeTruthy();
    });

    test('a generation that began before the composer mounted still shows Stop', () => {
        event({post_id: 'bot1', control: 'start'});
        setup();
        expect(stopButton()).toBeTruthy();
    });

    test('Stop stops the generating post, then the button is Send again even as late chunks arrive', async () => {
        mockStop.mockResolvedValue(null);
        setup();
        event({post_id: 'bot1', control: 'start'});
        await act(async () => {
            fireEvent.click(stopButton()!);
        });
        expect(mockStop).toHaveBeenCalledWith('bot1');
        expect(sendButton()).toBeTruthy();
        expect(screen.getByRole('status').textContent).toBe('Stopped');

        event({post_id: 'bot1', next: 'late chunk'});
        expect(sendButton()).toBeTruthy();

        // A regenerate starts it again.
        event({post_id: 'bot1', control: 'start'});
        expect(stopButton()).toBeTruthy();
        expect(screen.getByRole('status').textContent).toBe('Generating…');
    });

    test('a failed Stop keeps Stop and shows the error', async () => {
        mockStop.mockRejectedValue(new Error('403'));
        setup();
        event({post_id: 'bot1', control: 'start'});
        await act(async () => {
            fireEvent.click(stopButton()!);
        });
        expect(screen.getByRole('alert').textContent).toBe('Couldn\'t stop generating. Try again.');
        expect(stopButton()).toBeTruthy();
    });

    test('Enter with text still sends while generating, and does not stop', async () => {
        mockCreatePost.mockResolvedValue({id: 'p2'});
        const {textbox} = setup();
        event({post_id: 'bot1', control: 'start'});
        fireEvent.change(textbox, {target: {value: 'one more thing'}});
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(mockCreatePost).toHaveBeenCalledWith(expect.objectContaining({message: 'one more thing', root_id: 'root0'}));
        expect(mockStop).not.toHaveBeenCalled();
        expect(stopButton()).toBeTruthy();
    });
});
