// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useId} from 'react';
import styled from 'styled-components';
import {ChevronDownIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Tag} from '@mattermost/compass-ui/components/tag';

import {ToggleSwitch} from '../toggle_switch';

export type MCPBulkScope = 'all' | 'read' | 'write';

export type MCPBulkToggleState = {
    checked: boolean;
    disabled: boolean;
    ariaLabel: string;
    onChange: (checked: boolean) => void;
};

export type MCPServerAccordionProps<TTool extends {name: string; readOnlyHint?: boolean}> = {
    serverName: string;
    serverType?: string;
    meta?: React.ReactNode;
    expandAriaLabel?: string;
    expanded: boolean;
    onToggleExpanded: () => void;
    dimmed?: boolean;
    headerActions?: React.ReactNode;
    serverToggle?: {
        checked: boolean;
        disabled: boolean;
        ariaLabel: string;
        onChange: (checked: boolean) => void;
    } | null;
    notices?: React.ReactNode;
    tools: TTool[];
    totalToolCount: number;
    isSearchFiltered?: boolean;
    allToolsDescription: React.ReactNode;
    getBulkToggleState: (tools: TTool[], scope: MCPBulkScope) => MCPBulkToggleState;
    renderBulkPolicy?: (tools: TTool[], scope: MCPBulkScope) => React.ReactNode;
    renderToolRow: (tool: TTool) => React.ReactNode;
};

function MCPServerAccordion<TTool extends {name: string; readOnlyHint?: boolean}>({
    serverName,
    serverType,
    meta,
    expandAriaLabel,
    expanded,
    onToggleExpanded,
    dimmed = false,
    headerActions,
    serverToggle,
    notices,
    tools,
    totalToolCount,
    isSearchFiltered = false,
    allToolsDescription,
    getBulkToggleState,
    renderBulkPolicy,
    renderToolRow,
}: MCPServerAccordionProps<TTool>) {
    const intl = useIntl();
    const panelId = `mcp-tools-${useId()}`;

    // Group tools by the server's readOnlyHint annotation when at least one
    // visible tool is marked read-only; otherwise render a flat list so we do
    // not label unannotated tools as "Write".
    const readOnlyTools = tools.filter((tool) => tool.readOnlyHint);
    const writeTools = tools.filter((tool) => !tool.readOnlyHint);
    const toolGroups = readOnlyTools.length > 0 ? [
        {
            key: 'read' as const,
            tools: readOnlyTools,
            title: intl.formatMessage({defaultMessage: 'Read-only tools'}),
            description: intl.formatMessage({defaultMessage: 'Reported by the server as read-only.'}),
        },
        {
            key: 'write' as const,
            tools: writeTools,
            title: intl.formatMessage({defaultMessage: 'Write tools'}),
            description: intl.formatMessage({defaultMessage: 'Not marked read-only; may create, change, or delete data.'}),
        },
    ].filter((group) => group.tools.length > 0) : [];

    const renderBulkControls = (groupTools: TTool[], scope: MCPBulkScope) => {
        const bulkToggle = getBulkToggleState(groupTools, scope);
        return (
            <BulkControls>
                {renderBulkPolicy && renderBulkPolicy(groupTools, scope)}
                <ToggleWrapper>
                    <ToggleSwitch
                        checked={bulkToggle.checked}
                        disabled={bulkToggle.disabled}
                        onChange={(checked) => {
                            if (!bulkToggle.disabled) {
                                bulkToggle.onChange(checked);
                            }
                        }}
                        size='small'
                        ariaLabel={bulkToggle.ariaLabel}
                    />
                </ToggleWrapper>
                <DetailsButtonSpacer aria-hidden={true}/>
            </BulkControls>
        );
    };

    return (
        <ServerRowContainer $dimmed={dimmed}>
            <ServerRowHeader>
                <ServerRowExpandButton
                    type='button'
                    onClick={onToggleExpanded}
                    aria-expanded={expanded}
                    aria-controls={panelId}
                    aria-label={expandAriaLabel}
                >
                    <ServerAvatar aria-hidden={true}>
                        {serverName.charAt(0).toUpperCase()}
                    </ServerAvatar>
                    <ServerInfo>
                        <ServerName>{serverName}</ServerName>
                        <ServerMeta>
                            {serverType === 'plugin' && (
                                <Tag label={<FormattedMessage defaultMessage='Plugin'/>}/>
                            )}
                            {meta}
                        </ServerMeta>
                    </ServerInfo>
                    <ExpandChevron aria-hidden={true}>
                        <StyledChevron $expanded={expanded}>
                            <ChevronDownIcon size={16}/>
                        </StyledChevron>
                    </ExpandChevron>
                </ServerRowExpandButton>
                {headerActions}
                {serverToggle && (
                    <ToggleWrapper>
                        <ToggleSwitch
                            checked={serverToggle.checked}
                            onChange={(checked) => {
                                if (!serverToggle.disabled) {
                                    serverToggle.onChange(checked);
                                }
                            }}
                            disabled={serverToggle.disabled}
                            size='medium'
                            ariaLabel={serverToggle.ariaLabel}
                        />
                    </ToggleWrapper>
                )}
            </ServerRowHeader>

            {expanded && (
                <ToolsContainer
                    id={panelId}
                    role='region'
                    aria-label={serverName}
                >
                    {notices}
                    {isSearchFiltered && tools.length > 0 && (
                        <FilteredSummary>
                            <FormattedMessage
                                defaultMessage='Showing {shown} of {total} tools matching your search'
                                values={{shown: tools.length, total: totalToolCount}}
                            />
                        </FilteredSummary>
                    )}
                    {tools.length > 0 && (
                        <>
                            {!isSearchFiltered && (
                                <BulkRow>
                                    <BulkLabel>
                                        <BulkTitle>
                                            <FormattedMessage defaultMessage='All tools'/>
                                        </BulkTitle>
                                        <BulkDescription>
                                            {allToolsDescription}
                                        </BulkDescription>
                                    </BulkLabel>
                                    {renderBulkControls(tools, 'all')}
                                </BulkRow>
                            )}
                            {toolGroups.length === 0 && tools.map(renderToolRow)}
                            {toolGroups.map((group) => (
                                <ToolGroup
                                    key={group.key}
                                    role='group'
                                    aria-label={group.title}
                                >
                                    <BulkRow>
                                        <BulkLabel>
                                            <BulkTitle>
                                                {group.title}
                                                <GroupCount>{group.tools.length}</GroupCount>
                                            </BulkTitle>
                                            <BulkDescription>{group.description}</BulkDescription>
                                        </BulkLabel>
                                        {!isSearchFiltered && renderBulkControls(group.tools, group.key)}
                                    </BulkRow>
                                    {group.tools.map(renderToolRow)}
                                </ToolGroup>
                            ))}
                        </>
                    )}
                </ToolsContainer>
            )}
        </ServerRowContainer>
    );
}

const ServerRowContainer = styled.div<{$dimmed?: boolean}>`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: var(--radius-s);
    background-color: var(--center-channel-bg);
    overflow: hidden;
    opacity: ${(props) => (props.$dimmed ? 0.64 : 1)};
`;

const ServerRowHeader = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    padding-right: var(--spacing-l);
`;

const ServerRowExpandButton = styled.button`
    display: flex;
    align-items: center;
    flex: 1;
    min-width: 0;
    padding: var(--spacing-m) var(--spacing-xs) var(--spacing-m) var(--spacing-l);
    cursor: pointer;
    gap: var(--spacing-xs);
    border: none;
    background: none;
    text-align: left;
    font: inherit;
    color: inherit;

    &:hover {
        background-color: rgba(var(--center-channel-color-rgb), 0.04);
    }

    &:focus-visible {
        outline: 2px solid var(--button-bg);
        outline-offset: -2px;
        border-radius: var(--radius-s);
    }
`;

const ServerAvatar = styled.div`
    width: var(--spacing-xxxxl);
    height: var(--spacing-xxxxl);
    border-radius: var(--radius-full);
    background: rgba(var(--center-channel-color-rgb), 0.08);
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    flex-shrink: 0;
`;

const ServerInfo = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    gap: 2px;
`;

const ServerName = styled.div`
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-weight: var(--font-weight-semibold);
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);
    color: var(--center-channel-color);
`;

const ServerMeta = styled.div`
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-xs);
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const ToggleWrapper = styled.div`
    display: flex;
    align-items: center;
    flex-shrink: 0;
`;

const ExpandChevron = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    margin-left: auto;
    padding: var(--spacing-xs);
    flex-shrink: 0;
`;

const StyledChevron = styled.div<{$expanded: boolean}>`
    display: flex;
    align-items: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    transform: ${(props) => (props.$expanded ? 'rotate(0deg)' : 'rotate(-90deg)')};
    transition: transform 0.2s;
`;

const ToolsContainer = styled.div`
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
    padding: var(--spacing-m) 0;
`;

const FilteredSummary = styled.div`
    padding: 0 var(--spacing-l);
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ToolGroup = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
    padding-top: var(--spacing-m);
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const BulkRow = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-m);
    padding: 0 var(--spacing-l);
`;

const BulkLabel = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
`;

const BulkTitle = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-100);
    color: var(--center-channel-color);
`;

const GroupCount = styled.span`
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const BulkDescription = styled.div`
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const BulkControls = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-m);
    flex-shrink: 0;
`;

// Matches MCPToolRow's DetailsButtonSlot so bulk toggles align with per-tool toggles.
const DetailsButtonSpacer = styled.div`
    width: var(--spacing-xxxl);
    flex-shrink: 0;
`;

export default MCPServerAccordion;
