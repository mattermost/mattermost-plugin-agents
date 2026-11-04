// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    ArtifactBroker,
    ArtifactContext,
    ArtifactUser,
    cancelPendingConsent,
    colorSchemeFor,
    getConsentDecision,
    isConsentPromptPending,
    parseArtifactMessage,
    resetConsentStore,
    toArtifactUser,
} from './artifact_broker';

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

function setup(opts: {consent?: () => Promise<boolean>; fileId?: string; viewerId?: string; currentUser?: ArtifactUser | null; canPrompt?: () => boolean} = {}) {
    const target = makeTarget();
    const requestConsent = jest.fn(opts.consent ?? (() => Promise.resolve(true)));
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
    return {broker, target, requestConsent, dismissConsent, onReady, onHeartbeat, onResize, onEscape, send};
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
