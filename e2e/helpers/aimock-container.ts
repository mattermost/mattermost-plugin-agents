import fs from 'fs';
import path from 'path';
import {GenericContainer, StartedNetwork, StartedTestContainer, Wait} from 'testcontainers';

export const AIMOCK_IMAGE = 'ghcr.io/copilotkit/aimock:latest';
export const AIMOCK_PORT = 8080;
export const AIMOCK_NETWORK_ALIAS = 'openai';
export const AIMOCK_FIXTURES_DIR = path.join(__dirname, '../fixtures/aimock');

export const DEFAULT_FIXTURE_FILES = [
	'streaming-text.json',
	'tool-call.json',
] as const;

export type AimockStartOptions = {
	fixtureFiles?: readonly string[];
	strict?: boolean;
	logLevel?: string;
};

export class AimockContainer {
	container: StartedTestContainer;
	private network: StartedNetwork | null = null;
	private startOptions: AimockStartOptions = {};

	start = async (network: StartedNetwork, options: AimockStartOptions = {}): Promise<void> => {
		this.network = network;
		this.startOptions = {
			fixtureFiles: options.fixtureFiles ?? DEFAULT_FIXTURE_FILES,
			strict: options.strict ?? true,
			logLevel: options.logLevel ?? 'warn',
		};

		const fixtureFiles = this.startOptions.fixtureFiles ?? DEFAULT_FIXTURE_FILES;
		for (const file of fixtureFiles) {
			const fixturePath = path.join(AIMOCK_FIXTURES_DIR, file);
			if (!fs.existsSync(fixturePath)) {
				throw new Error(`Missing aimock fixture: ${fixturePath}`);
			}
		}

		const command = fixtureFiles.flatMap((file) => ['--fixtures', `/fixtures/${file}`]);
		command.push('--host', '0.0.0.0', '--port', String(AIMOCK_PORT));
		if (this.startOptions.strict) {
			command.push('--strict');
		}
		command.push('--log-level', this.startOptions.logLevel ?? 'warn');

		this.container = await new GenericContainer(AIMOCK_IMAGE)
			.withExposedPorts(AIMOCK_PORT)
			.withNetwork(network)
			.withNetworkAliases(AIMOCK_NETWORK_ALIAS)
			.withBindMounts([
				{
					source: AIMOCK_FIXTURES_DIR,
					target: '/fixtures',
					mode: 'ro',
				},
			])
			.withCommand(command)
			.withWaitStrategy(Wait.forHttp('/ready', AIMOCK_PORT))
			.start();
	};

	stop = async (): Promise<void> => {
		if (this.container) {
			await this.container.stop();
		}
	};

	/** aimock has no Smocker-style /reset; restart reloads bind-mounted fixtures. */
	restart = async (): Promise<void> => {
		if (!this.network) {
			throw new Error('AimockContainer.restart called before start');
		}
		await this.stop();
		await this.start(this.network, this.startOptions);
	};

	getMappedBaseUrl = (): string => {
		return `http://127.0.0.1:${this.container.getMappedPort(AIMOCK_PORT)}`;
	};

	getNetworkBaseUrl = (): string => {
		return `http://${AIMOCK_NETWORK_ALIAS}:${AIMOCK_PORT}`;
	};

	postChatCompletion = async (
		body: Record<string, unknown>,
		headers: Record<string, string> = {},
	): Promise<Response> => {
		return fetch(`${this.getMappedBaseUrl()}/v1/chat/completions`, {
			method: 'POST',
			headers: {
				Authorization: 'Bearer mock',
				'Content-Type': 'application/json',
				...headers,
			},
			body: JSON.stringify(body),
		});
	};
}

export const RunAimockContainer = async (
	network: StartedNetwork,
	options?: AimockStartOptions,
): Promise<AimockContainer> => {
	const aimock = new AimockContainer();
	await aimock.start(network, options);
	return aimock;
};
