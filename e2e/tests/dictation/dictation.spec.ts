// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Live dictation: the admin picks an OpenAI service for dictation while the
// agent runs on another service, and the microphone button then shows in every
// message box. The browser sends its SDP offer to the plugin, which opens the
// session at a fake OpenAI realtime API and watches it over a sideband. Live
// WebRTC media to OpenAI isn't available in CI, so tests that need a running
// session stub the browser's peer connection. License gating isn't covered
// here: an unlicensed server runs as Entry, which counts as Enterprise.

import { expect, type Locator, type Page } from '@playwright/test';

import { containerTest } from 'helpers/container-fixture';
import { chooseCompassOption } from 'helpers/compass-select';
import { AgentAPIHelper } from 'helpers/agent-api';
import { AIPlugin } from 'helpers/ai-plugin';
import { MattermostPage } from 'helpers/mm';
import MattermostContainer, { getTownSquareChannel } from 'helpers/mmcontainer';
import { mattermostAIAdminConfigApiFromClient } from 'helpers/plugin-http';
import { AIMOCK_BOT_ID, AIMOCK_BOT_NAME, AIMOCK_SERVICE_ID, RunAIMockContainer } from 'helpers/plugincontainer';
import {
    REALTIME_MOCK_ALIAS,
    REALTIME_MOCK_API_URL,
    REALTIME_MOCK_UPSTREAM_ERROR,
    RunRealtimeMockSidecar,
} from 'helpers/realtime-mock-container';
import { SystemConsoleHelper } from 'helpers/system-console';

const adminUsername = 'admin';
const adminPassword = 'admin';
const username = 'regularuser';
const password = 'regularuser';

const DICTATION_SERVICE_ID = 'dictation-openai';
const DICTATION_SERVICE_NAME = 'OpenAI dictation';

const services = [
    {
        id: AIMOCK_SERVICE_ID,
        name: 'Aimock Service',
        type: 'openaicompatible',
        apiKey: 'mock',
        apiURL: 'http://openai:8080',
        defaultModel: 'gpt-mock',
        useResponsesAPI: false,
    },
    {
        id: DICTATION_SERVICE_ID,
        name: DICTATION_SERVICE_NAME,
        type: 'openai',
        apiKey: 'mock-dictation-key',
        apiURL: REALTIME_MOCK_API_URL,
        defaultModel: 'gpt-4o',
        useResponsesAPI: false,
    },
];

const test = containerTest(async (own) => {
    const mattermost = own(await RunAIMockContainer({
        allowedConnections: [REALTIME_MOCK_ALIAS],
        config: {
            services,
            bots: [{
                id: AIMOCK_BOT_ID,
                name: AIMOCK_BOT_NAME,
                displayName: 'Aimock Bot',
                customInstructions: '',
                serviceID: AIMOCK_SERVICE_ID,
                enabledNativeTools: [],
                disableTools: false,
            }],
        },
    }));
    const realtime = own(await RunRealtimeMockSidecar(mattermost.network));
    return { mattermost, realtime };
});

test.use({
    permissions: ['microphone'],
    launchOptions: { args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'] },
});

async function adminConfig(mattermost: MattermostContainer) {
    return mattermostAIAdminConfigApiFromClient(await mattermost.getAdminClient(), mattermost.url());
}

async function setDictationEnabled(mattermost: MattermostContainer, enabled: boolean) {
    await (await adminConfig(mattermost)).patch({
        dictation: { enabled, serviceID: enabled ? DICTATION_SERVICE_ID : '', model: 'gpt-live-transcribe', language: '' },
    });
}

const centerComposer = (page: Page) => page.getByTestId('channel_view');
const dictationButton = (scope: Locator) => scope.getByTestId('dictation-button');

// The polite live region next to the button announces listening and errors.
const dictationStatus = (scope: Locator) => dictationButton(scope).locator('xpath=..').getByRole('status');

// Calls a dictation route as the regular user with their API token.
async function dictationRoute(mattermost: MattermostContainer, method: 'GET' | 'POST', route: string, body?: unknown) {
    const client = await mattermost.getClient(username, password);
    const response = await fetch(`${mattermost.url()}/plugins/mattermost-ai/dictation/${route}`, {
        method,
        headers: { Authorization: `Bearer ${client.getToken()}`, ...(body ? { 'Content-Type': 'application/json' } : {}) },
        body: body ? JSON.stringify(body) : undefined, // eslint-disable-line no-undefined
        signal: AbortSignal.timeout(30000),
    });
    return { status: response.status, body: await response.json() };
}

const STUB_OFFER_SDP = 'v=0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\n';

// stubPeerConnection replaces the browser's RTCPeerConnection with one that
// produces a fixed offer and opens its data channel once the answer is
// applied, so a session reaches listening without WebRTC media.
async function stubPeerConnection(page: Page) {
    await page.addInitScript((offerSDP) => {
        class FakeChannel extends EventTarget {
            readyState = 'connecting';
            send() { /* commits go nowhere */ }
            close() {
                this.readyState = 'closed';
            }
        }
        class FakePeerConnection extends EventTarget {
            connectionState = 'new';
            channel = new FakeChannel();
            addTrack() { /* no media */ }
            createDataChannel() {
                return this.channel;
            }
            async createOffer() {
                return { type: 'offer', sdp: offerSDP };
            }
            async setLocalDescription() { /* accepted */ }
            async setRemoteDescription() {
                setTimeout(() => {
                    this.channel.readyState = 'open';
                    this.channel.dispatchEvent(new Event('open'));
                }, 0);
            }
            close() { /* closed */ }
        }
        (window as unknown as { RTCPeerConnection: unknown }).RTCPeerConnection = FakePeerConnection;
    }, STUB_OFFER_SDP);
}

async function loginAsUser(page: Page, mattermost: MattermostContainer) {
    const mmPage = new MattermostPage(page);
    await mmPage.login(mattermost.url(), username, password, { channelViewTimeoutMs: 90000 });
    await expect(mmPage.postTextbox).toBeVisible({ timeout: 30000 });

    // Browsers expose the microphone only to secure origins. Testcontainers
    // can report the Docker gateway IP as the host, which isn't one.
    expect(
        await page.evaluate(() => window.isSecureContext),
        `${mattermost.url()} is not a secure context; set TESTCONTAINERS_HOST_OVERRIDE=localhost when running e2e inside a container`,
    ).toBe(true);
    return mmPage;
}

test.describe('Dictation', () => {
    // Each test starts from dictation enabled on the OpenAI service and a
    // provider that mints successfully, whatever ran before it.
    test.beforeEach(async ({ containers: { mattermost, realtime } }) => {
        await setDictationEnabled(mattermost, true);
        await realtime.setMode('ok');
    });

    // A stubbed peer never tells the provider it closed, so the sidebands are
    // closed here, as OpenAI does, to free the user's session slots.
    test.afterEach(async ({ containers: { realtime } }) => {
        await realtime.closeSidebands();
    });

    test.skip(({ browserName }) => browserName !== 'chromium', 'Fake media devices are Chromium flags');

    test('admin enables dictation on an OpenAI service while the agent uses another service', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(120000);
        await setDictationEnabled(mattermost, false);
        const mmPage = new MattermostPage(page);
        const systemConsole = new SystemConsoleHelper(page);

        await mmPage.login(mattermost.url(), adminUsername, adminPassword);
        await systemConsole.navigateToAgentsSettings(mattermost.url(), 'dictation');

        const enableGroup = page.getByRole('radiogroup', { name: 'Enable dictation' });
        await enableGroup.scrollIntoViewIfNeeded();
        await enableGroup.getByLabel('True').check();

        const serviceSelect = page.getByRole('combobox', { name: 'Transcription service', exact: true });
        await serviceSelect.click();
        const options = page.getByRole('listbox', { name: 'Transcription service' }).getByRole('option');
        await expect(options).toHaveText([DICTATION_SERVICE_NAME]);
        await page.keyboard.press('Escape');
        await chooseCompassOption(serviceSelect, DICTATION_SERVICE_NAME);

        await Promise.all([
            page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().includes('/admin/config') && response.ok()),
            systemConsole.getSaveButton().click(),
        ]);

        const saved = await (await adminConfig(mattermost)).get();
        expect(saved.dictation).toEqual(expect.objectContaining({
            enabled: true,
            serviceID: DICTATION_SERVICE_ID,
            model: 'gpt-live-transcribe',
        }));
        const admin = await mattermost.getAdminClient();
        const agents = await new AgentAPIHelper(mattermost.url()).getAgents(admin.getToken());
        expect(agents.find((a) => a.name === AIMOCK_BOT_NAME)?.serviceID).toBe(AIMOCK_SERVICE_ID);
    });

    test('the button shows in the channel, thread and agent chat message boxes', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(120000);

        const channel = await getTownSquareChannel(mattermost, username, password);
        const client = await mattermost.getClient(username, password);
        const root = await client.createPost({ channel_id: channel.id, message: 'Root post for dictation' } as any);
        await client.createPost({ channel_id: channel.id, root_id: root.id, message: 'A reply' } as any);

        await loginAsUser(page, mattermost);

        const center = centerComposer(page);
        await expect(dictationButton(center)).toBeVisible({ timeout: 30000 });
        await expect(dictationButton(center)).toHaveAttribute('aria-label', 'Start dictation');
        await expect(dictationButton(center)).toHaveAttribute('aria-pressed', 'false');

        const rootPost = page.locator('[id^="post_"]').filter({ hasText: 'Root post for dictation' }).last();
        await rootPost.getByText(/1 repl/i).click();
        const thread = page.locator('#rhsContainer');
        await expect(dictationButton(thread)).toBeVisible({ timeout: 30000 });

        const aiPlugin = new AIPlugin(page);
        await aiPlugin.openRHS();
        await aiPlugin.ensureRhsNewChatTab();
        await expect(dictationButton(page.getByTestId('mattermost-ai-rhs'))).toBeVisible({ timeout: 30000 });
    });

    test('a provider failure shows an error without the upstream body', async ({ page, containers: { mattermost, realtime } }) => {
        test.setTimeout(120000);
        await realtime.setMode('fail');
        try {
            await loginAsUser(page, mattermost);
            const center = centerComposer(page);

            const session = page.waitForResponse((response) => response.url().endsWith('/dictation/session'));
            await dictationButton(center).click();
            expect((await session).status()).toBe(502);

            await expect(dictationButton(center)).toHaveAttribute('data-status', 'error');
            await expect(dictationStatus(center)).toHaveText('The speech service could not be reached.');
            await expect(dictationButton(center)).toHaveAttribute('aria-label', 'Start dictation');

            // The webapp never reads the error body, so check it directly.
            const direct = await dictationRoute(mattermost, 'POST', 'session', { sdp: STUB_OFFER_SDP });
            expect(direct.status).toBe(502);
            expect(JSON.stringify(direct.body)).not.toContain(REALTIME_MOCK_UPSTREAM_ERROR);
        } finally {
            await realtime.setMode('ok');
        }
    });

    test('the browser\'s offer goes through the plugin, which opens the session at the provider', async ({ page, containers: { mattermost, realtime } }) => {
        test.setTimeout(120000);
        await stubPeerConnection(page);
        const callsBefore = (await realtime.state()).calls.length;
        const providerRequests: string[] = [];
        page.on('request', (request) => {
            if (request.url().includes(`${REALTIME_MOCK_ALIAS}:`) || request.url().includes('openai.com')) {
                providerRequests.push(request.url());
            }
        });

        await loginAsUser(page, mattermost);
        const center = centerComposer(page);
        const sessionRequest = page.waitForRequest((request) => request.url().endsWith('/dictation/session') && request.method() === 'POST');
        await dictationButton(center).click();
        expect(JSON.parse((await sessionRequest).postData() ?? '{}')).toEqual({ sdp: STUB_OFFER_SDP });
        await expect(dictationButton(center)).toHaveAttribute('data-status', 'listening', { timeout: 30000 });

        // The plugin made the provider call with the service key and the
        // configured session, and attached its sideband.
        const state = await realtime.state();
        expect(state.calls.length).toBe(callsBefore + 1);
        const call = state.calls[state.calls.length - 1];
        expect(call.sdp).toBe(STUB_OFFER_SDP);
        expect(call.authorization).toBe('Bearer mock-dictation-key');
        expect(call.session.type).toBe('transcription');
        expect(call.session.audio.input.transcription.model).toBe('gpt-live-transcribe');
        expect(call.session.audio.input.turn_detection).toBeNull();
        expect(state.openSidebands).toContain(call.callId);

        // The browser never contacted the provider and never held its key.
        expect(providerRequests).toEqual([]);

        await dictationButton(center).click();
        await expect(dictationButton(center)).toHaveAttribute('data-status', 'idle', { timeout: 10000 });
    });

    test('a session whose settings change is ended by the server, and the user is told why', async ({ page, containers: { mattermost, realtime } }) => {
        test.setTimeout(120000);
        await stubPeerConnection(page);
        await loginAsUser(page, mattermost);
        const center = centerComposer(page);
        await dictationButton(center).click();
        await expect(dictationButton(center)).toHaveAttribute('data-status', 'listening', { timeout: 30000 });

        const { calls } = await realtime.state();
        const callId = calls[calls.length - 1].callId;

        // A modified client switched the model: the provider echoes the change on the sideband.
        await realtime.sendSideband(callId, {
            type: 'session.updated',
            session: {
                type: 'transcription',
                object: 'realtime.transcription_session',
                audio: { input: { transcription: { model: 'gpt-4o-transcribe', language: null, languages: null, prompt: null }, noise_reduction: { type: 'near_field' }, turn_detection: null } },
            },
        });

        await expect(dictationButton(center)).toHaveAttribute('data-status', 'error', { timeout: 30000 });
        await expect(dictationStatus(center)).toHaveText('Dictation stopped because its session settings were changed. Start it again.');
        await expect.poll(async () => (await realtime.state()).hangups).toContain(callId);
    });

    test('while listening, the glow follows the live microphone level', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(120000);

        // The microphone is Chrome's fake device, whose periodic beep runs
        // through the real AudioContext meter.
        await stubPeerConnection(page);

        await loginAsUser(page, mattermost);
        const center = centerComposer(page);
        await dictationButton(center).click();
        await expect(dictationButton(center)).toHaveAttribute('data-status', 'listening', { timeout: 30000 });

        // Over 3 s the beep must drive the glow up and back down.
        const samples = await page.evaluate(async () => {
            const anchor = document.querySelector('[data-testid="channel_view"] [data-testid="dictation-indicator"]') as HTMLElement;
            const button = document.querySelector('[data-testid="channel_view"] [data-testid="dictation-button"]') as HTMLElement;
            const out: Array<{ level: number; spread: number }> = [];
            for (let i = 0; i < 60; i++) {
                const spread = Number((/(\d+(?:\.\d+)?)px\s*$/).exec(getComputedStyle(button).boxShadow)?.[1] ?? 0);
                out.push({ level: Number(anchor.style.getPropertyValue('--dictation-level') || 0), spread });
                await new Promise((r) => setTimeout(r, 50));
            }
            return out;
        });
        const levels = samples.map((s) => s.level);
        const spreads = samples.map((s) => s.spread);
        expect(Math.max(...levels)).toBeGreaterThan(0.5);
        expect(Math.min(...levels)).toBeLessThan(0.3);
        expect(Math.max(...spreads)).toBeGreaterThan(6);

        await dictationButton(center).click();
        await expect(dictationButton(center)).toHaveAttribute('data-status', 'idle', { timeout: 10000 });
    });

    test('turning dictation off and on updates open message boxes without a reload', async ({ page, containers: { mattermost } }) => {
        test.setTimeout(120000);
        const api = await adminConfig(mattermost);
        const current = await api.get();
        const center = centerComposer(page);

        await loginAsUser(page, mattermost);
        await expect(dictationButton(center)).toBeVisible({ timeout: 30000 });

        try {
            await api.patch({ dictation: { ...(current.dictation as object), enabled: false } });
            expect((await dictationRoute(mattermost, 'GET', 'availability')).body.reason).toBe('disabled');
            await expect(dictationButton(center)).toHaveCount(0, { timeout: 30000 });
        } finally {
            await api.patch({ dictation: { ...(current.dictation as object), enabled: true } });
        }
        await expect(dictationButton(center)).toBeVisible({ timeout: 30000 });
    });
});
