// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen} from '@testing-library/react';

import AgentChatComposer from './agent_chat_composer';

const mockCreatePost = jest.fn();
jest.mock('@/client', () => ({
    createPost: (...args: unknown[]) => mockCreatePost(...args),
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

const deferred = <T, >() => {
    let resolve!: (v: T) => void;
    let reject!: (e: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return {promise, resolve, reject};
};

const store = createStore(() => ({entities: {users: {currentUserId: 'me'}}}));

// New-chat mode posts a root; reply mode posts into the thread.
const modes = [
    {mode: 'new chat', rootId: '', agentName: 'Matty', label: 'Message Matty', placeholder: 'Message Matty', testId: 'agent-chat-new-composer'},
    {mode: 'reply', rootId: 'root0', agentName: 'Matty', label: 'Reply to Matty', placeholder: 'Reply…', testId: 'agent-chat-reply-composer'},
    {mode: 'reply, agent unknown', rootId: 'root0', agentName: '', label: 'Reply', placeholder: 'Reply…', testId: 'agent-chat-reply-composer'},
];

const setup = ({rootId, agentName = 'Matty', label = 'Message Matty'}: {rootId?: string; agentName?: string; label?: string} = {}) => {
    const onCreated = jest.fn();
    render(
        <Provider store={store}>
            <AgentChatComposer
                channelId='dm1'
                agentName={agentName}
                onCreated={onCreated}
                {...(rootId ? {rootId} : {})}
            />
        </Provider>,
    );
    const textbox = screen.getByRole('textbox', {name: label}) as HTMLTextAreaElement;
    const send = screen.getByRole('button', {name: 'Send message'});
    return {onCreated, textbox, send};
};

const expectedPost = (message: string, rootId = '') => ({
    channel_id: 'dm1',
    root_id: rootId,
    message,
    pending_post_id: expect.stringMatching(/^me:\d+$/),
    props: {},
    file_ids: [],
});

describe('AgentChatComposer', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        jest.spyOn(console, 'error').mockImplementation(() => null);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('is focused on mount', () => {
        const {textbox} = setup();
        expect(document.activeElement).toBe(textbox);
    });

    test.each(modes)('$mode: names the textbox and posts with the right root, then clears and keeps focus', async ({rootId, agentName, label, placeholder, testId}) => {
        mockCreatePost.mockResolvedValue({id: 'post1'});
        const {textbox, onCreated} = setup({rootId, agentName, label});
        expect(textbox.placeholder).toBe(placeholder);
        expect(screen.getByTestId(testId)).toBeTruthy();

        fireEvent.change(textbox, {target: {value: 'hi'}});
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(mockCreatePost).toHaveBeenCalledWith(expectedPost('hi', rootId));
        expect(onCreated).toHaveBeenCalledWith('post1');
        expect(textbox.value).toBe('');
        expect(document.activeElement).toBe(textbox);
    });

    test('a retry of the same text reuses the pending post id; new text gets a new one', async () => {
        let now = 1000;
        jest.spyOn(Date, 'now').mockImplementation(() => now++);
        mockCreatePost.mockRejectedValueOnce(new Error('timeout'));
        const {textbox} = setup({rootId: 'root0', label: 'Reply to Matty'});
        fireEvent.change(textbox, {target: {value: 'hi'}});
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        mockCreatePost.mockRejectedValueOnce(new Error('timeout'));
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        const [first, retry] = mockCreatePost.mock.calls.map(([post]) => post.pending_post_id);
        expect(retry).toBe(first);

        mockCreatePost.mockResolvedValueOnce({id: 'r1'});
        await act(async () => {
            fireEvent.change(textbox, {target: {value: 'hi again'}});
        });
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(mockCreatePost.mock.calls[2][0].pending_post_id).not.toBe(first);
    });

    test.each([
        {name: 'empty', value: ''},
        {name: 'whitespace only', value: '   \n\t '},
    ])('does not send when $name', async ({value}) => {
        const {textbox, send} = setup();
        fireEvent.change(textbox, {target: {value}});
        expect((send as HTMLButtonElement).disabled).toBe(true);
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(mockCreatePost).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'Enter', event: {key: 'Enter'}, sends: true},
        {name: 'Shift+Enter (newline)', event: {key: 'Enter', shiftKey: true}, sends: false},
        {name: 'Enter while composing (IME)', event: {key: 'Enter', isComposing: true}, sends: false},
        {name: 'Enter with IME keyCode 229', event: {key: 'Enter', keyCode: 229}, sends: false},
        {name: 'another key', event: {key: 'a'}, sends: false},
    ])('keyboard: $name', async ({event, sends}) => {
        mockCreatePost.mockResolvedValue({id: 'root1'});
        const {textbox, onCreated} = setup();
        fireEvent.change(textbox, {target: {value: '  hello agent  '}});
        await act(async () => {
            fireEvent.keyDown(textbox, event);
        });
        if (sends) {
            expect(mockCreatePost).toHaveBeenCalledWith(expectedPost('hello agent'));
            expect(onCreated).toHaveBeenCalledWith('root1');
            expect(textbox.value).toBe('');
        } else {
            expect(mockCreatePost).not.toHaveBeenCalled();
            expect(textbox.value).toBe('  hello agent  ');
        }
    });

    test('the send button creates the root post', async () => {
        mockCreatePost.mockResolvedValue({id: 'root1'});
        const {textbox, send, onCreated} = setup();
        fireEvent.change(textbox, {target: {value: 'hi'}});
        await act(async () => {
            fireEvent.click(send);
        });
        expect(mockCreatePost).toHaveBeenCalledTimes(1);
        expect(onCreated).toHaveBeenCalledWith('root1');
    });

    test('a second submit while the first is in flight does not post twice', async () => {
        const pending = deferred<{id: string}>();
        mockCreatePost.mockReturnValue(pending.promise);
        const {textbox, send, onCreated} = setup();
        fireEvent.change(textbox, {target: {value: 'hi'}});

        fireEvent.keyDown(textbox, {key: 'Enter'});
        fireEvent.keyDown(textbox, {key: 'Enter'});
        fireEvent.click(send);
        expect(mockCreatePost).toHaveBeenCalledTimes(1);

        await act(async () => {
            pending.resolve({id: 'root1'});
        });
        expect(onCreated).toHaveBeenCalledTimes(1);
    });

    test('a failed post keeps the text, shows an error, and can be retried', async () => {
        mockCreatePost.mockRejectedValueOnce(new Error('500'));
        const {textbox, onCreated} = setup();
        fireEvent.change(textbox, {target: {value: 'hi'}});
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(screen.getByRole('alert').textContent).toBe('Couldn\'t send your message. Try again.');
        expect(textbox.value).toBe('hi');
        expect(onCreated).not.toHaveBeenCalled();

        mockCreatePost.mockResolvedValueOnce({id: 'root1'});
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(onCreated).toHaveBeenCalledWith('root1');
        expect(screen.queryByRole('alert')).toBeNull();
    });
});
