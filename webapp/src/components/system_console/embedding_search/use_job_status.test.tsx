// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {IntlShape} from 'react-intl';

import {NO_STATUS, extractStatusCode, formatReindexError} from './use_job_status';

// Lightweight intl stub: production code only calls intl.formatMessage with a
// defaultMessage. `createIntl` from react-intl rejects messages without an id
// (the babel-plugin-formatjs that normally fills these in does not run under
// ts-jest), so this stub side-steps that and interpolates {placeholders}
// directly.
const intl = {
    formatMessage(
        descriptor: {defaultMessage: string},
        values?: Record<string, string | number>,
    ) {
        let out = descriptor.defaultMessage;
        if (values) {
            for (const [k, v] of Object.entries(values)) {
                out = out.replace(`{${k}}`, String(v));
            }
        }
        return out;
    },
} as IntlShape;

describe('extractStatusCode', () => {
    test('returns the numeric status_code from a ClientError-like object', () => {
        expect(extractStatusCode({status_code: 409})).toBe(409);
    });

    test('returns NO_STATUS for a native fetch error (no status_code)', () => {
        expect(extractStatusCode(new Error('network down'))).toBe(NO_STATUS);
    });

    test('returns NO_STATUS for non-error values', () => {
        expect(extractStatusCode(null)).toBe(NO_STATUS);
        expect(extractStatusCode('boom')).toBe(NO_STATUS);
    });

    test('ignores status_code when it is not a number', () => {
        expect(extractStatusCode({status_code: 'four-oh-nine'})).toBe(NO_STATUS);
    });
});

describe('formatReindexError', () => {
    const action = 'Reindexing';

    test('401 returns a session-expired hint', () => {
        const msg = formatReindexError({status_code: 401, message: ''}, intl, action);
        expect(msg).toMatch(/session has expired/i);
    });

    test('403 returns an admin-privileges hint', () => {
        const msg = formatReindexError({status_code: 403, message: ''}, intl, action);
        expect(msg).toMatch(/system administrator privileges/i);
    });

    test('409 prefers the server message when present', () => {
        const msg = formatReindexError(
            {status_code: 409, message: 'A reindex job is already running.'},
            intl,
            action,
        );
        expect(msg).toBe('A reindex job is already running.');
    });

    test('409 falls back to a default conflict message when the server message is empty', () => {
        const msg = formatReindexError({status_code: 409, message: ''}, intl, action);
        expect(msg).toMatch(/already running/i);
    });

    test('NO_STATUS (network failure) returns a connectivity hint mentioning the action', () => {
        const msg = formatReindexError(new Error('TypeError: Failed to fetch'), intl, action);
        expect(msg).toMatch(/could not reach the server/i);
        expect(msg).toContain(action);
    });

    test('other 5xx with a server message renders "<action> failed: <message>"', () => {
        const msg = formatReindexError(
            {status_code: 500, message: 'failed to start reindex job: boom'},
            intl,
            action,
        );
        expect(msg).toBe(`${action} failed: failed to start reindex job: boom`);
    });

    test('other 5xx without a server message tells the admin to check the server logs', () => {
        const msg = formatReindexError({status_code: 500, message: ''}, intl, action);
        expect(msg).toMatch(/check the server logs/i);
        expect(msg).toContain(action);
    });

    test('4xx with a server message (other than 401/403/409) also renders "<action> failed: <message>"', () => {
        const msg = formatReindexError(
            {status_code: 400, message: 'invalid request body'},
            intl,
            action,
        );
        expect(msg).toBe(`${action} failed: invalid request body`);
    });
});
