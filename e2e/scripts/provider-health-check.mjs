// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {appendFileSync} from 'fs';

const DEFAULT_ANTHROPIC_MODEL = 'claude-sonnet-4-20250514';
const DEFAULT_OPENAI_MODEL = 'gpt-5.2';

function sanitizeMessage(message) {
    return String(message).replace(/\s+/g, ' ').trim();
}

async function readBody(response) {
    try {
        const body = await response.text();
        return sanitizeMessage(body).slice(0, 200);
    } catch {
        return '';
    }
}

function makeResult({provider, available, skippable, reason}) {
    return {
        provider,
        available,
        skippable,
        reason,
        action: available ? 'run' : (skippable ? 'skip' : 'fail'),
        status: available ? 'healthy' : (skippable ? 'upstream-unavailable' : 'configuration-error'),
    };
}

async function checkAnthropicHealth() {
    const apiKey = process.env.ANTHROPIC_API_KEY;
    if (!apiKey) {
        return makeResult({
            provider: 'anthropic',
            available: false,
            skippable: false,
            reason: 'ANTHROPIC_API_KEY is not configured',
        });
    }

    const model = process.env.ANTHROPIC_MODEL || DEFAULT_ANTHROPIC_MODEL;
    const start = Date.now();

    try {
        const response = await fetch('https://api.anthropic.com/v1/messages', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'x-api-key': apiKey,
                'anthropic-version': '2023-06-01',
            },
            body: JSON.stringify({
                model,
                max_tokens: 20,
                messages: [{role: 'user', content: 'hi'}],
            }),
            signal: AbortSignal.timeout(30000),
        });

        const latencyMs = Date.now() - start;
        if (response.ok) {
            return makeResult({
                provider: 'anthropic',
                available: true,
                skippable: false,
                reason: `OK (${latencyMs}ms)`,
            });
        }

        const body = await readBody(response);
        return makeResult({
            provider: 'anthropic',
            available: false,
            skippable: response.status === 408 || response.status === 409 || response.status === 429 || response.status >= 500,
            reason: `HTTP ${response.status}${body ? `: ${body}` : ''}`,
        });
    } catch (error) {
        return makeResult({
            provider: 'anthropic',
            available: false,
            skippable: true,
            reason: sanitizeMessage(error instanceof Error ? error.message : String(error)),
        });
    }
}

async function checkOpenAIHealth() {
    const apiKey = process.env.OPENAI_API_KEY;
    if (!apiKey) {
        return makeResult({
            provider: 'openai',
            available: false,
            skippable: false,
            reason: 'OPENAI_API_KEY is not configured',
        });
    }

    const model = process.env.OPENAI_MODEL || DEFAULT_OPENAI_MODEL;
    const start = Date.now();

    try {
        const response = await fetch('https://api.openai.com/v1/chat/completions', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${apiKey}`,
            },
            body: JSON.stringify({
                model,
                max_completion_tokens: 20,
                messages: [{role: 'user', content: 'hi'}],
            }),
            signal: AbortSignal.timeout(30000),
        });

        const latencyMs = Date.now() - start;
        if (response.ok) {
            return makeResult({
                provider: 'openai',
                available: true,
                skippable: false,
                reason: `OK (${latencyMs}ms)`,
            });
        }

        const body = await readBody(response);
        return makeResult({
            provider: 'openai',
            available: false,
            skippable: response.status === 408 || response.status === 409 || response.status === 429 || response.status >= 500,
            reason: `HTTP ${response.status}${body ? `: ${body}` : ''}`,
        });
    } catch (error) {
        return makeResult({
            provider: 'openai',
            available: false,
            skippable: true,
            reason: sanitizeMessage(error instanceof Error ? error.message : String(error)),
        });
    }
}

function writeGithubOutput(results) {
    if (!process.env.GITHUB_OUTPUT) {
        return;
    }

    const lines = [
        `anthropic_available=${String(results.anthropic.available)}`,
        `anthropic_skippable=${String(results.anthropic.skippable)}`,
        `anthropic_status=${results.anthropic.status}`,
        `anthropic_action=${results.anthropic.action}`,
        `anthropic_reason=${results.anthropic.reason}`,
        `openai_available=${String(results.openai.available)}`,
        `openai_skippable=${String(results.openai.skippable)}`,
        `openai_status=${results.openai.status}`,
        `openai_action=${results.openai.action}`,
        `openai_reason=${results.openai.reason}`,
        `any_available=${String(results.anthropic.available || results.openai.available)}`,
    ];

    appendFileSync(process.env.GITHUB_OUTPUT, `${lines.join('\n')}\n`);
}

function writeGithubSummary(results) {
    if (!process.env.GITHUB_STEP_SUMMARY) {
        return;
    }

    const providers = [results.anthropic, results.openai];
    const lines = [
        '## Real API provider health',
        '',
        '| Provider | Status | Action | Reason |',
        '| --- | --- | --- | --- |',
        ...providers.map((provider) => `| ${provider.provider} | ${provider.status} | ${provider.action} | ${provider.reason} |`),
        '',
    ];

    if (providers.every((provider) => !provider.available)) {
        lines.push('No healthy upstream providers are available, so the workflow will fail instead of skipping all real-API coverage.');
    } else if (providers.some((provider) => provider.action === 'skip')) {
        lines.push('Some provider-backed suites will be skipped because the affected upstream is unavailable to CI. Healthy providers will still run.');
    }

    appendFileSync(process.env.GITHUB_STEP_SUMMARY, `${lines.join('\n')}\n`);
}

async function main() {
    const [anthropic, openai] = await Promise.all([
        checkAnthropicHealth(),
        checkOpenAIHealth(),
    ]);

    const results = {anthropic, openai};
    console.log(`Anthropic: ${anthropic.status} -> ${anthropic.action} (${anthropic.reason})`);
    console.log(`OpenAI: ${openai.status} -> ${openai.action} (${openai.reason})`);

    writeGithubOutput(results);
    writeGithubSummary(results);

    if (!anthropic.available && !openai.available) {
        console.error('No healthy real API providers are available. Failing CI instead of skipping all upstream-backed coverage.');
        process.exitCode = 1;
    }
}

await main();
