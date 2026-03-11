/**
 * API Health Check for E2E Tests
 *
 * Performs a lightweight pre-flight check against configured API providers
 * to fail fast with clear error messages instead of waiting through long
 * test timeouts when APIs are unreachable or misconfigured.
 *
 * Uses the same model and endpoint that the test will use to catch
 * model-specific issues (deprecated, unavailable, etc).
 */

import { LLMService } from './api-config';

interface HealthCheckResult {
    provider: string;
    model: string;
    healthy: boolean;
    error?: string;
    latencyMs: number;
}

// Cache health check results per service ID to avoid redundant checks
// when multiple test suites use the same provider in a single process.
const healthCheckCache = new Map<string, Promise<HealthCheckResult>>();

async function checkAnthropicHealth(service: LLMService): Promise<HealthCheckResult> {
    const start = Date.now();
    try {
        const response = await fetch(`${service.apiURL}/v1/messages`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'x-api-key': service.apiKey,
                'anthropic-version': '2023-06-01',
            },
            body: JSON.stringify({
                model: service.defaultModel,
                max_tokens: 20,
                messages: [{ role: 'user', content: 'hi' }],
            }),
            signal: AbortSignal.timeout(30000),
        });

        const latencyMs = Date.now() - start;

        if (response.ok) {
            return { provider: 'Anthropic', model: service.defaultModel, healthy: true, latencyMs };
        }

        const body = await response.text().catch(() => '');
        return {
            provider: 'Anthropic',
            model: service.defaultModel,
            healthy: false,
            error: `HTTP ${response.status}: ${body.substring(0, 200)}`,
            latencyMs,
        };
    } catch (err) {
        return {
            provider: 'Anthropic',
            model: service.defaultModel,
            healthy: false,
            error: `Connection failed: ${(err as Error).message}`,
            latencyMs: Date.now() - start,
        };
    }
}

async function checkOpenAIHealth(service: LLMService): Promise<HealthCheckResult> {
    const start = Date.now();
    try {
        const response = await fetch(`${service.apiURL}/chat/completions`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Authorization': `Bearer ${service.apiKey}`,
            },
            body: JSON.stringify({
                model: service.defaultModel,
                max_completion_tokens: 20,
                messages: [{ role: 'user', content: 'hi' }],
            }),
            signal: AbortSignal.timeout(30000),
        });

        const latencyMs = Date.now() - start;

        if (response.ok) {
            return { provider: 'OpenAI', model: service.defaultModel, healthy: true, latencyMs };
        }

        const body = await response.text().catch(() => '');
        return {
            provider: 'OpenAI',
            model: service.defaultModel,
            healthy: false,
            error: `HTTP ${response.status}: ${body.substring(0, 200)}`,
            latencyMs,
        };
    } catch (err) {
        return {
            provider: 'OpenAI',
            model: service.defaultModel,
            healthy: false,
            error: `Connection failed: ${(err as Error).message}`,
            latencyMs: Date.now() - start,
        };
    }
}

/**
 * Run a health check for the given service configuration.
 * Results are cached per service ID so repeated calls (from multiple test suites)
 * only hit the API once per provider per process.
 *
 * Throws an error with a clear message if the provider is unhealthy.
 */
export async function checkAPIHealth(service: LLMService): Promise<void> {
    // Return cached result if already checked
    if (!healthCheckCache.has(service.id)) {
        const checkPromise = (service.type === 'anthropic'
            ? checkAnthropicHealth(service)
            : checkOpenAIHealth(service)
        ).then(result => {
            if (result.healthy) {
                console.log(`API Health Check: ${result.provider} (${result.model}) OK (${result.latencyMs}ms)`);
            } else {
                console.error(`API Health Check: ${result.provider} (${result.model}) FAILED - ${result.error}`);
            }
            return result;
        });

        healthCheckCache.set(service.id, checkPromise);
    }

    const result = await healthCheckCache.get(service.id)!;

    if (!result.healthy) {
        throw new Error(
            `API health check failed for ${result.provider} (model: ${result.model}):\n` +
            `  ${result.error}\n\n` +
            `This is likely an upstream API issue, not a test bug.\n` +
            `Check API status: https://status.anthropic.com / https://status.openai.com`
        );
    }
}
