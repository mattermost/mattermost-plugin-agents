/**
 * Server Log Scanner for E2E Tests
 *
 * Scans the server-logs.log file for error patterns that indicate
 * upstream API failures, giving clear diagnostic information when tests fail.
 */
import fs from 'fs';
import path from 'path';
import { TestInfo } from '@playwright/test';

const LOG_FILE = path.join(__dirname, '../logs/server-logs.log');

/** Error patterns that indicate upstream API issues */
const API_ERROR_PATTERNS = [
    { pattern: /Streaming result to post failed partway/i, category: 'LLM Streaming Error' },
    { pattern: /LLM closed stream with no result/i, category: 'LLM Empty Response' },
    { pattern: /error from anthropic stream/i, category: 'Anthropic API Error' },
    { pattern: /error.*accessing the LLM/i, category: 'LLM Access Error' },
    { pattern: /timeout streaming/i, category: 'Streaming Timeout' },
    { pattern: /status.?code.*[45]\d\d/i, category: 'API HTTP Error' },
    { pattern: /rate.?limit/i, category: 'API Rate Limit' },
    { pattern: /overloaded/i, category: 'API Overloaded' },
    { pattern: /authentication.*error|invalid.*api.*key|unauthorized/i, category: 'API Auth Error' },
    { pattern: /connection.*refused|ECONNREFUSED|ETIMEDOUT/i, category: 'Network Error' },
];

export interface LogScanResult {
    hasErrors: boolean;
    errors: Array<{
        category: string;
        line: string;
    }>;
    summary: string;
}

/**
 * Scan the server log file for API error patterns.
 * Only scans the last 200 lines to avoid false positives from earlier tests.
 */
export function scanServerLogs(): LogScanResult {
    const result: LogScanResult = {
        hasErrors: false,
        errors: [],
        summary: '',
    };

    if (!fs.existsSync(LOG_FILE)) {
        return result;
    }

    let content: string;
    try {
        content = fs.readFileSync(LOG_FILE, 'utf-8');
    } catch {
        return result;
    }
    const lines = content.split('\n').slice(-200);

    for (const line of lines) {
        for (const { pattern, category } of API_ERROR_PATTERNS) {
            if (pattern.test(line)) {
                result.hasErrors = true;
                const truncatedLine = line.length > 500 ? line.substring(0, 500) + '...' : line;
                result.errors.push({ category, line: truncatedLine });
                break;
            }
        }
    }

    if (result.hasErrors) {
        const categories = [...new Set(result.errors.map(e => e.category))];
        result.summary = `Server logs contain API errors: ${categories.join(', ')}. ` +
            `This may indicate an upstream API issue rather than a test bug. ` +
            `Check the server-logs artifact for full details.`;
    }

    return result;
}

/**
 * Get a formatted error message suitable for appending to test failure messages.
 * Returns empty string if no errors found.
 */
export function getAPIErrorContext(): string {
    const scan = scanServerLogs();
    if (!scan.hasErrors) return '';

    const errorLines = scan.errors
        .slice(0, 5)
        .map(e => `  [${e.category}] ${e.line}`)
        .join('\n');

    return `\n\n--- API Error Context from Server Logs ---\n${scan.summary}\n\nRecent errors:\n${errorLines}\n---`;
}

/**
 * Scan server logs for API errors on test failure and attach context to the
 * Playwright HTML report. Call from test.afterEach in real API test suites.
 *
 * Only runs for failed or timed-out tests (skipped/interrupted are ignored).
 */
export async function attachAPIErrorContext(testInfo: TestInfo): Promise<void> {
    if (testInfo.status !== 'failed' && testInfo.status !== 'timedOut') {
        return;
    }

    const scan = scanServerLogs();
    if (!scan.hasErrors) {
        return;
    }

    console.error(`\n=== API Error Context ===\n${scan.summary}`);
    for (const error of scan.errors.slice(0, 5)) {
        console.error(`  [${error.category}] ${error.line}`);
    }
    console.error('=========================\n');

    await testInfo.attach('api-error-context', {
        body: JSON.stringify(scan, null, 2),
        contentType: 'application/json',
    });
}
