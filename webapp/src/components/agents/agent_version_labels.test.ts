// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createIntl, type IntlShape} from 'react-intl';

import {changedFieldLabel, changedFieldLabels, differingConfigFields, formatRelativeTime} from './agent_version_labels';

// Message ids are derived from defaultMessage at build time; mirror that here.
const real = createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
const intl = {
    formatMessage: (descriptor: {id?: string; defaultMessage: string}) =>
        real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}),
    formatRelativeTime: real.formatRelativeTime,
} as unknown as IntlShape;

describe('changedFieldLabel', () => {
    test.each([
        {field: 'customInstructions', expected: 'Custom instructions'},
        {field: 'skills', expected: 'Skills'},
        {field: 'structuredOutputEnabled', expected: 'Structured output enabled'},
        {field: 'maxFileSize', expected: 'Max file size'},
        {field: 'botUserID', expected: 'Bot user ID'},
        {field: 'someNewMCPSetting', expected: 'Some new MCP setting'},
        {field: 'service', expected: 'Service'},
    ])('labels $field as "$expected"', ({field, expected}) => {
        expect(changedFieldLabel(intl, field)).toBe(expected);
    });
});

describe('changedFieldLabels', () => {
    test('de-duplicates keys that share a label', () => {
        expect(changedFieldLabels(intl, ['userIDs', 'userAccessLevel', 'futureField'])).toEqual(['User access', 'Future field']);
    });
});

describe('formatRelativeTime', () => {
    const now = 1760000000000;
    const second = 1000;
    const minute = 60 * second;
    const hour = 60 * minute;
    const day = 24 * hour;

    test.each([
        {name: 'under a minute', ago: 30 * second, expected: 'now'},
        {name: 'just over a minute', ago: minute + second, expected: '1m ago'},
        {name: 'rounds down within the hour', ago: (59 * minute) + (59 * second), expected: '59m ago'},
        {name: 'hours', ago: (5 * hour) + (30 * minute), expected: '5h ago'},
        {name: 'a day', ago: day, expected: '1d ago'},
        {name: 'days', ago: 2 * day, expected: '2d ago'},
        {name: 'a week', ago: 7 * day, expected: '1w ago'},
        {name: 'weeks', ago: 21 * day, expected: '3w ago'},
        {name: 'months', ago: 90 * day, expected: '3mo ago'},
        {name: 'the last days of the year, not 12 months', ago: 364 * day, expected: '11mo ago'},
        {name: 'a year', ago: 365 * day, expected: '1y ago'},
        {name: 'years', ago: 800 * day, expected: '2y ago'},
    ])('formats $name', ({ago, expected}) => {
        expect(formatRelativeTime(intl, now - ago, now)).toBe(expected);
    });

    test.each([
        {name: 'slightly', ahead: 10 * second},
        {name: 'by a few minutes of clock skew', ahead: 5 * minute},
        {name: 'by hours', ahead: 3 * hour},
    ])('treats a timestamp in the future $name as now', ({ahead}) => {
        expect(formatRelativeTime(intl, now + ahead, now)).toBe('now');
    });
});

describe('differingConfigFields', () => {
    test('returns the keys whose values differ, including keys present on one side only', () => {
        const a = {model: 'a', customInstructions: 'same', enabledNativeTools: ['web_search'], channelIDs: null};
        const b = {model: 'b', customInstructions: 'same', enabledNativeTools: [], extra: true};

        expect(differingConfigFields(a, b).sort()).toEqual(['enabledNativeTools', 'extra', 'model']);
    });

    test('ignores ids, the username and keys that are not part of a version snapshot', () => {
        const a = {id: 'one', name: 'helper', service: {id: 's'}, structuredOutputEnabled: true, model: 'a'};
        const b = {id: 'two', name: 'helper2', model: 'a'};

        expect(differingConfigFields(a, b)).toEqual([]);
    });

    test('treats a missing value and null as equal', () => {
        expect(differingConfigFields({mcpDynamicToolLoading: null}, {})).toEqual([]);
    });
});
