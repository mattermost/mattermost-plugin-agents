// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import styled from 'styled-components';

import {BoundParamTargetChannelSentinel} from '@/types/agents';
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

// --- Parsing helpers shared between tabs ---

// parseAllowedTools turns a comma-separated input into the list of tool
// names the backend expects. Empty chunks are dropped and whitespace is
// trimmed; deliberate laxness — the server still validates reserved names.
export function parseAllowedTools(raw: string): string[] {
    return raw.split(',').map((s) => s.trim()).filter((s) => s.length > 0);
}

// serializeAllowedTools turns the persisted string[] back into a comma-and-
// space list suitable for an <input>.
export function serializeAllowedTools(tools: string[] | undefined): string {
    if (!tools) {
        return '';
    }
    return tools.join(', ');
}

// defaultBoundParamsForAllowedTools auto-wires channel_id = {{TargetChannelID}}
// for any post-writing tool present in allowedTools. Gives users sane
// behaviour without forcing them into the BoundParams weeds on the happy
// path; advanced users can still override via an API-side update later.
export function defaultBoundParamsForAllowedTools(
    allowedTools: string[] | undefined,
): Record<string, Record<string, unknown>> {
    const bound: Record<string, Record<string, unknown>> = {};
    if (!allowedTools || allowedTools.length === 0) {
        return bound;
    }
    for (const name of allowedTools) {
        if (name === 'create_post' || name === 'create_post_in_thread') {
            bound[name] = {channel_id: BoundParamTargetChannelSentinel};
        }
    }
    return bound;
}
