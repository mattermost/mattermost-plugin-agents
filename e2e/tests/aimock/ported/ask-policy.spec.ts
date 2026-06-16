import {test, expect} from '@playwright/test';
import {Network} from 'testcontainers';
import type {StartedNetwork} from 'testcontainers';

import {
	AimockContainer,
	RunAimockContainer,
} from 'helpers/aimock-container';
import {
	AIMOCK_ASK_POLICY_CONTINUATION_TEXT,
	AIMOCK_ASK_POLICY_TOOL_LABEL,
	AIMOCK_ASK_POLICY_TOOL_NAME,
	AIMOCK_ASK_POLICY_USER_MESSAGE,
} from 'helpers/aimock-fixture-constants';
import {hasAimockPluginDist} from 'helpers/aimock-plugin-dist';
import {expectChunkedFragments} from 'helpers/aimock-sse-assertions';
import RunAimockToolConfigPluginContainer from 'helpers/aimock-tool-config-plugin-container';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIPlugin} from 'helpers/ai-plugin';

const username = 'regularuser';
const password = 'regularuser';

const ASK_POLICY_FIXTURES = ['ask-policy.json'] as const;

/**
 * Aimock port of tool-config/real-api/ask-policy.spec.ts.
 *
 * Original real-api suite: e2e/tests/tool-config/real-api/ask-policy.spec.ts
 * Uses deterministic aimock tool-call fixtures instead of relying on a live LLM
 * to invoke the ask-policy get_channel_info tool.
 */
test.describe('aimock ported: ask policy', () => {
	let network: StartedNetwork;
	let aimock: AimockContainer;

	test.beforeAll(async () => {
		network = await new Network().start();
		aimock = await RunAimockContainer(network, {
			fixtureFiles: ASK_POLICY_FIXTURES,
		});
	});

	test.afterAll(async () => {
		await aimock?.stop();
		await network?.stop();
	});

	test('fixture returns ask-policy tool call with exact args', async () => {
		const response = await aimock.postChatCompletion({
			model: 'gpt-mock',
			stream: true,
			messages: [{role: 'user', content: AIMOCK_ASK_POLICY_USER_MESSAGE}],
			tools: [
				{
					type: 'function',
					function: {
						name: AIMOCK_ASK_POLICY_TOOL_NAME,
						parameters: {
							type: 'object',
							properties: {
								channel_name: {type: 'string'},
							},
							required: ['channel_name'],
						},
					},
				},
			],
		});
		const body = await response.text();

		expect(response.ok).toBe(true);
		expect(body).toContain(`"name":"${AIMOCK_ASK_POLICY_TOOL_NAME}"`);
		expectChunkedFragments(body, ['channel_name', 'Tow', 'n Square']);
	});

	test('fixture serves continuation after tool result round', async () => {
		const response = await aimock.postChatCompletion({
			model: 'gpt-mock',
			stream: true,
			messages: [
				{role: 'user', content: AIMOCK_ASK_POLICY_USER_MESSAGE},
				{
					role: 'assistant',
					content: null,
					tool_calls: [
						{
							id: 'call_aimock_ask_1',
							type: 'function',
							function: {
								name: AIMOCK_ASK_POLICY_TOOL_NAME,
								arguments: JSON.stringify({channel_name: 'Town Square'}),
							},
						},
					],
				},
				{
					role: 'tool',
					tool_call_id: 'call_aimock_ask_1',
					content: '{"channel_id":"town-square-id","name":"Town Square"}',
				},
			],
		});
		const body = await response.text();

		expect(response.ok).toBe(true);
		expectChunkedFragments(body, ['Aimock ask policy co', 'ntinuation after cha', 'nnel lookup.']);
		expect(body).toContain('[DONE]');
	});
});

test.describe('aimock ported: ask policy (plugin E2E)', () => {
	test.skip(!hasAimockPluginDist(), 'requires make dist in repo root');

	let mattermost: MattermostContainer;
	let aimock: AimockContainer;

	test.beforeAll(async () => {
		mattermost = await RunAimockToolConfigPluginContainer();
		aimock = await RunAimockContainer(mattermost.network, {
			fixtureFiles: ASK_POLICY_FIXTURES,
		});
	}, {timeout: 300000});

	test.afterAll(async () => {
		await aimock?.stop();
		await mattermost?.stop();
	});

	test('ask policy tool shows pending approval in DM', async ({page}) => {
		test.setTimeout(120000);

		const mmPage = new MattermostPage(page);
		const aiPlugin = new AIPlugin(page);

		await mmPage.login(mattermost.url(), username, password);
		await aiPlugin.openRHS();
		await aiPlugin.sendMessage(AIMOCK_ASK_POLICY_USER_MESSAGE);

		const stopButton = page.getByRole('button', {name: /stop/i});
		await expect(stopButton).not.toBeVisible({timeout: 90000});

		const rhsContainer = page.getByTestId('mattermost-ai-rhs');
		await expect(rhsContainer).toBeVisible();

		const botPost = rhsContainer.locator('[data-testid="llm-bot-post"]').last();
		await expect(botPost.getByText(AIMOCK_ASK_POLICY_TOOL_LABEL, {exact: true})).toBeVisible({
			timeout: 30000,
		});

		const acceptButton = page.getByRole('button', {name: /^accept$/i});
		const rejectButton = page.getByRole('button', {name: /reject/i});
		await expect(acceptButton).toBeVisible();
		await expect(rejectButton).toBeVisible();

		await acceptButton.click();
		await expect(stopButton).not.toBeVisible({timeout: 60000});
		await expect(botPost.getByText(AIMOCK_ASK_POLICY_CONTINUATION_TEXT)).toBeVisible({
			timeout: 30000,
		});
	});
});
