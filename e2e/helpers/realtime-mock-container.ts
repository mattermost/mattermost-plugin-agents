// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import os from 'os';
import path from 'path';

import { GenericContainer, StartedNetwork, StartedTestContainer, Wait } from 'testcontainers';

import { withDeadline } from './deadline';

export const REALTIME_MOCK_IMAGE = 'node:20-alpine';
export const REALTIME_MOCK_PORT = 8095;
export const REALTIME_MOCK_ALIAS = 'realtime';

// The OpenAI service APIURL that points dictation at this mock from inside the Mattermost network.
export const REALTIME_MOCK_API_URL = `http://${REALTIME_MOCK_ALIAS}:${REALTIME_MOCK_PORT}`;

// The browser POSTs its SDP offer here; the hostname only resolves inside the
// Docker network, so specs intercept it with page.route.
export const REALTIME_MOCK_CALLS_URL = `${REALTIME_MOCK_API_URL}/v1/realtime/calls`;

export const REALTIME_MOCK_CLIENT_SECRET = 'ek_e2e_client_secret';
export const REALTIME_MOCK_UPSTREAM_ERROR = 'UPSTREAM_SENTINEL_BODY';

export type RealtimeMockMode = 'ok' | 'fail';

export type RecordedMint = {
    path: string;
    authorization: string;
    body: any;
};

function buildServerScript(): string {
    return `
const http = require('http');

let mode = 'ok';
const mints = [];

function readBody(req) {
  return new Promise((resolve) => {
    let data = '';
    req.on('data', (chunk) => { data += chunk; });
    req.on('end', () => resolve(data));
  });
}

const server = http.createServer(async (req, res) => {
  const body = await readBody(req);
  if (req.method === 'POST' && req.url === '/__mode') {
    mode = JSON.parse(body).mode;
    res.writeHead(204);
    res.end();
    return;
  }
  if (req.method === 'GET' && req.url === '/__mints') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(mints));
    return;
  }
  if (req.method === 'POST' && req.url === '/v1/realtime/client_secrets') {
    let parsed = null;
    try { parsed = JSON.parse(body); } catch (e) { parsed = body; }
    mints.push({ path: req.url, authorization: req.headers.authorization || '', body: parsed });
    if (mode === 'fail') {
      res.writeHead(500, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { message: '${REALTIME_MOCK_UPSTREAM_ERROR}' } }));
      return;
    }
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      value: '${REALTIME_MOCK_CLIENT_SECRET}',
      expires_at: Math.floor(Date.now() / 1000) + 15,
      session: { type: 'transcription' },
    }));
    return;
  }
  res.writeHead(404);
  res.end();
});

server.listen(${REALTIME_MOCK_PORT}, '0.0.0.0');
`.trim();
}

/** A fake OpenAI realtime API that mints client secrets, or fails on demand. */
export class RealtimeMockContainer {
    private container: StartedTestContainer | null = null;
    private serverDir: string | null = null;

    async start(network: StartedNetwork): Promise<void> {
        this.serverDir = fs.mkdtempSync(path.join(os.tmpdir(), 'realtime-mock-'));
        fs.writeFileSync(path.join(this.serverDir, 'server.js'), buildServerScript());

        try {
            this.container = await withDeadline('Starting the realtime mock container', 5 * 60000, () => new GenericContainer(REALTIME_MOCK_IMAGE)
                .withExposedPorts(REALTIME_MOCK_PORT)
                .withNetwork(network)
                .withNetworkAliases(REALTIME_MOCK_ALIAS)
                .withBindMounts([{ source: this.serverDir!, target: '/app', mode: 'ro' }])
                .withCommand(['node', '/app/server.js'])
                .withWaitStrategy(Wait.forListeningPorts())
                .withStartupTimeout(120000)
                .start());
        } catch (error) {
            this.removeServerDir();
            throw error;
        }
    }

    private controlURL(pathname: string): string {
        if (!this.container) {
            throw new Error('The realtime mock is not running');
        }
        return `http://${this.container.getHost()}:${this.container.getMappedPort(REALTIME_MOCK_PORT)}${pathname}`;
    }

    async setMode(mode: RealtimeMockMode): Promise<void> {
        const response = await fetch(this.controlURL('/__mode'), {
            method: 'POST',
            body: JSON.stringify({ mode }),
            signal: AbortSignal.timeout(10000),
        });
        if (!response.ok) {
            throw new Error(`Setting the realtime mock mode failed with ${response.status}`);
        }
    }

    async mints(): Promise<RecordedMint[]> {
        const response = await fetch(this.controlURL('/__mints'), { signal: AbortSignal.timeout(10000) });
        if (!response.ok) {
            throw new Error(`Reading realtime mock mints failed with ${response.status}`);
        }
        return response.json();
    }

    async stop(): Promise<void> {
        try {
            if (this.container) {
                await this.container.stop();
            }
        } finally {
            this.container = null;
            this.removeServerDir();
        }
    }

    private removeServerDir(): void {
        if (this.serverDir && fs.existsSync(this.serverDir)) {
            fs.rmSync(this.serverDir, { recursive: true, force: true });
        }
        this.serverDir = null;
    }
}

export async function RunRealtimeMockSidecar(network: StartedNetwork): Promise<RealtimeMockContainer> {
    const mock = new RealtimeMockContainer();
    await mock.start(network);
    return mock;
}
