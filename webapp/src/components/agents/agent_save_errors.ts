// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

type Tab = 'config' | 'access' | 'mcps';

export type SaveErrorState = {
    activeTab?: Tab;
    errors: Record<string, string>;
}

export function buildUsernameValidationMessage(intl: IntlShape): string {
    return intl.formatMessage({
        id: 'agents.error.username.invalid',
        defaultMessage: 'Username must start with a letter and contain only lowercase letters, numbers, periods, hyphens, and underscores',
    });
}

export function getAgentSaveErrorState(error: {status_code?: number; message?: string} | undefined, intl: IntlShape): SaveErrorState {
    const statusCode = error?.status_code;
    const message = error?.message?.trim() ?? '';
    const lower = message.toLowerCase();

    if (statusCode === 409 || (lower.includes('username') && (lower.includes('taken') || lower.includes('conflict')))) {
        return {
            activeTab: 'config',
            errors: {username: intl.formatMessage({id: 'agents.error.username.taken', defaultMessage: 'This username is already taken'})},
        };
    }

    if (statusCode === 403) {
        if (lower.includes('requires an e20 or enterprise license')) {
            return {
                errors: {
                    general: intl.formatMessage({id: 'agents.error.license_required', defaultMessage: 'Creating additional agents requires an E20 or Enterprise license.'}),
                },
            };
        }

        return {
            errors: {general: intl.formatMessage({id: 'agents.error.permission_denied', defaultMessage: 'You do not have permission to perform this action.'})},
        };
    }

    if (statusCode === 400 || statusCode === 413) {
        if (lower.includes('invalid username')) {
            return {
                activeTab: 'config',
                errors: {username: buildUsernameValidationMessage(intl)},
            };
        }

        if (lower.includes('username cannot be changed')) {
            return {
                activeTab: 'config',
                errors: {username: intl.formatMessage({id: 'agents.error.username_locked', defaultMessage: 'The username cannot be changed after the agent is created.'})},
            };
        }

        if (lower.includes('displayname is required')) {
            return {
                activeTab: 'config',
                errors: {displayName: intl.formatMessage({id: 'agents.error.display_name_required', defaultMessage: 'Display name is required'})},
            };
        }

        if (lower.includes('serviceid is required')) {
            return {
                activeTab: 'config',
                errors: {serviceId: intl.formatMessage({id: 'agents.error.service_required', defaultMessage: 'AI Service is required'})},
            };
        }

        if (lower.includes('not found in configuration') && lower.includes('service')) {
            return {
                activeTab: 'config',
                errors: {
                    serviceId: intl.formatMessage({id: 'agents.error.service_deleted', defaultMessage: 'The selected AI service is no longer available. Select another service and try again.'}),
                },
            };
        }

        if (lower.includes('custominstructions exceeds maximum length')) {
            return {
                activeTab: 'config',
                errors: {
                    customInstructions: intl.formatMessage({id: 'agents.error.custom_instructions_too_long', defaultMessage: 'Custom instructions are too long. Shorten them and try again.'}),
                },
            };
        }

        if (lower.includes('request body too large')) {
            return {
                errors: {
                    general: intl.formatMessage({id: 'agents.error.request_too_large', defaultMessage: 'The agent configuration is too large to save. Reduce the amount of content and try again.'}),
                },
            };
        }

        return {
            errors: {
                general: intl.formatMessage({id: 'agents.error.invalid_settings', defaultMessage: 'Some agent settings are invalid. Review your changes and try again.'}),
            },
        };
    }

    if (statusCode === 404) {
        return {
            errors: {
                general: intl.formatMessage({id: 'agents.error.not_found', defaultMessage: 'This agent no longer exists. Refresh the page and try again.'}),
            },
        };
    }

    return {
        errors: {general: intl.formatMessage({id: 'agents.error.save_failed', defaultMessage: 'Failed to save agent. Please try again.'})},
    };
}
