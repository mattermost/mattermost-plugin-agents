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

export type AIMockStartOptions = {
    fixtures?: AIMockFixtureFile | AIMockFixture[];
    fixtureFiles?: Array<{ name: string; contents: AIMockFixtureFile | AIMockFixture[] }>;
    strict?: boolean;
    logLevel?: 'error' | 'warn' | 'info' | 'debug';
};

const DEFAULT_FIXTURE_FILE = 'fixtures.json';

export class AIMockContainer {
    private container: StartedTestContainer | null = null;
    private network: StartedNetwork | null = null;
    private fixturesDir: string | null = null;
    private startOptions: AIMockStartOptions = {};
    private fixtureFileContents: AIMockFixtureFile = { fixtures: [] };

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
        if (this.container) {
            await this.restart();
        }
    }

    async appendFixtures(fixtures: AIMockFixtureFile | AIMockFixture[]): Promise<void> {
        this.fixtureFileContents = mergeFixtureFiles(
            this.fixtureFileContents,
            normalizeFixtureInput(fixtures),
        );
        if (this.container) {
            await this.restart();
        }
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

        for (const entry of fs.readdirSync(fixturesDir)) {
            fs.rmSync(path.join(fixturesDir, entry), { force: true });
        }

        // aimock concatenates every fixture file under /fixtures, so a single
        // merged file keeps first-match ordering deterministic and lets
        // setFixtures/appendFixtures reload correctly regardless of how the
        // sidecar was originally started.
        fs.writeFileSync(
            path.join(fixturesDir, DEFAULT_FIXTURE_FILE),
            JSON.stringify(this.fixtureFileContents, null, 2),
        );
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
