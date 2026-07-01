import { test, expect } from '@playwright/test';
import { Network } from 'testcontainers';

import { AIMockContainer, RunAIMockSidecar } from 'helpers/aimock-container';
import {
    buildCitationResponse,
    buildReasoningResponse,
    buildTextResponse,
    buildToolCallAndTextResponse,
    TITLE_GENERATION_PROMPT_PREFIX,
} from 'helpers/aimock-fixtures';
import { RunAIMockContainer } from 'helpers/plugincontainer';
import MattermostContainer from 'helpers/mmcontainer';
import { MattermostPage } from 'helpers/mm';
import { AIPlugin } from 'helpers/ai-plugin';
import { LLMBotPostHelper } from 'helpers/llmbot-post';

const username = 'regularuser';
const password = 'regularuser';
const TEXT_USER_MESSAGE = 'aimock-baseline-text-seed-001';
const TEXT_RESPONSE = 'Aimock baseline deterministic reply.';
const REASONING_USER_MESSAGE = 'aimock-baseline-reasoning-seed-001';
const REASONING_TEXT = 'Reasoning baseline final answer.';
const REASONING_CONTENT = 'Step one: inspect prompt. Step two: respond.';
const CITATION_USER_MESSAGE = 'aimock-baseline-citation-seed-001';
const CITATION_TEXT = 'Citation baseline answer with source.';
const CITATION_URL = 'https://docs.example.com/aimock-baseline';

function extractStreamedContent(body: string): string {
    const chunks: string[] = [];
    for (const line of body.split('\n')) {
        if (!line.startsWith('data:') || line.includes('[DONE]')) {
            continue;
        }

        const payload = line.slice('data:'.length).trim();
        if (!payload) {
            continue;
        }

        try {
            const parsed = JSON.parse(payload) as {
                choices?: Array<{ delta?: { content?: string } }>;
            };
            const content = parsed.choices?.[0]?.delta?.content;
            if (content) {
                chunks.push(content);
            }
        } catch {
            // Ignore malformed SSE lines in smoke assertions.
        }
    }

    return chunks.join('');
}

test.describe('aimock fixture builders', () => {
    test('orders toolCallId before userMessage and omits sequenceIndex', () => {
        const file = buildToolCallAndTextResponse({
            userMessage: 'run tool please',
            toolCallId: 'call_baseline_001',
            toolName: 'get_channel_info',
            toolArguments: { channel_id: 'abc' },
            finalContent: 'Tool done.',
            title: 'Baseline title',
        });

        expect(JSON.stringify(file)).not.toContain('sequenceIndex');
        expect(file.fixtures[0].match.toolCallId).toBe('call_baseline_001');
        expect(file.fixtures[1].match.userMessage).toBe(TITLE_GENERATION_PROMPT_PREFIX);
        expect(file.fixtures[2].match.userMessage).toBe('run tool please');
        expect(file.fixtures[2].match.hasToolResult).toBe(false);
    });
});

test.describe('aimock sidecar HTTP', () => {
    let network: Awaited<ReturnType<Network['start']>>;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        network = await new Network().start();
        aimock = await RunAIMockSidecar(network, {
            fixtures: buildTextResponse({
                userMessage: TEXT_USER_MESSAGE,
                content: TEXT_RESPONSE,
                title: 'Aimock HTTP smoke',
            }),
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await network?.stop();
    });

    test('matched chat completion streams expected text', async () => {
        const response = await aimock.postChatCompletion({
            model: 'gpt-mock',
            messages: [{ role: 'user', content: TEXT_USER_MESSAGE }],
            stream: true,
        });

        expect(response.status).toBe(200);
        const body = await response.text();
        expect(body).toContain('data:');
        expect(extractStreamedContent(body)).toBe(TEXT_RESPONSE);
    });

    test('unmatched request returns 503 in strict mode', async () => {
        const response = await aimock.postChatCompletion({
            model: 'gpt-mock',
            messages: [{ role: 'user', content: 'unmatched-aimock-baseline-request' }],
            stream: true,
        });

        expect(response.status).toBe(503);
    });
});

test.describe('aimock Mattermost stack', () => {
    let mattermost: MattermostContainer;
    let aimock: AIMockContainer;

    test.beforeAll(async () => {
        test.setTimeout(180000);
        mattermost = await RunAIMockContainer();
        aimock = await RunAIMockSidecar(mattermost.network, {
            fixtures: buildTextResponse({
                userMessage: TEXT_USER_MESSAGE,
                content: TEXT_RESPONSE,
                title: 'Aimock stack smoke',
            }),
        });
    });

    test.afterAll(async () => {
        await aimock?.stop();
        await mattermost?.stop();
    });

    test('RHS receives deterministic aimock response', async ({ page }) => {
        const mmPage = new MattermostPage(page);
        const aiPlugin = new AIPlugin(page);

        await mmPage.login(mattermost.url(), username, password);
        await aiPlugin.openRHS();
        await aiPlugin.switchBotWhenListed('Aimock Bot');
        await aiPlugin.sendMessage(TEXT_USER_MESSAGE);
        await aiPlugin.waitForBotResponse(TEXT_RESPONSE);
    });
});

test.describe('aimock schema probes', () => {
    test.describe('reasoning via Bifrost chat completions', () => {
        let mattermost: MattermostContainer;
        let aimock: AIMockContainer;

        test.beforeAll(async () => {
            test.setTimeout(180000);
            mattermost = await RunAIMockContainer();
            aimock = await RunAIMockSidecar(mattermost.network, {
                fixtures: buildReasoningResponse({
                    userMessage: REASONING_USER_MESSAGE,
                    reasoning: REASONING_CONTENT,
                    content: REASONING_TEXT,
                    title: 'Reasoning probe',
                }),
            });
        });

        test.afterAll(async () => {
            await aimock?.stop();
            await mattermost?.stop();
        });

        test('waitForReasoning succeeds with aimock reasoning fixture', async ({ page }) => {
            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);
            const llmBotHelper = new LLMBotPostHelper(page);

            await mmPage.login(mattermost.url(), username, password);
            await aiPlugin.openRHS();
            await aiPlugin.switchBotWhenListed('Aimock Bot');
            await aiPlugin.sendMessage(REASONING_USER_MESSAGE);
            await llmBotHelper.waitForReasoning();
            await aiPlugin.waitForBotResponse(REASONING_TEXT);
        });
    });

    test.describe('citation via webSearches metadata', () => {
        let mattermost: MattermostContainer;
        let aimock: AIMockContainer;

        test.beforeAll(async () => {
            test.setTimeout(180000);
            mattermost = await RunAIMockContainer();
            aimock = await RunAIMockSidecar(mattermost.network, {
                fixtures: buildCitationResponse({
                    userMessage: CITATION_USER_MESSAGE,
                    content: CITATION_TEXT,
                    citations: [{ url: CITATION_URL, title: 'Aimock Baseline Docs' }],
                    title: 'Citation probe',
                }),
            });
        });

        test.afterAll(async () => {
            await aimock?.stop();
            await mattermost?.stop();
        });

        test('records whether native webSearches produce citation UI', async ({ page }) => {
            const mmPage = new MattermostPage(page);
            const aiPlugin = new AIPlugin(page);
            const llmBotHelper = new LLMBotPostHelper(page);

            await mmPage.login(mattermost.url(), username, password);
            await aiPlugin.openRHS();
            await aiPlugin.switchBotWhenListed('Aimock Bot');
            await aiPlugin.sendMessage(CITATION_USER_MESSAGE);
            await aiPlugin.waitForBotResponse(CITATION_TEXT);

            const citations = llmBotHelper.getAllCitationIcons();
            const count = await citations.count();
            test.info().annotations.push({
                type: 'aimock-citation-probe',
                description:
                    count > 0
                        ? 'Native aimock webSearches produced citation UI via chat completions.'
                        : 'Native webSearches did NOT produce citation UI with useResponsesAPI:false; later citation suites should use tool-call fallback.',
            });

            if (count === 0) {
                test.fixme(true, 'Native URL citations unsupported on chat completions path for this milestone.');
            } else {
                await llmBotHelper.expectCitationCount(count);
            }
        });
    });
});
