// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createIntl, type IntlShape} from 'react-intl';

import {changedFieldLabel, changedFieldLabels} from './agent_version_labels';

// Message ids are derived from defaultMessage at build time; mirror that here.
const real = createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
const intl = {
    formatMessage: (descriptor: {id?: string; defaultMessage: string}) =>
        real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}),
} as unknown as IntlShape;

describe('changedFieldLabel', () => {
    test.each([
        {field: 'customInstructions', expected: 'Custom instructions'},
        {field: 'documents', expected: 'Reference documents'},
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
