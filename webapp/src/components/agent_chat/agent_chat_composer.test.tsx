// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import type {LLMBot} from '@/bots';

import AgentChatComposer from './agent_chat_composer';
import {makeStore as makePluginStore} from './composer_plus_menu_test_harness';
import AgentChatDropZone from './agent_chat_drop_zone';

const mockCreatePost = jest.fn();
const mockUploadFile = jest.fn();
const mockGetCustomPrompts = jest.fn();
const mockRenderCustomPrompt = jest.fn();
const mockRewriteMessage = jest.fn();
jest.mock('@/client', () => {
    class FileUploadAbortedError extends Error {}
    return {
        createPost: (...args: unknown[]) => mockCreatePost(...args),
        uploadFile: (...args: unknown[]) => mockUploadFile(...args),
        getCustomPrompts: () => mockGetCustomPrompts(),
        getCustomPromptPins: () => Promise.resolve([]),
        renderCustomPrompt: (...args: unknown[]) => mockRenderCustomPrompt(...args),
        rewriteMessage: (...args: unknown[]) => mockRewriteMessage(...args),
        getAgentsBridgeStatus: () => Promise.resolve({available: true}),
        getFileThumbnailUrl: (id: string) => `/api/v4/files/${id}/thumbnail`,
        FileUploadAbortedError,
    };
});

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string | number>) => (
            defaultMessage.replace(/\{(\w+)(?:, ?\w+)?\}/g, (_m, k) => String(values?.[k] ?? ''))
        ),
        formatNumber: (n: number, opts?: Intl.NumberFormatOptions) => new Intl.NumberFormat('en', opts).format(n),
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

const MAX_FILE_SIZE = 1000;
const makeStore = (config: Record<string, string> = {MaxFileSize: String(MAX_FILE_SIZE), EnableFileAttachments: 'true'}) => createStore(() => ({
    entities: {users: {currentUserId: 'me'}, general: {config}},
}));
const store = makeStore();

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
    file_ids: [] as string[],
});

describe('AgentChatComposer', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        window.sessionStorage.clear();
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

    test('hints the send key while focused and empty, and not once there is text', () => {
        const {textbox} = renderComposer();
        const hint = () => screen.getByTestId('agent-chat-send-hint');

        // The composer takes focus on mount, so the hint shows right away; leaving it hides it.
        expect(hint().getAttribute('data-visible')).toBe('true');
        expect(hint().textContent).toBe('Enter to send');
        fireEvent.blur(textbox);
        expect(hint().getAttribute('data-visible')).toBe('false');
        fireEvent.focus(textbox);
        expect(hint().getAttribute('data-visible')).toBe('true');
        fireEvent.change(textbox, {target: {value: 'hello'}});
        expect(hint().getAttribute('data-visible')).toBe('false');
    });

    test('the hint fades after a few seconds', () => {
        jest.useFakeTimers();
        try {
            renderComposer();
            expect(screen.getByTestId('agent-chat-send-hint').getAttribute('data-visible')).toBe('true');
            act(() => {
                jest.advanceTimersByTime(5000);
            });
            expect(screen.getByTestId('agent-chat-send-hint').getAttribute('data-visible')).toBe('false');
        } finally {
            jest.useRealTimers();
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

// One controllable upload per uploadFile call: progress, resolve or reject it,
// and see whether the composer aborted it.
interface FakeUpload {
    file: File;
    channelId: string;
    progress: (percent: number) => void;
    resolve: (info: {id: string; has_preview_image?: boolean}) => void;
    reject: (e: unknown) => void;
    abort: jest.Mock;
}

const fakeUploads = (): FakeUpload[] => {
    const uploads: FakeUpload[] = [];
    mockUploadFile.mockImplementation((file: File, channelId: string, _clientId: string, onProgress: (p: number) => void) => {
        const d = deferred<{id: string}>();
        const upload = {file, channelId, progress: onProgress, resolve: d.resolve, reject: d.reject, abort: jest.fn()};
        uploads.push(upload);
        return {promise: d.promise, abort: upload.abort};
    });
    return uploads;
};

const file = (name: string, size = 10, type = 'text/plain') => new File(['x'.repeat(size)], name, {type});

const renderComposer = ({rootId = '', channelId = 'dm1', config, inDropZone = false}: {rootId?: string; channelId?: string; config?: Record<string, string>; inDropZone?: boolean} = {}) => {
    const onCreated = jest.fn();
    const composer = (
        <AgentChatComposer
            channelId={channelId}
            agentName='Matty'
            rootId={rootId}
            onCreated={onCreated}
        />
    );
    const {unmount} = render(
        <Provider store={config ? makeStore(config) : store}>
            {inDropZone ? <AgentChatDropZone testId='zone'>{composer}</AgentChatDropZone> : composer}
        </Provider>,
    );
    const textbox = screen.getByRole('textbox') as HTMLTextAreaElement;
    const send = screen.getByRole('button', {name: 'Send message'}) as HTMLButtonElement;
    return {onCreated, textbox, send, unmount};
};

const pick = async (...files: File[]) => {
    await act(async () => {
        fireEvent.change(screen.getByTestId('agent-chat-file-input'), {target: {files}});
    });
};

const cards = () => screen.queryAllByTestId('agent-chat-attachment');

// While the "+" menu is open its focus manager hides the rest of the page.
const plusButton = () => screen.getByTestId('composer-plus-menu-button');
const openPlusMenu = async () => {
    fireEvent.click(plusButton());
    await screen.findByRole('menu', {name: 'Add to message'});
};
const chooseUploadFiles = async () => {
    await openPlusMenu();
    fireEvent.click(screen.getByRole('menuitem', {name: 'Upload files'}));
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
};

const attachModes = [
    {mode: 'new chat', rootId: ''},
    {mode: 'reply', rootId: 'root0'},
];

describe('AgentChatComposer attachments', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        window.sessionStorage.clear();
        jest.spyOn(console, 'error').mockImplementation(() => null);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test.each(attachModes)('$mode: the pill is "+", then the textbox, then send', ({rootId}) => {
        renderComposer({rootId});
        const attach = screen.getByRole('button', {name: 'Add to message'});
        const textbox = screen.getByRole('textbox');
        const send = screen.getByRole('button', {name: 'Send message'});
        expect(attach.getAttribute('aria-haspopup')).toBe('menu');
        expect(attach.closest('[data-testid="agent-chat-composer-pill"]')).toBe(textbox.closest('[data-testid="agent-chat-composer-pill"]'));
        expect(attach.compareDocumentPosition(textbox) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(textbox.compareDocumentPosition(send) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    test('"Upload files" in the "+" menu opens the file picker', async () => {
        renderComposer();
        const input = screen.getByTestId('agent-chat-file-input') as HTMLInputElement;
        expect(input.type).toBe('file');
        expect(input.multiple).toBe(true);
        const click = jest.spyOn(input, 'click').mockImplementation(() => null);
        await chooseUploadFiles();
        expect(click).toHaveBeenCalledTimes(1);
    });

    test.each([
        {name: 'the DM channel is not known yet', channelId: '', config: {EnableFileAttachments: 'true', MaxFileSize: '1000'}},
        {name: 'the server disables file attachments', channelId: 'dm1', config: {EnableFileAttachments: 'false', MaxFileSize: '1000'}},
    ])('no attaching while $name', async ({channelId, config}) => {
        renderComposer({channelId, config});
        expect(screen.queryByTestId('agent-chat-file-input')).toBeNull();
        await openPlusMenu();
        expect(screen.queryByRole('menuitem', {name: 'Upload files'})).toBeNull();
        expect(screen.getByRole('menuitem', {name: 'Custom prompts'})).toBeTruthy();
    });

    test.each(attachModes)('$mode: a picked file uploads with progress, holds Send, then posts its file id and clears', async ({rootId}) => {
        const uploads = fakeUploads();
        mockCreatePost.mockResolvedValue({id: 'post1'});
        const {textbox, send, onCreated} = renderComposer({rootId});
        await pick(file('notes.txt'));

        expect(mockUploadFile).toHaveBeenCalledWith(expect.any(File), 'dm1', expect.stringMatching(/^[a-z0-9]{26}$/), expect.any(Function));
        expect(uploads[0].file.name).toBe('notes.txt');
        expect(cards()[0].textContent).toContain('Uploading… (0%)');

        await act(async () => {
            uploads[0].progress(40);
        });
        expect(cards()[0].textContent).toContain('Uploading… (40%)');

        // Send waits for the upload, even with text.
        fireEvent.change(textbox, {target: {value: 'see attached'}});
        expect(send.disabled).toBe(true);
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(mockCreatePost).not.toHaveBeenCalled();

        await act(async () => {
            uploads[0].resolve({id: 'file1'});
        });
        expect(cards()[0].getAttribute('data-status')).toBe('done');
        expect(send.disabled).toBe(false);

        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(mockCreatePost).toHaveBeenCalledWith({...expectedPost('see attached', rootId), file_ids: ['file1']});
        expect(onCreated).toHaveBeenCalledWith('post1');
        expect(cards()).toHaveLength(0);
        expect(textbox.value).toBe('');
    });

    test('files alone can be sent; nothing at all cannot', async () => {
        const uploads = fakeUploads();
        mockCreatePost.mockResolvedValue({id: 'post1'});
        const {send} = renderComposer();
        expect(send.disabled).toBe(true);
        await pick(file('a.txt'));
        await act(async () => {
            uploads[0].resolve({id: 'file1'});
        });
        await act(async () => {
            fireEvent.click(send);
        });
        expect(mockCreatePost).toHaveBeenCalledWith({...expectedPost(''), file_ids: ['file1']});
    });

    test('a failed post keeps the text and the attachments', async () => {
        const uploads = fakeUploads();
        mockCreatePost.mockRejectedValueOnce(new Error('500'));
        const {textbox} = renderComposer();
        await pick(file('a.txt'));
        await act(async () => {
            uploads[0].resolve({id: 'file1'});
        });
        fireEvent.change(textbox, {target: {value: 'hi'}});
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(screen.getByRole('alert').textContent).toBe('Couldn\'t send your message. Try again.');
        expect(cards()).toHaveLength(1);
        expect(textbox.value).toBe('hi');
    });

    test('a failed upload shows an error, holds Send, and can be retried', async () => {
        const uploads = fakeUploads();
        const {textbox, send} = renderComposer();
        fireEvent.change(textbox, {target: {value: 'hi'}});
        await pick(file('a.txt'));
        await act(async () => {
            uploads[0].reject(new Error('413'));
        });
        expect(screen.getByRole('alert').textContent).toBe('Couldn\'t upload this file.');
        expect(cards()[0].getAttribute('data-status')).toBe('failed');
        expect(send.disabled).toBe(true);

        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Retry uploading a.txt'}));
        });
        expect(mockUploadFile).toHaveBeenCalledTimes(2);
        await act(async () => {
            uploads[1].resolve({id: 'file1'});
        });
        expect(screen.queryByRole('alert')).toBeNull();
        expect(send.disabled).toBe(false);
    });

    test.each([
        {name: 'cancelling an upload aborts it', status: 'uploading', label: 'Cancel upload of a.txt', aborts: true},
        {name: 'removing an uploaded file', status: 'done', label: 'Remove a.txt', aborts: false},
        {name: 'removing a failed file', status: 'failed', label: 'Remove a.txt', aborts: false},
    ])('$name drops it from the post', async ({status, label, aborts}) => {
        const uploads = fakeUploads();
        mockCreatePost.mockResolvedValue({id: 'post1'});
        const {textbox} = renderComposer();
        await pick(file('a.txt'));
        await act(async () => {
            if (status === 'done') {
                uploads[0].resolve({id: 'file1'});
            } else if (status === 'failed') {
                uploads[0].reject(new Error('500'));
            }
        });
        fireEvent.click(screen.getByRole('button', {name: label}));
        expect(cards()).toHaveLength(0);
        expect(uploads[0].abort).toHaveBeenCalledTimes(aborts ? 1 : 0);

        fireEvent.change(textbox, {target: {value: 'hi'}});
        await act(async () => {
            fireEvent.keyDown(textbox, {key: 'Enter'});
        });
        expect(mockCreatePost).toHaveBeenCalledWith(expectedPost('hi'));
    });

    test('an upload in flight is cancelled when the composer unmounts', async () => {
        const uploads = fakeUploads();
        const {unmount} = renderComposer();
        await pick(file('a.txt'));
        unmount();
        expect(uploads[0].abort).toHaveBeenCalledTimes(1);
    });

    test('a file over MaxFileSize is shown as an error and never uploaded', async () => {
        fakeUploads();
        const {send, textbox} = renderComposer();
        fireEvent.change(textbox, {target: {value: 'hi'}});
        await pick(file('big.bin', MAX_FILE_SIZE + 1), file('ok.txt'));
        expect(mockUploadFile).toHaveBeenCalledTimes(1);
        expect(mockUploadFile.mock.calls[0][0].name).toBe('ok.txt');
        expect(cards().map((c) => c.getAttribute('data-status'))).toEqual(['too_large', 'uploading']);
        expect(screen.getByRole('alert').textContent).toContain('over the');
        expect(screen.queryByRole('button', {name: 'Retry uploading big.bin'})).toBeNull();
        expect(send.disabled).toBe(true);
    });

    test('at most 10 files per post; more are refused with an error', async () => {
        fakeUploads();
        renderComposer();
        await pick(...Array.from({length: 11}, (_v, i) => file(`f${i}.txt`)));
        expect(mockUploadFile).toHaveBeenCalledTimes(10);
        expect(cards()).toHaveLength(10);
        expect(screen.getByRole('alert').textContent).toBe('You can attach up to 10 files to a message.');

        // The "+" refuses to open the picker while the post is full.
        const input = screen.getByTestId('agent-chat-file-input') as HTMLInputElement;
        const click = jest.spyOn(input, 'click').mockImplementation(() => null);
        await chooseUploadFiles();
        expect(click).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'a pasted image uploads (named when the clipboard gives no name)', types: ['Files'], text: '', items: [{kind: 'file', type: 'image/png', file: new File(['png'], '', {type: 'image/png'})}], uploads: true},
        {name: 'pasted text is left to the textarea', types: ['text/plain', 'Files'], text: 'hello', items: [{kind: 'file', type: 'image/png', file: new File(['png'], 'a.png', {type: 'image/png'})}], uploads: false},
    ])('paste: $name', async ({types, text, items, uploads}) => {
        fakeUploads();
        const {textbox} = renderComposer();
        const clipboardData = {
            types,
            getData: () => text,
            items: items.map((i) => ({kind: i.kind, type: i.type, getAsFile: () => i.file})),
        };
        let event!: boolean;
        await act(async () => {
            event = fireEvent.paste(textbox, {clipboardData});
        });
        expect(mockUploadFile).toHaveBeenCalledTimes(uploads ? 1 : 0);
        expect(event).toBe(!uploads);
        if (uploads) {
            expect(mockUploadFile.mock.calls[0][0].name).toMatch(/^Image Pasted at \d{4}-\d{1,2}-\d{1,2} \d{2}-\d{2}\.png$/);
        }
    });

    test('drop: dragging files over the zone shows the overlay and a drop uploads them', async () => {
        fakeUploads();
        renderComposer({inDropZone: true});
        const zone = screen.getByTestId('zone');
        const dataTransfer = {types: ['Files'], files: [file('dropped.txt')], dropEffect: ''};

        fireEvent.dragEnter(zone, {dataTransfer});
        expect(screen.getByTestId('agent-chat-drop-overlay').textContent).toBe('Drop file to attach');

        await act(async () => {
            fireEvent.drop(zone, {dataTransfer});
        });
        expect(screen.queryByTestId('agent-chat-drop-overlay')).toBeNull();
        expect(mockUploadFile).toHaveBeenCalledTimes(1);
        expect(mockUploadFile.mock.calls[0][0].name).toBe('dropped.txt');
    });

    test.each([
        {name: 'a drag without files', types: ['text/plain'], channelId: 'dm1'},
        {name: 'a file drag before the DM channel is known', types: ['Files'], channelId: ''},
    ])('drop: $name is left alone', ({types, channelId}) => {
        fakeUploads();
        renderComposer({inDropZone: true, channelId});
        const zone = screen.getByTestId('zone');
        const dataTransfer = {types, files: [], dropEffect: ''};
        expect(fireEvent.dragEnter(zone, {dataTransfer})).toBe(true);
        expect(screen.queryByTestId('agent-chat-drop-overlay')).toBeNull();
        expect(fireEvent.drop(zone, {dataTransfer})).toBe(true);
        expect(mockUploadFile).not.toHaveBeenCalled();
    });
});

describe('AgentChatComposer "+" menu text', () => {
    const bot = {id: 'bot1', username: 'matty', displayName: 'Matty', dmChannelID: 'dm1', lastIconUpdate: 0, enabledMCPTools: null, autoEnableNewMCPTools: true} as unknown as LLMBot;
    const renderWithBot = (rootId = 'root0') => {
        // The plugin's reducers, so fetched custom prompts reach the menu.
        render(
            <Provider store={makePluginStore()}>
                <AgentChatComposer
                    channelId='dm1'
                    agentName='Matty'
                    rootId={rootId}
                    bot={bot}
                />
            </Provider>,
        );
        return screen.getByRole('textbox') as HTMLTextAreaElement;
    };
    const pickSubmenuItem = async (submenu: string, item: string) => {
        await openPlusMenu();
        fireEvent.click(screen.getByRole('menuitem', {name: new RegExp(`^${submenu}`)}));
        fireEvent.click(await screen.findByRole('menuitem', {name: item}));
        await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
    };

    beforeEach(() => {
        jest.clearAllMocks();
        window.sessionStorage.clear();
        mockGetCustomPrompts.mockResolvedValue([{id: 'p1', name: 'Standup'}]);
        jest.spyOn(console, 'error').mockImplementation(() => null);
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    test('a custom prompt replaces the text like an edit: draft kept, box focused, Send enabled', async () => {
        mockRenderCustomPrompt.mockResolvedValue({rendered: 'Write my standup\nfor today'});
        const textbox = renderWithBot();
        fireEvent.change(textbox, {target: {value: 'old'}});

        await pickSubmenuItem('Custom prompts', 'Standup');

        await waitFor(() => expect(textbox.value).toBe('Write my standup\nfor today'));
        await waitFor(() => expect(document.activeElement).toBe(textbox));
        expect(screen.getByTestId('agent-chat-composer-pill').dataset.expanded).toBe('true');
        expect((screen.getByRole('button', {name: 'Send message'}) as HTMLButtonElement).disabled).toBe(false);
        expect(JSON.stringify(Object.values(window.sessionStorage))).toContain('Write my standup');
    });

    test('a failed menu action shows inline under the pill and clears on the next edit', async () => {
        mockRenderCustomPrompt.mockRejectedValue(new Error('nope'));
        const textbox = renderWithBot();
        await pickSubmenuItem('Custom prompts', 'Standup');

        const alert = await screen.findByRole('alert');
        expect(alert.textContent).toBe('Couldn\'t insert the custom prompt. Try again.');
        expect(textbox.getAttribute('aria-describedby')).toBe(alert.id);

        fireEvent.change(textbox, {target: {value: 'x'}});
        expect(screen.queryByRole('alert')).toBeNull();
    });

    test('while a rewrite runs the box is read-only, Send waits and the status says so', async () => {
        let finish!: (text: string) => void;
        mockRewriteMessage.mockReturnValue(new Promise((resolve) => {
            finish = resolve;
        }));
        const textbox = renderWithBot();
        fireEvent.change(textbox, {target: {value: 'hello there'}});
        await pickSubmenuItem('AI Rewrite', 'Shorten');

        await waitFor(() => expect(textbox.readOnly).toBe(true));
        const send = screen.getByRole('button', {name: 'Send message'}) as HTMLButtonElement;
        expect(send.disabled).toBe(true);
        expect(screen.getByRole('status').textContent).toBe('Rewriting…');
        fireEvent.keyDown(textbox, {key: 'Enter'});
        expect(mockCreatePost).not.toHaveBeenCalled();

        await act(async () => finish('Hi.'));
        expect(textbox.value).toBe('Hi.');
        expect(textbox.readOnly).toBe(false);
        expect(send.disabled).toBe(false);
        expect(screen.getByRole('status').textContent).toBe('');
    });
});
