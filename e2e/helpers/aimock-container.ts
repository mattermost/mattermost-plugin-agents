import fs from 'fs';
import os from 'os';
import path from 'path';

import { GenericContainer, StartedNetwork, StartedTestContainer, Wait } from 'testcontainers';

import {
    AIMockFixture,
    AIMockFixtureFile,
    mergeFixtureFiles,
    normalizeFixtureInput,
} from './aimock-fixtures';

export const AIMOCK_IMAGE =
    'ghcr.io/copilotkit/aimock:1.31.0@sha256:288f6698ff2f3c97eb422dd84a865cd5e610041ee096a36bee7aa4768c33a8ca';
export const AIMOCK_PORT = 8080;
export const AIMOCK_NETWORK_ALIAS = 'openai';

// aimock runs a Node HTTP server whose default keep-alive idle timeout is 5s.
// The plugin's bifrost client (fasthttp) keeps idle connections pooled for up to
// 30s (MaxIdleConnDuration), so between 5s and 30s of idle it reuses a connection
// aimock has already closed. For streaming requests fasthttp surfaces this as a
// hard error without retrying, which the plugin renders as an LLM error post and
// flakes the e2e tests. Preloading this script raises the server keep-alive idle
// timeout above the client reuse window so connections are never stale on reuse.
const AIMOCK_KEEPALIVE_PRELOAD_TARGET = '/preload/keepalive.cjs';
const AIMOCK_KEEPALIVE_PRELOAD = `'use strict';
const http = require('http');
const KEEP_ALIVE_MS = 120000;
const HEADERS_MS = 125000;
function applyTimeouts(server) {
  try {
    server.keepAliveTimeout = KEEP_ALIVE_MS;
    server.headersTimeout = HEADERS_MS;
  } catch (e) { /* ignore */ }
  return server;
}
const origCreateServer = http.createServer;
http.createServer = function (...args) {
  return applyTimeouts(origCreateServer.apply(this, args));
};
const origListen = http.Server.prototype.listen;
http.Server.prototype.listen = function (...args) {
  applyTimeouts(this);
  return origListen.apply(this, args);
};
`;

export type AIMockStartOptions = {
    fixtures?: AIMockFixtureFile | AIMockFixture[];
    fixtureFiles?: Array<{ name: string; contents: AIMockFixtureFile | AIMockFixture[] }>;
    strict?: boolean;
    logLevel?: 'error' | 'warn' | 'info' | 'debug';
};

const DEFAULT_FIXTURE_FILE = 'fixtures.json';

// Unique user message used to confirm that an in-place fixture reload (--watch)
// has taken effect. Each write embeds a fresh token in a sentinel fixture; after
// rewriting we poll until aimock serves the new token, so callers observe the new
// fixtures without restarting the container.
const AIMOCK_RELOAD_PROBE_MESSAGE = '__aimock_reload_probe__';
const AIMOCK_RELOAD_TIMEOUT_MS = 15000;

export class AIMockContainer {
    private container: StartedTestContainer | null = null;
    private network: StartedNetwork | null = null;
    private fixturesDir: string | null = null;
    private startOptions: AIMockStartOptions = {};
    private fixtureFileContents: AIMockFixtureFile = { fixtures: [] };
    private reloadToken = '';
    private reloadCounter = 0;

    async start(network: StartedNetwork, options: AIMockStartOptions = {}): Promise<void> {
        this.network = network;
        this.startOptions = options;
        this.fixtureFileContents = this.buildInitialFixtureFile(options);
        await this.writeFixtureFiles();
        await this.startContainer();
    }

    async stop(): Promise<void> {
        if (this.container) {
            await this.container.stop();
            this.container = null;
        }

        this.removeFixturesDir();
        this.network = null;
    }

    async restart(): Promise<void> {
        if (!this.network) {
            throw new Error('AIMockContainer.restart called before start');
        }

        if (this.container) {
            await this.container.stop();
            this.container = null;
        }

        await this.writeFixtureFiles();
        await this.startContainer();
    }

    async setFixtures(fixtures: AIMockFixtureFile | AIMockFixture[]): Promise<void> {
        this.fixtureFileContents = normalizeFixtureInput(fixtures);
        await this.reloadFixtures();
    }

    async appendFixtures(fixtures: AIMockFixtureFile | AIMockFixture[]): Promise<void> {
        this.fixtureFileContents = mergeFixtureFiles(
            this.fixtureFileContents,
            normalizeFixtureInput(fixtures),
        );
        await this.reloadFixtures();
    }

    getMappedBaseUrl(): string {
        if (!this.container) {
            throw new Error('AIMockContainer.getMappedBaseUrl called before start');
        }

        return `http://127.0.0.1:${this.container.getMappedPort(AIMOCK_PORT)}`;
    }

    getNetworkBaseUrl(): string {
        return `http://${AIMOCK_NETWORK_ALIAS}:${AIMOCK_PORT}`;
    }

    async postChatCompletion(
        body: Record<string, unknown>,
        headers: Record<string, string> = {},
    ): Promise<Response> {
        return fetch(`${this.getMappedBaseUrl()}/v1/chat/completions`, {
            method: 'POST',
            headers: {
                Authorization: 'Bearer mock',
                'Content-Type': 'application/json',
                ...headers,
            },
            body: JSON.stringify(body),
        });
    }

    private buildInitialFixtureFile(options: AIMockStartOptions): AIMockFixtureFile {
        if (options.fixtureFiles?.length) {
            return mergeFixtureFiles(
                ...options.fixtureFiles.map((file) => normalizeFixtureInput(file.contents)),
            );
        }

        if (options.fixtures) {
            return normalizeFixtureInput(options.fixtures);
        }

        return { fixtures: [] };
    }

    private ensureFixturesDir(): string {
        if (!this.fixturesDir) {
            this.fixturesDir = fs.mkdtempSync(path.join(os.tmpdir(), 'aimock-fixtures-'));
        }

        return this.fixturesDir;
    }

    private removeFixturesDir(): void {
        if (this.fixturesDir && fs.existsSync(this.fixturesDir)) {
            fs.rmSync(this.fixturesDir, { recursive: true, force: true });
        }

        this.fixturesDir = null;
    }

    private async writeFixtureFiles(): Promise<void> {
        const fixturesDir = this.ensureFixturesDir();
        this.reloadToken = `${Date.now()}-${++this.reloadCounter}`;

        // Embed a sentinel fixture carrying the current reload token so reloads
        // can be confirmed over HTTP (see reloadFixtures).
        const fileContents: AIMockFixtureFile = {
            fixtures: [
                {
                    match: { userMessage: AIMOCK_RELOAD_PROBE_MESSAGE },
                    response: { content: this.reloadToken },
                },
                ...this.fixtureFileContents.fixtures,
            ],
        };

        // Drop any stray files but overwrite the active fixture file in place so a
        // watch reload sees one atomic change instead of an unlink+add that could
        // momentarily leave aimock with no fixtures.
        for (const entry of fs.readdirSync(fixturesDir)) {
            if (entry !== DEFAULT_FIXTURE_FILE) {
                fs.rmSync(path.join(fixturesDir, entry), { force: true });
            }
        }

        // aimock concatenates every fixture file under /fixtures, so a single
        // merged file keeps first-match ordering deterministic and lets
        // setFixtures/appendFixtures reload correctly regardless of how the
        // sidecar was originally started.
        fs.writeFileSync(
            path.join(fixturesDir, DEFAULT_FIXTURE_FILE),
            JSON.stringify(fileContents, null, 2),
        );
    }

    // Rewrites the fixture file and waits for aimock's --watch reload to take
    // effect (confirmed via the sentinel token), avoiding a container restart.
    // Falls back to a full restart if the reload cannot be confirmed in time.
    private async reloadFixtures(): Promise<void> {
        await this.writeFixtureFiles();
        if (!this.container) {
            return;
        }

        const token = this.reloadToken;
        const deadline = Date.now() + AIMOCK_RELOAD_TIMEOUT_MS;
        while (Date.now() < deadline) {
            try {
                const response = await this.postChatCompletion({
                    model: 'gpt-mock',
                    messages: [{ role: 'user', content: AIMOCK_RELOAD_PROBE_MESSAGE }],
                });
                if (response.ok) {
                    const data = (await response.json()) as {
                        choices?: Array<{ message?: { content?: string } }>;
                    };
                    if (data.choices?.[0]?.message?.content === token) {
                        return;
                    }
                }
            } catch {
                // aimock may briefly drop the connection while reloading; retry.
            }
            await new Promise((resolve) => setTimeout(resolve, 100));
        }

        await this.restart();
    }

    private buildCommand(): string[] {
        const strict = this.startOptions.strict ?? true;
        const command = [
            '--fixtures',
            '/fixtures',
            '--host',
            '0.0.0.0',
            '--port',
            String(AIMOCK_PORT),
            // Reload fixtures in place on file change so setFixtures/appendFixtures
            // never restart the container; restarting would leave the plugin's
            // pooled connections pointing at the dead container and flake the
            // first streaming request after a fixture swap (fasthttp does not
            // retry streaming connection errors).
            '--watch',
        ];

        if (strict) {
            command.push('--strict');
        }

        command.push('--log-level', this.startOptions.logLevel ?? 'warn');
        return command;
    }

    private async startContainer(): Promise<void> {
        if (!this.network) {
            throw new Error('AIMockContainer.startContainer called without network');
        }

        const fixturesDir = this.ensureFixturesDir();

        this.container = await new GenericContainer(AIMOCK_IMAGE)
            .withExposedPorts(AIMOCK_PORT)
            .withNetwork(this.network)
            .withNetworkAliases(AIMOCK_NETWORK_ALIAS)
            .withBindMounts([
                {
                    source: fixturesDir,
                    target: '/fixtures',
                    mode: 'ro',
                },
            ])
            .withCopyContentToContainer([
                {
                    content: AIMOCK_KEEPALIVE_PRELOAD,
                    target: AIMOCK_KEEPALIVE_PRELOAD_TARGET,
                },
            ])
            .withEnvironment({
                NODE_OPTIONS: `--require=${AIMOCK_KEEPALIVE_PRELOAD_TARGET}`,
            })
            .withCommand(this.buildCommand())
            .withWaitStrategy(Wait.forHttp('/ready', AIMOCK_PORT))
            .start();
    }
}

export const RunAIMockSidecar = async (
    network: StartedNetwork,
    options?: AIMockStartOptions,
): Promise<AIMockContainer> => {
    const aimock = new AIMockContainer();
    await aimock.start(network, options);
    return aimock;
};
