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
    case 'system':
        return intl.formatMessage({defaultMessage: 'System'});
    default:
        return intl.formatMessage({defaultMessage: 'Edited'});
    }
}

const MINUTE = 60;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const WEEK = 7 * DAY;
const MONTH = 30 * DAY;
const YEAR = 365 * DAY;

// formatRelativeTime renders compact "2d ago" style text, rounding down so a
// version never reads as older than it is. A timestamp ahead of the clock
// (client and server clocks disagree) reads as "now", never "in 2m".
export function formatRelativeTime(intl: IntlShape, timestamp: number, now: number = Date.now()): string {
    const abs = Math.trunc((now - timestamp) / 1000);
    const options = {style: 'narrow', numeric: 'always'} as const;

    if (abs < MINUTE) {
        return intl.formatRelativeTime(0, 'second', {numeric: 'auto'});
    }
    if (abs < HOUR) {
        return intl.formatRelativeTime(-Math.floor(abs / MINUTE), 'minute', options);
    }
    if (abs < DAY) {
        return intl.formatRelativeTime(-Math.floor(abs / HOUR), 'hour', options);
    }
    if (abs < WEEK) {
        return intl.formatRelativeTime(-Math.floor(abs / DAY), 'day', options);
    }
    if (abs < MONTH) {
        return intl.formatRelativeTime(-Math.floor(abs / WEEK), 'week', options);
    }
    if (abs < YEAR) {
        // Months are 30 days, so the last days of the year would round up to 12.
        return intl.formatRelativeTime(-Math.min(11, Math.floor(abs / MONTH)), 'month', options);
    }
    return intl.formatRelativeTime(-Math.floor(abs / YEAR), 'year', options);
}

// Not reported as changes: lifecycle ids, deprecated or non-snapshot keys, and
// the username, which a restore never changes.
const IGNORED_CONFIG_FIELDS = new Set(['id', 'name', 'service', 'structuredOutputEnabled']);

// differingConfigFields returns the keys whose values differ between two
// version snapshots, e.g. what restoring one over the other would change.
export function differingConfigFields(a: Record<string, unknown>, b: Record<string, unknown>): string[] {
    const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
    return Array.from(keys).filter((key) => (
        !IGNORED_CONFIG_FIELDS.has(key) && JSON.stringify(a[key] ?? null) !== JSON.stringify(b[key] ?? null)
    ));
}

// changedFieldLabel maps a BotConfig JSON key to a label for a version's changes.
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
    case 'skills':
        return intl.formatMessage({defaultMessage: 'Skills'});
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
