// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {IntlShape} from 'react-intl';

import {AgentVersionSummary} from '@/types/agents';

export function versionSourceLabel(intl: IntlShape, summary: Pick<AgentVersionSummary, 'source' | 'restoredFromVersion'>): string {
    switch (summary.source) {
    case 'initial':
        return intl.formatMessage({defaultMessage: 'Initial version'});
    case 'create':
        return intl.formatMessage({defaultMessage: 'Created'});
    case 'update':
        return intl.formatMessage({defaultMessage: 'Edited'});
    case 'restore':
        if (summary.restoredFromVersion > 0) {
            return intl.formatMessage(
                {defaultMessage: 'Restored from version {version}'},
                {version: summary.restoredFromVersion},
            );
        }
        return intl.formatMessage({defaultMessage: 'Restored'});
    case 'import':
        return intl.formatMessage({defaultMessage: 'Imported'});
    case 'system':
        return intl.formatMessage({defaultMessage: 'System'});
    default:
        return intl.formatMessage({defaultMessage: 'Edited'});
    }
}

// changedFieldLabel maps a BotConfig JSON key to a label for the History tab.
export function changedFieldLabel(intl: IntlShape, field: string): string {
    switch (field) {
    case 'name':
        return intl.formatMessage({defaultMessage: 'Username'});
    case 'displayName':
        return intl.formatMessage({defaultMessage: 'Display name'});
    case 'customInstructions':
        return intl.formatMessage({defaultMessage: 'Custom instructions'});
    case 'serviceID':
        return intl.formatMessage({defaultMessage: 'AI service'});
    case 'model':
        return intl.formatMessage({defaultMessage: 'Model'});
    case 'enableVision':
        return intl.formatMessage({defaultMessage: 'Vision'});
    case 'disableTools':
        return intl.formatMessage({defaultMessage: 'Tools'});
    case 'channelAccessLevel':
    case 'channelIDs':
        return intl.formatMessage({defaultMessage: 'Channel access'});
    case 'userAccessLevel':
    case 'userIDs':
    case 'teamIDs':
        return intl.formatMessage({defaultMessage: 'User access'});
    case 'adminUserIDs':
        return intl.formatMessage({defaultMessage: 'Agent admins'});
    case 'enabledNativeTools':
        return intl.formatMessage({defaultMessage: 'Native tools'});
    case 'enabledMCPTools':
        return intl.formatMessage({defaultMessage: 'MCP tools'});
    case 'autoEnableNewMCPTools':
        return intl.formatMessage({defaultMessage: 'All MCP tools'});
    case 'mcpDynamicToolLoading':
        return intl.formatMessage({defaultMessage: 'Dynamic tool loading'});
    case 'useServiceAccountAuth':
        return intl.formatMessage({defaultMessage: 'Service account authentication'});
    case 'reasoningEnabled':
    case 'reasoningEffort':
    case 'thinkingBudget':
        return intl.formatMessage({defaultMessage: 'Reasoning'});
    case 'maxToolTurns':
        return intl.formatMessage({defaultMessage: 'Max tool turns'});
    case 'documents':
        return intl.formatMessage({defaultMessage: 'Reference documents'});
    default:
        return humanizeFieldKey(field);
    }
}

// humanizeFieldKey turns a camelCase key into sentence case, keeping
// acronyms: "maxFileSize" -> "Max file size", "botUserID" -> "Bot user ID".
export function humanizeFieldKey(field: string): string {
    const words = field.match(/[A-Z]+(?![a-z])|[A-Z]?[a-z]+|\d+/g);
    if (!words) {
        return field;
    }
    return words.map((word, i) => {
        if (word.length > 1 && word === word.toUpperCase()) {
            return word;
        }
        const lower = word.toLowerCase();
        return i === 0 ? lower.charAt(0).toUpperCase() + lower.slice(1) : lower;
    }).join(' ');
}

// changedFieldLabels returns de-duplicated, human-readable labels (several raw
// keys can share one label, e.g. userIDs and userAccessLevel).
export function changedFieldLabels(intl: IntlShape, fields: string[] | null | undefined): string[] {
    const labels: string[] = [];
    for (const field of fields ?? []) {
        const label = changedFieldLabel(intl, field);
        if (!labels.includes(label)) {
            labels.push(label);
        }
    }
    return labels;
}
