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

export const REALTIME_MOCK_UPSTREAM_ERROR = 'UPSTREAM_SENTINEL_BODY';

// The SDP answer the mock returns for every call.
export const REALTIME_MOCK_ANSWER_SDP = 'v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n';

export type RealtimeMockMode = 'ok' | 'fail';

// One POST /v1/realtime/calls the plugin server made.
export type RecordedCall = {
    callId: string;
    authorization: string;
    sdp: string;
    session: any;
};

export type MockState = {
    calls: RecordedCall[];
    hangups: string[];
    sidebands: string[];
    openSidebands: string[];
};

// A fake OpenAI realtime API for the plugin's server-side session flow:
// POST /v1/realtime/calls (multipart sdp + session) answers with SDP and a
// Location header naming the call; GET /v1/realtime?call_id= upgrades to a
// minimal WebSocket sideband; POST /v1/realtime/calls/<id>/hangup closes it.
// Closing a sideband is what frees the user's session slot on the server.
function buildServerScript(): string {
    return `
const http = require('http');
const crypto = require('crypto');

let mode = 'ok';
let nextCall = 1;
const calls = [];
const hangups = [];
const sidebandLog = [];
const sidebands = new Map();

function readBody(req) {
  return new Promise((resolve) => {
    let data = '';
    req.on('data', (chunk) => { data += chunk; });
    req.on('end', () => resolve(data));
  });
}

function formField(body, name) {
  const m = body.match(new RegExp('name="' + name + '"\\\\r\\\\n\\\\r\\\\n([\\\\s\\\\S]*?)\\\\r\\\\n--'));
  return m ? m[1] : '';
}

function frame(opcode, payload) {
  const data = Buffer.from(payload);
  let header;
  if (data.length < 126) {
    header = Buffer.from([0x80 | opcode, data.length]);
  } else {
    header = Buffer.alloc(4);
    header[0] = 0x80 | opcode;
    header[1] = 126;
    header.writeUInt16BE(data.length, 2);
  }
  return Buffer.concat([header, data]);
}

function closeSideband(callId) {
  const socket = sidebands.get(callId);
  if (!socket) {
    return;
  }
  sidebands.delete(callId);
  try {
    socket.write(frame(0x8, Buffer.from([0x03, 0xe8])));
  } catch (e) { /* already gone */ }
  socket.end();
}

// Answers pings and close frames from the server's WebSocket client.
function readFrames(callId, socket) {
  let buf = Buffer.alloc(0);
  socket.on('data', (chunk) => {
    buf = Buffer.concat([buf, chunk]);
    while (buf.length >= 2) {
      const opcode = buf[0] & 0x0f;
      const masked = (buf[1] & 0x80) !== 0;
      let len = buf[1] & 0x7f;
      let offset = 2;
      if (len === 126) { if (buf.length < 4) return; len = buf.readUInt16BE(2); offset = 4; }
      else if (len === 127) { if (buf.length < 10) return; len = Number(buf.readBigUInt64BE(2)); offset = 10; }
      const maskOffset = offset;
      if (masked) offset += 4;
      if (buf.length < offset + len) return;
      let payload = buf.subarray(offset, offset + len);
      if (masked) {
        const mask = buf.subarray(maskOffset, maskOffset + 4);
        payload = Buffer.from(payload.map((b, i) => b ^ mask[i % 4]));
      }
      buf = buf.subarray(offset + len);
      if (opcode === 0x9) socket.write(frame(0xa, payload));
      if (opcode === 0x8) closeSideband(callId);
    }
  });
  socket.on('close', () => { if (sidebands.get(callId) === socket) sidebands.delete(callId); });
  socket.on('error', () => { if (sidebands.get(callId) === socket) sidebands.delete(callId); });
}

const server = http.createServer(async (req, res) => {
  const body = await readBody(req);
  const url = new URL(req.url, 'http://mock');
  const json = (status, value) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value)); };

  if (req.method === 'POST' && url.pathname === '/__mode') { mode = JSON.parse(body).mode; res.writeHead(204); res.end(); return; }
  if (req.method === 'GET' && url.pathname === '/__state') { json(200, { calls, hangups, sidebands: sidebandLog, openSidebands: [...sidebands.keys()] }); return; }
  if (req.method === 'POST' && url.pathname === '/__close-sidebands') { [...sidebands.keys()].forEach(closeSideband); res.writeHead(204); res.end(); return; }
  const send = url.pathname.match(/^\\/__sideband\\/([^/]+)\\/send$/);
  if (req.method === 'POST' && send) {
    const socket = sidebands.get(send[1]);
    if (!socket) { json(404, { error: 'no sideband' }); return; }
    socket.write(frame(0x1, body));
    res.writeHead(204); res.end();
    return;
  }

  if (req.method === 'POST' && url.pathname === '/v1/realtime/calls') {
    const callId = 'rtc_e2e' + (nextCall++);
    let session = null;
    try { session = JSON.parse(formField(body, 'session')); } catch (e) { session = formField(body, 'session'); }
    calls.push({ callId, authorization: req.headers.authorization || '', sdp: formField(body, 'sdp'), session });
    if (mode === 'fail') { json(500, { error: { message: '${REALTIME_MOCK_UPSTREAM_ERROR}' } }); return; }
    res.writeHead(201, { 'Content-Type': 'application/sdp', Location: '/v1/realtime/calls/' + callId });
    res.end(${JSON.stringify(REALTIME_MOCK_ANSWER_SDP)});
    return;
  }
  const hangup = url.pathname.match(/^\\/v1\\/realtime\\/calls\\/([^/]+)\\/hangup$/);
  if (req.method === 'POST' && hangup) { hangups.push(hangup[1]); closeSideband(hangup[1]); res.writeHead(200); res.end(); return; }
  res.writeHead(404);
  res.end();
});

server.on('upgrade', (req, socket) => {
  const url = new URL(req.url, 'http://mock');
  const callId = url.searchParams.get('call_id');
  if (url.pathname !== '/v1/realtime' || !callId) { socket.end('HTTP/1.1 404 Not Found\\r\\n\\r\\n'); return; }
  const accept = crypto.createHash('sha1').update(req.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
  socket.write('HTTP/1.1 101 Switching Protocols\\r\\nUpgrade: websocket\\r\\nConnection: Upgrade\\r\\nSec-WebSocket-Accept: ' + accept + '\\r\\n\\r\\n');
  sidebands.set(callId, socket);
  sidebandLog.push(callId);
  readFrames(callId, socket);
});

server.listen(${REALTIME_MOCK_PORT}, '0.0.0.0');
`.trim();
}

/** Runs the fake OpenAI realtime API, which can also fail on demand. */
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

    async state(): Promise<MockState> {
        const response = await fetch(this.controlURL('/__state'), { signal: AbortSignal.timeout(10000) });
        if (!response.ok) {
            throw new Error(`Reading the realtime mock state failed with ${response.status}`);
        }
        return response.json();
    }

    // closeSidebands closes every open sideband, as OpenAI does once a
    // browser closes its peer connection, which frees the users' session slots.
    async closeSidebands(): Promise<void> {
        const response = await fetch(this.controlURL('/__close-sidebands'), { method: 'POST', signal: AbortSignal.timeout(10000) });
        if (!response.ok) {
            throw new Error(`Closing the realtime mock sidebands failed with ${response.status}`);
        }
    }

    // sendSideband sends a session event to the plugin over the call's sideband.
    async sendSideband(callId: string, event: unknown): Promise<void> {
        const response = await fetch(this.controlURL(`/__sideband/${encodeURIComponent(callId)}/send`), {
            method: 'POST',
            body: JSON.stringify(event),
            signal: AbortSignal.timeout(10000),
        });
        if (!response.ok) {
            throw new Error(`Sending on the realtime mock sideband failed with ${response.status}`);
        }
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
