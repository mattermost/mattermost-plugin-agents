// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

import {
    ArtifactBroker,
    ArtifactContext,
    ArtifactUser,
    ConsentRequest,
    cancelPendingConsent,
    colorSchemeFor,
    getConsentDecision,
    isConsentPromptPending,
    parseArtifactMessage,
    resetConsentStore,
    toArtifactUser,
} from './artifact_broker';
import {MAX_ARTIFACT_DOWNLOAD_BYTES, MAX_DOWNLOAD_FILENAME_LENGTH} from './artifact_download';

const context: ArtifactContext = {
    theme: {centerChannelBg: '#ffffff', centerChannelColor: '#3f4350'},
    colorScheme: 'light',
    displayMode: 'inline',
    locale: 'en',
};

const user: ArtifactUser = {
    username: 'alice',
    firstName: 'Alice',
    lastName: 'Liddell',
    nickname: 'al',
    displayName: 'Alice Liddell',
};

function makeTarget() {
    return {postMessage: jest.fn()} as unknown as Window & {postMessage: jest.Mock};
}

const TOKEN = 'bridge-token-1';

function setup(opts: {
    consent?: (request: ConsentRequest) => Promise<boolean>;
    fileId?: string;
    viewerId?: string;
    currentUser?: ArtifactUser | null;
    canPrompt?: () => boolean;
    saveDownload?: (blob: Blob, filename: string) => void;
} = {}) {
    const target = makeTarget();
    const requestConsent = jest.fn(opts.consent ?? (() => Promise.resolve(true)));
    const saveDownload = jest.fn(opts.saveDownload ?? (() => undefined)); // eslint-disable-line no-undefined
    const onReady = jest.fn();
    const onHeartbeat = jest.fn();
    const onResize = jest.fn();
    const onEscape = jest.fn();
    const dismissConsent = jest.fn();
    const broker = new ArtifactBroker({
        fileId: opts.fileId ?? 'file1',
        viewerId: opts.viewerId ?? 'u1',
        token: TOKEN,
        getTargetWindow: () => target,
        getContext: () => context,
        getCurrentUser: () => (typeof opts.currentUser === 'undefined' ? user : opts.currentUser),
        requestConsent,
        dismissConsent,
        saveDownload,
        canPrompt: opts.canPrompt,
        onReady,
        onHeartbeat,
        onResize,
        onEscape,
    });

    // Adds the bridge token to object payloads unless withToken is false.
    const send = (data: unknown, overrides: Partial<{source: unknown; origin: string}> = {}, withToken = true) => {
        const payload = withToken && typeof data === 'object' && data !== null ? {token: TOKEN, ...data} : data;
        return broker.handleMessage({data: payload, source: target, origin: 'null', ...overrides} as unknown as MessageEvent);
    };
    return {broker, target, requestConsent, dismissConsent, saveDownload, onReady, onHeartbeat, onResize, onEscape, send};
}

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

beforeEach(() => {
    resetConsentStore();
});

describe('ArtifactBroker message validation', () => {
    const cases: Array<{name: string; data: unknown; overrides?: Partial<{source: unknown; origin: string}>}> = [
        {name: 'wrong source', data: {mmArtifact: 1, type: 'ready'}, overrides: {source: makeTarget()}},
        {name: 'null source', data: {mmArtifact: 1, type: 'ready'}, overrides: {source: null}},
        {name: 'wrong origin', data: {mmArtifact: 1, type: 'ready'}, overrides: {origin: 'https://evil.example'}},
        {name: 'same origin as host', data: {mmArtifact: 1, type: 'ready'}, overrides: {origin: window.location.origin}},
        {name: 'missing version', data: {type: 'ready'}},
        {name: 'wrong version', data: {mmArtifact: 2, type: 'ready'}},
        {name: 'string payload', data: 'ready'},
        {name: 'null payload', data: null},
        {name: 'unknown type', data: {mmArtifact: 1, type: 'navigate'}},
        {name: 'resize without height', data: {mmArtifact: 1, type: 'resize'}},
        {name: 'resize with string height', data: {mmArtifact: 1, type: 'resize', height: '100'}},
        {name: 'resize with NaN height', data: {mmArtifact: 1, type: 'resize', height: NaN}},
        {name: 'request without id', data: {mmArtifact: 1, type: 'request', method: 'getCurrentUser'}},
        {name: 'request with numeric id', data: {mmArtifact: 1, type: 'request', id: 1, method: 'getCurrentUser'}},
        {name: 'request without method', data: {mmArtifact: 1, type: 'request', id: 'a'}},
        {name: 'request with array params', data: {mmArtifact: 1, type: 'request', id: 'a', method: 'getCurrentUser', params: []}},
    ];

    test.each(cases)('rejects $name', ({data, overrides}) => {
        const {send, target, onReady, onHeartbeat, onResize, requestConsent} = setup();
        expect(send(data, overrides)).toBe(false);
        expect(target.postMessage).not.toHaveBeenCalled();
        expect(onReady).not.toHaveBeenCalled();
        expect(onHeartbeat).not.toHaveBeenCalled();
        expect(onResize).not.toHaveBeenCalled();
        expect(requestConsent).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'wrong source', overrides: {source: makeTarget()}},
        {name: 'wrong origin', overrides: {origin: 'https://evil.example'}},
    ])('rejects a heartbeat from the $name', ({overrides}) => {
        const {send, onHeartbeat} = setup();
        expect(send({mmArtifact: 1, type: 'heartbeat'}, overrides)).toBe(false);
        expect(onHeartbeat).not.toHaveBeenCalled();
    });

    const messages: Array<{type: string; data: Record<string, unknown>}> = [
        {type: 'ready', data: {mmArtifact: 1, type: 'ready'}},
        {type: 'heartbeat', data: {mmArtifact: 1, type: 'heartbeat'}},
        {type: 'resize', data: {mmArtifact: 1, type: 'resize', height: 200}},
        {type: 'escape', data: {mmArtifact: 1, type: 'escape'}},
        {type: 'request', data: {mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'}},
    ];
    const badTokens: Array<{name: string; token?: unknown}> = [
        {name: 'missing token'},
        {name: 'empty token', token: ''},
        {name: 'wrong token', token: 'guessed-token'},
        {name: 'token prefix', token: TOKEN.slice(0, -1)},
        {name: 'non-string token', token: {toString: () => TOKEN}},
        {name: 'array token', token: [TOKEN]},
    ];
    const tokenCases = messages.flatMap((m) => badTokens.map((b) => ({...b, type: m.type, data: m.data})));

    test.each(tokenCases)('ignores $type with $name', ({data, token}) => {
        const {send, target, onReady, onHeartbeat, onResize, onEscape, requestConsent} = setup();
        const payload = typeof token === 'undefined' ? data : {...data, token};
        expect(send(payload, {}, false)).toBe(false);
        expect(target.postMessage).not.toHaveBeenCalled();
        expect(onReady).not.toHaveBeenCalled();
        expect(onHeartbeat).not.toHaveBeenCalled();
        expect(onResize).not.toHaveBeenCalled();
        expect(onEscape).not.toHaveBeenCalled();
        expect(requestConsent).not.toHaveBeenCalled();
    });

    test.each(messages)('accepts $type with the right token', ({data}) => {
        const {send} = setup();
        expect(send({...data, token: TOKEN}, {}, false)).toBe(true);
    });

    test.each([
        {type: 'ready', data: {mmArtifact: 1, type: 'ready', extra: 'x'}},
        {type: 'heartbeat', data: {mmArtifact: 1, type: 'heartbeat', token: TOKEN, extra: 'x'}},
    ])('parseArtifactMessage drops unknown fields of $type', ({type, data}) => {
        expect(parseArtifactMessage(data)).toEqual({mmArtifact: 1, type});
    });

    test('ignores messages after dispose', () => {
        const {broker, send, target, onHeartbeat} = setup();
        broker.dispose();
        expect(send({mmArtifact: 1, type: 'ready'})).toBe(false);
        expect(send({mmArtifact: 1, type: 'heartbeat'})).toBe(false);
        expect(target.postMessage).not.toHaveBeenCalled();
        expect(onHeartbeat).not.toHaveBeenCalled();
    });
});

describe('ArtifactBroker lifecycle', () => {
    test('ready sends the context', () => {
        const {send, target, onReady} = setup();
        expect(send({mmArtifact: 1, type: 'ready'})).toBe(true);
        expect(onReady).toHaveBeenCalledTimes(1);
        expect(target.postMessage).toHaveBeenCalledWith({mmArtifact: 1, type: 'context', context}, '*');
    });

    test('context is not sent before ready, and is resent after ready', () => {
        const {broker, send, target} = setup();
        broker.sendContext();
        expect(target.postMessage).not.toHaveBeenCalled();
        send({mmArtifact: 1, type: 'ready'});
        broker.sendContext();
        expect(target.postMessage).toHaveBeenCalledTimes(2);
    });

    test('heartbeat is reported to the host and answered with nothing', () => {
        const {send, target, onHeartbeat, onReady} = setup();
        expect(send({mmArtifact: 1, type: 'heartbeat'})).toBe(true);
        expect(send({mmArtifact: 1, type: 'heartbeat'})).toBe(true);
        expect(onHeartbeat).toHaveBeenCalledTimes(2);
        expect(onReady).not.toHaveBeenCalled();
        expect(target.postMessage).not.toHaveBeenCalled();
    });

    test('resize reports the height', () => {
        const {send, onResize} = setup();
        send({mmArtifact: 1, type: 'resize', height: 450});
        expect(onResize).toHaveBeenCalledWith(450);
    });

    test('escape is forwarded to the host', () => {
        const {send, onEscape} = setup();
        send({mmArtifact: 1, type: 'escape'});
        expect(onEscape).toHaveBeenCalledTimes(1);
    });

    test('unknown method returns unknown_method', () => {
        const {send, target} = setup();
        send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getAuthToken'});
        expect(target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            type: 'response',
            id: 'r1',
            error: expect.objectContaining({code: 'unknown_method'}),
        }), '*');
    });
});

describe('ArtifactBroker getCurrentUser', () => {
    const request = {mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'};

    test('allow returns the allowlisted profile and remembers the decision', async () => {
        const leaky = {...user, email: 'alice@example.com', roles: 'system_admin', authData: 'x'} as ArtifactUser;
        const {send, target, requestConsent} = setup({currentUser: leaky});
        send(request);
        await flush();
        send({mmArtifact: 1, type: 'heartbeat'});

        expect(requestConsent).toHaveBeenCalledTimes(1);
        const response = target.postMessage.mock.calls[0][0];
        expect(response).toEqual({mmArtifact: 1, type: 'response', id: 'r1', result: user});
        expect(JSON.stringify(response)).not.toContain('alice@example.com');
        expect(JSON.stringify(response)).not.toContain('system_admin');
        expect(getConsentDecision('u1', 'file1')).toBe('allow');

        send({...request, id: 'r2'});
        expect(requestConsent).toHaveBeenCalledTimes(1);
        expect(target.postMessage).toHaveBeenLastCalledWith(expect.objectContaining({id: 'r2', result: user}), '*');
    });

    test('deny returns permission_denied and remembers the decision', async () => {
        const {send, target, requestConsent} = setup({consent: () => Promise.resolve(false)});
        send(request);
        await flush();

        expect(target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            id: 'r1',
            error: expect.objectContaining({code: 'permission_denied'}),
        }), '*');
        expect(target.postMessage.mock.calls[0][0]).not.toHaveProperty('result');

        send({...request, id: 'r2'});
        expect(requestConsent).toHaveBeenCalledTimes(1);
        expect(target.postMessage).toHaveBeenLastCalledWith(expect.objectContaining({
            id: 'r2',
            error: expect.objectContaining({code: 'permission_denied'}),
        }), '*');
    });

    test('a second request while a prompt is pending gets busy', async () => {
        let resolve: (v: boolean) => void = () => undefined; // eslint-disable-line no-undefined
        const {send, target} = setup({consent: () => new Promise<boolean>((r) => {
            resolve = r;
        })});
        send(request);
        send({...request, id: 'r2'});

        expect(target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            id: 'r2',
            error: expect.objectContaining({code: 'busy'}),
        }), '*');

        resolve(true);
        await flush();
        send({mmArtifact: 1, type: 'heartbeat'});
        expect(target.postMessage).toHaveBeenLastCalledWith(expect.objectContaining({id: 'r1', result: user}), '*');
    });

    test('a pending prompt in another artifact makes requests busy', () => {
        const first = setup({fileId: 'file1', consent: () => new Promise<boolean>(() => undefined), // eslint-disable-line no-undefined
        });
        const second = setup({fileId: 'file2'});
        first.send(request);
        second.send(request);
        expect(second.requestConsent).not.toHaveBeenCalled();
        expect(second.target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            error: expect.objectContaining({code: 'busy'}),
        }), '*');
    });

    test('decisions are shared between brokers for the same file', async () => {
        const inline = setup({fileId: 'shared'});
        inline.send(request);
        await flush();

        const fullscreen = setup({fileId: 'shared'});
        fullscreen.send(request);
        expect(fullscreen.requestConsent).not.toHaveBeenCalled();
        expect(fullscreen.target.postMessage).toHaveBeenCalledWith(expect.objectContaining({result: user}), '*');
    });

    test('decisions are not shared between viewers of the same file', async () => {
        const alice = setup({fileId: 'shared', viewerId: 'alice'});
        alice.send(request);
        await flush();

        const bob = setup({fileId: 'shared', viewerId: 'bob', consent: () => Promise.resolve(false)});
        bob.send(request);
        expect(bob.requestConsent).toHaveBeenCalledTimes(1);
        await flush();
        expect(getConsentDecision('alice', 'shared')).toBe('allow');
        expect(getConsentDecision('bob', 'shared')).toBe('deny');
    });

    test('a dismissed prompt records no decision and denies the request', async () => {
        const {send, target} = setup({consent: () => Promise.reject(new Error('unmounted'))});
        send(request);
        await flush();
        expect(getConsentDecision('u1', 'file1')).toBeUndefined();
        expect(target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            error: expect.objectContaining({code: 'permission_denied'}),
        }), '*');
    });

    test('a frame that cannot show a prompt gets busy without prompting', () => {
        const {send, target, requestConsent} = setup({canPrompt: () => false});
        send(request);
        expect(requestConsent).not.toHaveBeenCalled();
        expect(isConsentPromptPending()).toBe(false);
        expect(target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            error: expect.objectContaining({code: 'busy', message: expect.stringContaining('interact with the artifact')}),
        }), '*');
    });

    test('a frame that cannot show a prompt still answers from a cached decision', async () => {
        const allowed = setup({fileId: 'cached'});
        allowed.send(request);
        await flush();

        const covered = setup({fileId: 'cached', canPrompt: () => false});
        covered.send(request);
        expect(covered.target.postMessage).toHaveBeenCalledWith(expect.objectContaining({result: user}), '*');
    });

    test('cancelling a pending prompt denies without a decision and frees the prompt for another frame', async () => {
        const never = () => new Promise<boolean>(() => undefined); // eslint-disable-line no-undefined
        const inline = setup({fileId: 'file1', consent: never});
        inline.send(request);
        expect(isConsentPromptPending()).toBe(true);

        // Another file's cancel leaves the prompt alone.
        cancelPendingConsent('other');
        await flush();
        expect(inline.dismissConsent).not.toHaveBeenCalled();
        expect(isConsentPromptPending()).toBe(true);

        cancelPendingConsent('file1');
        await flush();
        expect(inline.dismissConsent).toHaveBeenCalledTimes(1);
        expect(getConsentDecision('u1', 'file1')).toBeUndefined();
        expect(isConsentPromptPending()).toBe(false);
        expect(inline.target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            id: 'r1',
            error: expect.objectContaining({code: 'permission_denied'}),
        }), '*');

        const fullscreen = setup({fileId: 'file1'});
        fullscreen.send(request);
        await flush();
        fullscreen.send({mmArtifact: 1, type: 'heartbeat'});
        expect(fullscreen.requestConsent).toHaveBeenCalledTimes(1);
        expect(fullscreen.target.postMessage).toHaveBeenCalledWith(expect.objectContaining({result: user}), '*');
    });

    describe('a consented profile waits for the bridge heartbeat', () => {
        const heartbeat = {mmArtifact: 1, type: 'heartbeat'};
        const profileResponses = (target: {postMessage: jest.Mock}) =>
            target.postMessage.mock.calls.filter(([msg]) => msg.type === 'response' && 'result' in msg);

        test('is posted once, only after a valid heartbeat', async () => {
            const leaky = {...user, email: 'alice@example.com'} as ArtifactUser;
            const {send, target} = setup({currentUser: leaky});
            send(request);
            await flush();
            expect(target.postMessage).not.toHaveBeenCalled();

            // A heartbeat without the bridge token proves nothing.
            expect(send({...heartbeat, token: 'forged'}, {}, false)).toBe(false);
            expect(target.postMessage).not.toHaveBeenCalled();

            send(heartbeat);
            expect(profileResponses(target)).toEqual([[{mmArtifact: 1, type: 'response', id: 'r1', result: user}, '*']]);

            send(heartbeat);
            expect(profileResponses(target)).toHaveLength(1);
        });

        test('is never posted when the broker is disposed first', async () => {
            const {broker, send, target} = setup();
            send(request);
            await flush();
            broker.dispose();
            send(heartbeat);
            expect(target.postMessage).not.toHaveBeenCalled();
        });
    });

    test('missing user returns unavailable', async () => {
        const {send, target} = setup({currentUser: null});
        send(request);
        await flush();
        send({mmArtifact: 1, type: 'heartbeat'});
        expect(target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            error: expect.objectContaining({code: 'unavailable'}),
        }), '*');
    });
});

describe('ArtifactBroker download', () => {
    const download = (params?: Record<string, unknown>, id = 'd1') => ({mmArtifact: 1, type: 'request', id, method: 'download', ...(params ? {params} : {})});

    const readBytes = (blob: Blob) => new Promise<Uint8Array>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(new Uint8Array(reader.result as ArrayBuffer));
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(blob);
    });

    const errorCode = (target: {postMessage: jest.Mock}, id = 'd1') => {
        const call = target.postMessage.mock.calls.find(([msg]) => msg.type === 'response' && msg.id === id);
        return call?.[0].error?.code;
    };

    test('allow saves the file and resolves with its name, without remembering a decision', async () => {
        const {send, target, requestConsent, saveDownload} = setup();
        send(download({filename: 'report.csv', mimeType: 'text/csv', data: 'name,total\nAda,3\n'}));
        await flush();

        expect(requestConsent).toHaveBeenCalledWith({kind: 'download', filename: 'report.csv', mimeType: 'text/csv', size: 17});
        expect(saveDownload).toHaveBeenCalledTimes(1);
        const [blob, filename] = saveDownload.mock.calls[0];
        expect(filename).toBe('report.csv');
        expect(blob.type).toBe('text/csv');
        expect(String.fromCharCode(...await readBytes(blob))).toBe('name,total\nAda,3\n');
        expect(target.postMessage).toHaveBeenCalledWith({mmArtifact: 1, type: 'response', id: 'd1', result: {filename: 'report.csv'}}, '*');

        // Every download asks again, and a download grants no profile access.
        expect(getConsentDecision('u1', 'file1')).toBeUndefined();
        send(download({filename: 'again.csv', mimeType: 'text/csv', data: 'x'}, 'd2'));
        await flush();
        expect(requestConsent).toHaveBeenCalledTimes(2);
        expect(saveDownload).toHaveBeenCalledTimes(2);
    });

    const png = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
    const dataCases: Array<{name: string; params: () => Record<string, unknown>; mimeType: string; bytes: number[]}> = [
        {name: 'a UTF-8 string', params: () => ({mimeType: 'text/plain', data: 'h\u00e9'}), mimeType: 'text/plain', bytes: [0x68, 0xc3, 0xa9]},
        {name: 'a base64 data: URL from canvas.toDataURL()', params: () => ({data: `data:image/png;base64,${btoa(String.fromCharCode(...png))}`}), mimeType: 'image/png', bytes: png},
        {name: 'a percent-encoded data: URL', params: () => ({data: 'data:text/csv;charset=utf-8,a%2Cb%0A'}), mimeType: 'text/csv', bytes: [0x61, 0x2c, 0x62, 0x0a]},
        {name: 'a data: URL without a type, as text/plain', params: () => ({data: 'data:,hi'}), mimeType: 'text/plain', bytes: [0x68, 0x69]},
        {name: 'a string that only starts like a data: URL, as content', params: () => ({mimeType: 'text/plain', data: 'data: 1'}), mimeType: 'text/plain', bytes: [...'data: 1'].map((c) => c.charCodeAt(0))},
        {name: 'an ArrayBuffer', params: () => ({mimeType: 'application/pdf', data: new Uint8Array([0x25, 0x50, 0x44, 0x46]).buffer}), mimeType: 'application/pdf', bytes: [0x25, 0x50, 0x44, 0x46]},
        {name: 'only the bytes of a typed array view', params: () => ({mimeType: 'image/gif', data: new Uint8Array([9, 1, 2, 9]).subarray(1, 3)}), mimeType: 'image/gif', bytes: [1, 2]},
        {name: 'a Blob, typed by the Blob', params: () => ({data: new Blob(['{}'], {type: 'application/json'})}), mimeType: 'application/json', bytes: [0x7b, 0x7d]},
        {name: 'a Blob with an explicit mimeType, which wins', params: () => ({mimeType: 'text/plain', data: new Blob(['<b>x</b>'], {type: 'text/html'})}), mimeType: 'text/plain', bytes: [...'<b>x</b>'].map((c) => c.charCodeAt(0))},
        {name: 'a type with parameters and odd case', params: () => ({mimeType: 'Text/Markdown; charset=UTF-8', data: '#'}), mimeType: 'text/markdown', bytes: [0x23]},
        {name: 'the image/jpg alias', params: () => ({mimeType: 'image/jpg', data: 'x'}), mimeType: 'image/jpeg', bytes: [0x78]},
        {name: 'webp', params: () => ({mimeType: 'image/webp', data: 'x'}), mimeType: 'image/webp', bytes: [0x78]},
    ];

    test.each(dataCases)('saves $name with the allowlisted type', async ({params, mimeType, bytes}) => {
        const {send, saveDownload, requestConsent} = setup();
        send(download(params()));
        await flush();
        expect(requestConsent).toHaveBeenCalledWith(expect.objectContaining({kind: 'download', mimeType, size: bytes.length}));
        const [blob] = saveDownload.mock.calls[0];
        expect(blob.type).toBe(mimeType);
        expect(Array.from(await readBytes(blob))).toEqual(bytes);
    });

    const nameCases: Array<{name: string; filename: unknown; mimeType: string; expected: string}> = [
        {name: 'a matching extension is kept', filename: 'chart.png', mimeType: 'image/png', expected: 'chart.png'},
        {name: 'an alternate extension of the type is kept', filename: 'photo.jpeg', mimeType: 'image/jpeg', expected: 'photo.jpeg'},
        {name: 'the extension is lowercased', filename: 'Report.CSV', mimeType: 'text/csv', expected: 'Report.csv'},
        {name: 'a missing extension is added', filename: 'chart', mimeType: 'image/png', expected: 'chart.png'},
        {name: 'a mismatched extension gets the type\'s appended', filename: 'page.html', mimeType: 'text/plain', expected: 'page.html.txt'},
        {name: 'an executable extension gets the type\'s appended', filename: 'setup.exe', mimeType: 'application/pdf', expected: 'setup.exe.pdf'},
        {name: 'POSIX path separators are replaced', filename: '../../etc/passwd', mimeType: 'text/plain', expected: '_.._etc_passwd.txt'},
        {name: 'Windows path separators and reserved characters are replaced', filename: 'C:\\Users\\a<b>?.csv', mimeType: 'text/csv', expected: 'C__Users_a_b__.csv'},
        {name: 'control characters are removed', filename: 'a\u0000b\nc\u007f.txt', mimeType: 'text/plain', expected: 'abc.txt'},
        {name: 'bidi overrides and zero-width characters are removed', filename: 'invoice\u202Efdp.exe\u200B', mimeType: 'image/png', expected: 'invoicefdp.exe.png'},
        {name: 'leading dots are removed', filename: '.bashrc', mimeType: 'text/plain', expected: 'bashrc.txt'},
        {name: 'a missing name gets the default', filename: undefined, mimeType: 'image/png', expected: 'download.png'}, // eslint-disable-line no-undefined
        {name: 'a blank name gets the default', filename: '  ..  ', mimeType: 'application/json', expected: 'download.json'},
        {name: 'a name of only removed characters gets the default', filename: '\u0000\u202E', mimeType: 'text/csv', expected: 'download.csv'},
    ];

    test.each(nameCases)('filename: $name', async ({filename, mimeType, expected}) => {
        const {send, saveDownload, requestConsent} = setup();
        send(download({filename, mimeType, data: 'x'}));
        await flush();
        expect(requestConsent).toHaveBeenCalledWith(expect.objectContaining({filename: expected}));
        expect(saveDownload.mock.calls[0][1]).toBe(expected);
    });

    test('a long filename is capped and keeps its extension', async () => {
        const {send, saveDownload} = setup();
        send(download({filename: `${'\u{1F600}'.repeat(300)}.md`, mimeType: 'text/markdown', data: 'x'}));
        await flush();
        const filename: string = saveDownload.mock.calls[0][1];
        expect(Array.from(filename)).toHaveLength(MAX_DOWNLOAD_FILENAME_LENGTH);
        expect(filename.endsWith('\u{1F600}.md')).toBe(true);
    });

    const rejectCases: Array<{name: string; params?: () => Record<string, unknown>; code: string}> = [
        {name: 'no params', code: 'invalid_params'},
        {name: 'no data', params: () => ({filename: 'a.txt', mimeType: 'text/plain'}), code: 'invalid_params'},
        {name: 'numeric data', params: () => ({mimeType: 'text/plain', data: 42}), code: 'invalid_params'},
        {name: 'a plain object as data', params: () => ({mimeType: 'text/plain', data: {length: 1}}), code: 'invalid_params'},
        {name: 'a non-string filename', params: () => ({filename: ['a.txt'], mimeType: 'text/plain', data: 'x'}), code: 'invalid_params'},
        {name: 'a non-string mimeType', params: () => ({mimeType: {toString: () => 'text/plain'}, data: 'x'}), code: 'invalid_params'},
        {name: 'no type at all', params: () => ({data: 'x'}), code: 'invalid_params'},
        {name: 'a malformed base64 data: URL', params: () => ({data: 'data:image/png;base64,***'}), code: 'invalid_params'},
        {name: 'text/html', params: () => ({mimeType: 'text/html', data: '<script>1</script>'}), code: 'unsupported_type'},
        {name: 'application/xhtml+xml', params: () => ({mimeType: 'application/xhtml+xml', data: 'x'}), code: 'unsupported_type'},
        {name: 'image/svg+xml', params: () => ({mimeType: 'image/svg+xml', data: '<svg/>'}), code: 'unsupported_type'},
        {name: 'application/xml', params: () => ({mimeType: 'application/xml', data: 'x'}), code: 'unsupported_type'},
        {name: 'text/javascript', params: () => ({mimeType: 'text/javascript', data: 'x'}), code: 'unsupported_type'},
        {name: 'application/octet-stream', params: () => ({mimeType: 'application/octet-stream', data: 'x'}), code: 'unsupported_type'},
        {name: 'application/x-msdownload', params: () => ({mimeType: 'application/x-msdownload', data: 'x'}), code: 'unsupported_type'},
        {name: 'an HTML data: URL with no mimeType', params: () => ({data: 'data:text/html,<script>1</script>'}), code: 'unsupported_type'},
        {name: 'an SVG Blob with no mimeType', params: () => ({data: new Blob(['<svg/>'], {type: 'image/svg+xml'})}), code: 'unsupported_type'},
        {name: 'an allowed type with a smuggled suffix', params: () => ({mimeType: 'text/plain+html', data: 'x'}), code: 'unsupported_type'},
        {name: 'an ArrayBuffer over the size cap', params: () => ({mimeType: 'image/png', data: new ArrayBuffer(MAX_ARTIFACT_DOWNLOAD_BYTES + 1)}), code: 'too_large'},
        {name: 'a Blob over the size cap', params: () => ({mimeType: 'image/png', data: new Blob([new Uint8Array(MAX_ARTIFACT_DOWNLOAD_BYTES + 1)])}), code: 'too_large'},
        {name: 'a string that encodes to more than the size cap', params: () => ({mimeType: 'text/plain', data: '\u00e9'.repeat((MAX_ARTIFACT_DOWNLOAD_BYTES / 2) + 1)}), code: 'too_large'},
        {name: 'a string too long to decode', params: () => ({mimeType: 'text/plain', data: 'a'.repeat(MAX_ARTIFACT_DOWNLOAD_BYTES * 2)}), code: 'too_large'},
    ];

    test.each(rejectCases)('rejects $name with $code, without prompting or saving', async ({params, code}) => {
        const {send, target, requestConsent, saveDownload} = setup();
        send(download(params?.()));
        await flush();
        expect(errorCode(target)).toBe(code);
        expect(requestConsent).not.toHaveBeenCalled();
        expect(saveDownload).not.toHaveBeenCalled();
        expect(isConsentPromptPending()).toBe(false);
    });

    test('a file of exactly the size cap is allowed', async () => {
        const {send, saveDownload} = setup();
        send(download({mimeType: 'image/png', data: new ArrayBuffer(MAX_ARTIFACT_DOWNLOAD_BYTES)}));
        await flush();
        expect(saveDownload.mock.calls[0][0].size).toBe(MAX_ARTIFACT_DOWNLOAD_BYTES);
    });

    test('cancelling rejects with permission_denied and saves nothing, and the next download asks again', async () => {
        const {send, target, requestConsent, saveDownload} = setup({consent: () => Promise.resolve(false)});
        send(download({mimeType: 'text/plain', data: 'x'}));
        await flush();
        expect(errorCode(target)).toBe('permission_denied');
        expect(saveDownload).not.toHaveBeenCalled();
        expect(target.postMessage.mock.calls[0][0]).not.toHaveProperty('result');

        send(download({mimeType: 'text/plain', data: 'x'}, 'd2'));
        await flush();
        expect(requestConsent).toHaveBeenCalledTimes(2);
        expect(errorCode(target, 'd2')).toBe('permission_denied');
    });

    test('a prompt dismissed without an answer rejects with permission_denied and saves nothing', async () => {
        const never = () => new Promise<boolean>(() => undefined); // eslint-disable-line no-undefined
        const {send, target, saveDownload, dismissConsent} = setup({consent: never});
        send(download({mimeType: 'text/plain', data: 'x'}));
        expect(isConsentPromptPending()).toBe(true);
        cancelPendingConsent('file1');
        await flush();
        expect(dismissConsent).toHaveBeenCalledTimes(1);
        expect(errorCode(target)).toBe('permission_denied');
        expect(saveDownload).not.toHaveBeenCalled();
        expect(isConsentPromptPending()).toBe(false);
    });

    test('a frame the viewer is not interacting with gets busy, and its data is never decoded', async () => {
        const {send, target, requestConsent, saveDownload} = setup({canPrompt: () => false});
        send(download({data: 'data:image/png;base64,***'}));
        await flush();
        expect(target.postMessage).toHaveBeenCalledTimes(1);
        expect(target.postMessage).toHaveBeenCalledWith(expect.objectContaining({
            id: 'd1',
            error: expect.objectContaining({code: 'busy', message: expect.stringContaining('interact with the artifact')}),
        }), '*');
        expect(requestConsent).not.toHaveBeenCalled();
        expect(saveDownload).not.toHaveBeenCalled();
    });

    test('a second download while one is pending gets busy, and only the first is saved', async () => {
        let resolve: (v: boolean) => void = () => undefined; // eslint-disable-line no-undefined
        const {send, target, saveDownload} = setup({consent: () => new Promise<boolean>((r) => {
            resolve = r;
        })});
        send(download({filename: 'first.txt', mimeType: 'text/plain', data: '1'}));
        send(download({filename: 'second.txt', mimeType: 'text/plain', data: '2'}, 'd2'));
        expect(errorCode(target, 'd2')).toBe('busy');

        resolve(true);
        await flush();
        expect(saveDownload).toHaveBeenCalledTimes(1);
        expect(saveDownload.mock.calls[0][1]).toBe('first.txt');
    });

    test('a pending profile prompt in another artifact makes a download busy', () => {
        const first = setup({fileId: 'file1', consent: () => new Promise<boolean>(() => undefined)}); // eslint-disable-line no-undefined
        const second = setup({fileId: 'file2'});
        first.send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'});
        second.send(download({mimeType: 'text/plain', data: 'x'}));
        expect(second.requestConsent).not.toHaveBeenCalled();
        expect(errorCode(second.target)).toBe('busy');
    });

    test('a remembered profile decision does not skip the download prompt', async () => {
        const {send, requestConsent} = setup();
        send({mmArtifact: 1, type: 'request', id: 'r1', method: 'getCurrentUser'});
        await flush();
        expect(getConsentDecision('u1', 'file1')).toBe('allow');
        send(download({mimeType: 'text/plain', data: 'x'}));
        await flush();
        expect(requestConsent).toHaveBeenLastCalledWith(expect.objectContaining({kind: 'download'}));
        expect(requestConsent).toHaveBeenCalledTimes(2);
    });

    test('nothing is saved when the broker is disposed before the viewer answers', async () => {
        let resolve: (v: boolean) => void = () => undefined; // eslint-disable-line no-undefined
        const {broker, send, saveDownload} = setup({consent: () => new Promise<boolean>((r) => {
            resolve = r;
        })});
        send(download({mimeType: 'text/plain', data: 'x'}));
        broker.dispose();
        resolve(true);
        await flush();
        expect(saveDownload).not.toHaveBeenCalled();
    });

    test('a save that throws rejects with unavailable', async () => {
        const {send, target} = setup({saveDownload: () => {
            throw new Error('blocked');
        }});
        send(download({mimeType: 'text/plain', data: 'x'}));
        await flush();
        expect(errorCode(target)).toBe('unavailable');
    });

    test('the bridge checks the same size cap before cloning', () => {
        const bridge = fs.readFileSync(path.resolve(__dirname, '../../../../api/artifact_bridge.js'), 'utf8');
        const match = (/var MAX_DOWNLOAD_BYTES = ([\d\s*]+);/).exec(bridge);
        expect(match).not.toBeNull();
        expect(match![1].split('*').reduce((product, n) => product * Number(n), 1)).toBe(MAX_ARTIFACT_DOWNLOAD_BYTES);
    });
});

describe('toArtifactUser', () => {
    test('never copies email or other profile fields', () => {
        const profile = {
            id: 'u',
            username: 'bob',
            first_name: '',
            last_name: '',
            nickname: 'bobby',
            locale: 'fr',
            email: 'bob@example.com',
            roles: 'system_user',
            auth_data: 'secret',
            props: {a: 'b'},
        };
        const result = toArtifactUser(profile);
        expect(result).toEqual({
            username: 'bob', firstName: '', lastName: '', nickname: 'bobby', displayName: 'bobby',
        });
    });
});

describe('colorSchemeFor', () => {
    test.each([
        ['#ffffff', 'light'],
        ['#1f1f1f', 'dark'],
        ['#000', 'dark'],
        ['rgb(25, 27, 31)', 'dark'],
        ['', 'light'],
        ['not-a-color', 'light'],
    ])('%s is %s', (color, expected) => {
        expect(colorSchemeFor(color)).toBe(expected);
    });
});
