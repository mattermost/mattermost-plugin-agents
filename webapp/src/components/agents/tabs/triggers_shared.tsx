// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import styled from 'styled-components';

import {BoundParamTargetChannelSentinel, EnabledTool, TriggerScopeBinding} from '@/types/agents';
import {PrimaryButton} from '@/components/assets/buttons';

// --- Styled primitives shared between subscriptions_tab and schedules_tab ---

export const SectionsContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 24px;
`;

export const SectionTitle = styled.h3`
    font-size: 16px;
    font-weight: 600;
    color: var(--center-channel-color);
    margin: 0;
`;

export const SectionDescription = styled.p`
    font-size: 14px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    margin: 4px 0 0;
    line-height: 20px;
`;

export const Row = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 6px;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
`;

export const RowHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
`;

export const RowBody = styled.div`
    display: flex;
    flex-direction: column;
    gap: 12px;
`;

export const EmptyState = styled.div`
    padding: 24px;
    text-align: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 14px;
    border: 1px dashed rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 6px;
`;

export const BottomActions = styled.div`
    display: flex;
    justify-content: flex-start;
`;

export const AddTriggerButton = styled(PrimaryButton)`
    height: 36px;
`;

// defaultBoundParamsForAllowedTools auto-wires channel_id = {{TargetChannelID}}
// for any post-writing tool present in allowedTools. Gives users sane
// behaviour without forcing them into the BoundParams weeds on the happy
// path; advanced users can still override via an API-side update later.
export function defaultBoundParamsForAllowedTools(
    allowedTools: string[] | undefined,
    scope?: TriggerScopeBinding,
): Record<string, Record<string, unknown>> {
    const bound: Record<string, Record<string, unknown>> = {};
    if (!allowedTools || allowedTools.length === 0) {
        return bound;
    }
    for (const name of allowedTools) {
        if (name === 'create_post' || name === 'create_post_in_thread') {
            bound[name] = {channel_id: BoundParamTargetChannelSentinel};
        }
        const scoped = scopeBoundParamsForTool(name, scope);
        if (Object.keys(scoped).length > 0) {
            bound[name] = {...(bound[name] ?? {}), ...scoped};
        }
    }
    return bound;
}

export const boundParamsForToolsAndScope = defaultBoundParamsForAllowedTools;
export const boundParamsForTrigger = defaultBoundParamsForAllowedTools;

export function toolNamesFromEnabledTools(tools: EnabledTool[] | undefined): string[] {
    return (tools ?? []).map((tool) => tool.tool_name).filter((name) => name !== '');
}

export function enabledToolsFromAllowedTools(tools: string[] | undefined): EnabledTool[] {
    return (tools ?? []).map((name) => ({server_origin: '', tool_name: name}));
}

function allowedValues(values: string[] | undefined): {allowed_values: string[]} | null {
    const filtered = (values ?? []).filter(Boolean);
    if (filtered.length === 0) {
        return null;
    }
    return {allowed_values: filtered};
}

function scopeBoundParamsForTool(
    toolName: string,
    scope: TriggerScopeBinding | undefined,
): Record<string, unknown> {
    const channelScope = allowedValues(scope?.channelIDs);
    const teamScope = allowedValues(scope?.teamIDs);
    const bound: Record<string, unknown> = {};

    switch (toolName) {
    case 'read_channel':
    case 'get_channel_info':
    case 'get_channel_members':
    case 'add_user_to_channel':
    case 'read_post':
    case 'search_posts':
    case 'create_channel':
    case 'get_user_channels':
        if (channelScope) {
            bound.channel_id = channelScope;
        }
        if (teamScope) {
            bound.team_id = teamScope;
        }
        break;
    case 'get_team_info':
    case 'get_team_members':
        if (teamScope) {
            bound.team_id = teamScope;
        }
        break;
    }

    return bound;
}

export function triggerScopeFromBoundParams(
    boundParams: Record<string, Record<string, unknown>> | undefined,
): TriggerScopeBinding {
    const teamIDs = new Set<string>();
    const channelIDs = new Set<string>();
    for (const params of Object.values(boundParams ?? {})) {
        collectAllowedValues(params.team_id, teamIDs);
        collectAllowedValues(params.channel_id, channelIDs);
    }
    return {
        teamIDs: Array.from(teamIDs),
        channelIDs: Array.from(channelIDs),
    };
}

export const scopeFromBoundParams = triggerScopeFromBoundParams;

function collectAllowedValues(value: unknown, target: Set<string>) {
    if (!value || typeof value !== 'object' || !('allowed_values' in value)) {
        return;
    }
    const allowed = (value as {allowed_values?: unknown}).allowed_values;
    if (!Array.isArray(allowed)) {
        return;
    }
    for (const item of allowed) {
        if (typeof item === 'string' && item !== '') {
            target.add(item);
        }
    }
}
