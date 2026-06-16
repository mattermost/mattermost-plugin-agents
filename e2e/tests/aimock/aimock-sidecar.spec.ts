import fs from 'fs';
import path from 'path';

import {test, expect} from '@playwright/test';
import {Network} from 'testcontainers';
import type {StartedNetwork} from 'testcontainers';

import {
	AimockContainer,
	RunAimockContainer,
} from 'helpers/aimock-container';
import {
	AIMOCK_EXPECTED_STREAMING_TEXT,
	AIMOCK_TOOL_NAME,
	AIMOCK_USER_MESSAGE,
} from 'helpers/aimock-fixture-constants';
import RunAimockPluginContainer from 'helpers/aimock-plugin-container';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIPlugin} from 'helpers/ai-plugin';

const distPath = path.resolve(__dirname, '../../../dist');
const hasPluginDist =
	fs.existsSync(distPath) && fs.readdirSync(distPath).some((f) => f.endsWith('.tar.gz'));

const username = 'regularuser';
const password = 'regularuser';

/** aimock may split streamed text/tool args across SSE chunks; match stable fragments. */
function expectChunkedFragments(body: string, fragments: string[]): void {
	for (const fragment of fragments) {
		expect(body).toContain(fragment);
	}
}

test.describe('aimock sidecar harness', () => {
	let network: StartedNetwork;
	let aimock: AimockContainer;

	test.beforeAll(async () => {
		network = await new Network().start();
		aimock = await RunAimockContainer(network);
	});

	test.afterAll(async () => {
		await aimock?.stop();
		await network?.stop();
	});

	test('streams fixture text via openaicompatible SSE', async () => {
		const response = await aimock.postChatCompletion({
			model: 'gpt-mock',
			stream: true,
			messages: [{role: 'user', content: AIMOCK_USER_MESSAGE}],
		});
		const body = await response.text();

		expect(response.ok).toBe(true);
		expect(body).toContain('data:');
		expectChunkedFragments(body, ['Hello from aimock E2', 'E!']);
		expect(body).toContain('[DONE]');
	});

	test('returns exact tool-call args in streaming response', async () => {
		const response = await aimock.postChatCompletion({
			model: 'gpt-mock',
			stream: true,
			messages: [{role: 'user', content: 'trigger aimock tool'}],
			tools: [
				{
					type: 'function',
					function: {
						name: AIMOCK_TOOL_NAME,
						parameters: {
							type: 'object',
							properties: {
								channel_id: {type: 'string'},
								message: {type: 'string'},
							},
							required: ['channel_id', 'message'],
						},
					},
				},
			],
		});
		const body = await response.text();

		expect(response.ok).toBe(true);
		expect(body).toContain(`"name":"${AIMOCK_TOOL_NAME}"`);
		expect(body).toContain('e2e-c');
		expect(body).toContain('h-1');
		expectChunkedFragments(body, ['exac', 'aimock tool messag']);
	});

	test('strict mode rejects unmatched requests', async () => {
		const response = await aimock.postChatCompletion({
			model: 'gpt-mock',
			stream: false,
			messages: [{role: 'user', content: 'no matching fixture'}],
		});

		expect(response.status).toBe(503);
	});

	test('restart reloads bind-mounted fixtures', async () => {
		await aimock.restart();

		const response = await aimock.postChatCompletion({
			model: 'gpt-mock',
			stream: true,
			messages: [{role: 'user', content: AIMOCK_USER_MESSAGE}],
		});
		const body = await response.text();

		expect(response.ok).toBe(true);
		expectChunkedFragments(body, ['Hello from aimock E2', 'E!']);
	});
});

test.describe('aimock plugin E2E', () => {
	test.skip(!hasPluginDist, 'requires make dist in repo root');

	let mattermost: MattermostContainer;
	let aimock: AimockContainer;

	test.beforeAll(async () => {
		mattermost = await RunAimockPluginContainer();
		aimock = await RunAimockContainer(mattermost.network);
	}, {timeout: 300000});

	test.afterAll(async () => {
		await aimock?.stop();
		await mattermost?.stop();
	});

	test('RHS receives deterministic aimock streaming response', async ({page}) => {
		test.setTimeout(120000);

		const mmPage = new MattermostPage(page);
		const aiPlugin = new AIPlugin(page);

		await mmPage.login(mattermost.url(), username, password);
		await aiPlugin.openRHS();
		await aiPlugin.sendMessage(AIMOCK_USER_MESSAGE);
		await aiPlugin.waitForBotResponse(AIMOCK_EXPECTED_STREAMING_TEXT);
	});
});
