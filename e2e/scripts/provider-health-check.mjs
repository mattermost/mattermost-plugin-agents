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

async function checkAnthropicHealth() {
    const apiKey = process.env.ANTHROPIC_API_KEY;
    if (!apiKey) {
        return {
            provider: 'anthropic',
            available: false,
            reason: 'ANTHROPIC_API_KEY is not configured',
        };
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
            return {
                provider: 'anthropic',
                available: true,
                reason: `OK (${latencyMs}ms)`,
            };
        }

        const body = await readBody(response);
        return {
            provider: 'anthropic',
            available: false,
            reason: `HTTP ${response.status}${body ? `: ${body}` : ''}`,
        };
    } catch (error) {
        return {
            provider: 'anthropic',
            available: false,
            reason: sanitizeMessage(error instanceof Error ? error.message : String(error)),
        };
    }
}

async function checkOpenAIHealth() {
    const apiKey = process.env.OPENAI_API_KEY;
    if (!apiKey) {
        return {
            provider: 'openai',
            available: false,
            reason: 'OPENAI_API_KEY is not configured',
        };
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
            return {
                provider: 'openai',
                available: true,
                reason: `OK (${latencyMs}ms)`,
            };
        }

        const body = await readBody(response);
        return {
            provider: 'openai',
            available: false,
            reason: `HTTP ${response.status}${body ? `: ${body}` : ''}`,
        };
    } catch (error) {
        return {
            provider: 'openai',
            available: false,
            reason: sanitizeMessage(error instanceof Error ? error.message : String(error)),
        };
    }
}

function writeGithubOutput(results) {
    if (!process.env.GITHUB_OUTPUT) {
        return;
    }

    const lines = [
        `anthropic_available=${String(results.anthropic.available)}`,
        `anthropic_reason=${results.anthropic.reason}`,
        `openai_available=${String(results.openai.available)}`,
        `openai_reason=${results.openai.reason}`,
        `any_available=${String(results.anthropic.available || results.openai.available)}`,
    ];

    appendFileSync(process.env.GITHUB_OUTPUT, `${lines.join('\n')}\n`);
}

async function main() {
    const [anthropic, openai] = await Promise.all([
        checkAnthropicHealth(),
        checkOpenAIHealth(),
    ]);

    const results = {anthropic, openai};
    console.log(`Anthropic available: ${anthropic.available} (${anthropic.reason})`);
    console.log(`OpenAI available: ${openai.available} (${openai.reason})`);

    writeGithubOutput(results);
}

await main();
