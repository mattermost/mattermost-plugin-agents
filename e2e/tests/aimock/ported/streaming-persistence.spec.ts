import {test, expect} from '@playwright/test';
import {Network} from 'testcontainers';
import type {StartedNetwork} from 'testcontainers';

import {
	AimockContainer,
	RunAimockContainer,
} from 'helpers/aimock-container';
import {
	AIMOCK_STREAMING_PERSISTENCE_MESSAGE,
	AIMOCK_STREAMING_PERSISTENCE_TEXT,
} from 'helpers/aimock-fixture-constants';
import {hasAimockPluginDist} from 'helpers/aimock-plugin-dist';
import {expectChunkedFragments} from 'helpers/aimock-sse-assertions';
import RunAimockPluginContainer from 'helpers/aimock-plugin-container';
import MattermostContainer from 'helpers/mmcontainer';
import {MattermostPage} from 'helpers/mm';
import {AIPlugin} from 'helpers/ai-plugin';
import {LLMBotPostHelper} from 'helpers/llmbot-post';

const username = 'regularuser';
const password = 'regularuser';

const STREAMING_PERSISTENCE_FIXTURES = [
	'streaming-persistence.json',
] as const;

/**
 * Aimock port of llmbot-post-component/streaming-persistence.spec.ts
 * ("Navigation Persistence").
 *
 * Original real-api suite: e2e/tests/llmbot-post-component/streaming-persistence.spec.ts
 * Uses deterministic aimock streaming text instead of live LLM output.
 */
test.describe('aimock ported: streaming persistence', () => {
	let network: StartedNetwork;
	let aimock: AimockContainer;

	test.beforeAll(async () => {
		network = await new Network().start();
		aimock = await RunAimockContainer(network, {
			fixtureFiles: STREAMING_PERSISTENCE_FIXTURES,
		});
	});

	test.afterAll(async () => {
		await aimock?.stop();
		await network?.stop();
	});

	test('fixture streams deterministic persistence text', async () => {
		const response = await aimock.postChatCompletion({
			model: 'gpt-mock',
			stream: true,
			messages: [{role: 'user', content: AIMOCK_STREAMING_PERSISTENCE_MESSAGE}],
		});
		const body = await response.text();

		expect(response.ok).toBe(true);
		expectChunkedFragments(body, ['Aimock: TypeScript a', 'dds static types']);
		expect(body).toContain('[DONE]');
	});
});

test.describe('aimock ported: streaming persistence (plugin E2E)', () => {
	test.skip(!hasAimockPluginDist(), 'requires make dist in repo root');

	let mattermost: MattermostContainer;
	let aimock: AimockContainer;

	test.beforeAll(async () => {
		mattermost = await RunAimockPluginContainer();
		aimock = await RunAimockContainer(mattermost.network, {
			fixtureFiles: STREAMING_PERSISTENCE_FIXTURES,
		});
	}, {timeout: 300000});

	test.afterAll(async () => {
		await aimock?.stop();
		await mattermost?.stop();
	});

	test('Navigation Persistence', async ({page}) => {
		test.setTimeout(120000);

		const mmPage = new MattermostPage(page);
		const aiPlugin = new AIPlugin(page);
		const llmBotHelper = new LLMBotPostHelper(page);

		await mmPage.login(mattermost.url(), username, password);
		await aiPlugin.openRHS();
		await aiPlugin.sendMessage(AIMOCK_STREAMING_PERSISTENCE_MESSAGE);
		await llmBotHelper.waitForStreamingComplete();

		const postTextBefore = llmBotHelper.getPostText();
		const contentBefore = await postTextBefore.textContent();
		expect(contentBefore).toContain('TypeScript adds static types');

		await aiPlugin.closeRHS();
		await page.waitForTimeout(1000);
		await aiPlugin.openRHS();
		await page.waitForTimeout(2000);

		const postTextAfter = llmBotHelper.getPostText();
		await expect(postTextAfter).toBeVisible();
		const contentAfter = await postTextAfter.textContent();
		expect(contentAfter).toBe(contentBefore);
		expect(contentAfter).toContain(AIMOCK_STREAMING_PERSISTENCE_TEXT);
	});
});
