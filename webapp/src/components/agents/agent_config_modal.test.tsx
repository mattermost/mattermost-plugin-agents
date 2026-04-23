// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createIntl} from 'react-intl';

import {getAgentSaveErrorState} from './agent_save_errors';

const intl = createIntl({
    locale: 'en',
    messages: {},
});

describe('getAgentSaveErrorState', () => {
    test('maps deleted service validation to the service field', () => {
        const state = getAgentSaveErrorState({
            status_code: 400,
            message: 'service "old-service" not found in configuration',
        }, intl);

        expect(state.activeTab).toBe('config');
        expect(state.errors.serviceId).toBe('The selected AI service is no longer available. Select another service and try again.');
    });

    test('maps custom instruction length validation to the field', () => {
        const state = getAgentSaveErrorState({
            status_code: 400,
            message: 'invalid agent configuration: customInstructions exceeds maximum length of 16384 characters',
        }, intl);

        expect(state.activeTab).toBe('config');
        expect(state.errors.customInstructions).toBe('Custom instructions are too long. Shorten them and try again.');
    });

    test('keeps internal server errors generic to avoid leaking details', () => {
        const state = getAgentSaveErrorState({
            status_code: 500,
            message: 'failed to persist agent: duplicate key value violates unique constraint "agents_useragents_pkey"',
        }, intl);

        expect(state.activeTab).toBeUndefined();
        expect(state.errors.general).toBe('Failed to save agent. Please try again.');
    });

    test('shows license guidance for quota failures', () => {
        const state = getAgentSaveErrorState({
            status_code: 403,
            message: 'creating more than 1 self-service agent(s) requires an E20 or Enterprise license',
        }, intl);

        expect(state.errors.general).toBe('Creating additional agents requires an E20 or Enterprise license.');
    });
});
