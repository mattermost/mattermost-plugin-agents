// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import AgentNewChatComposer from './agent_new_chat_composer';

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

const setup = () => {
    const onCreated = jest.fn();
    render(
        <AgentNewChatComposer
            channelId='dm1'
            agentName='Matty'
            onCreated={onCreated}
        />,
    );
    const textbox = screen.getByRole('textbox', {name: 'Message Matty'}) as HTMLTextAreaElement;
    const send = screen.getByRole('button', {name: 'Send message'});
    return {onCreated, textbox, send};
};

describe('AgentNewChatComposer', () => {
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
            expect(mockCreatePost).toHaveBeenCalledWith({channel_id: 'dm1', root_id: '', message: 'hello agent', props: {}, file_ids: []});
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
