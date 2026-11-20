// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';
import {useIntl} from 'react-intl';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {ToggleSwitch} from '../toggle_switch';

export type MCPToolRowProps = {
    displayName: string;
    description?: string;
    inputSchema?: Record<string, unknown> | null;
    badges?: React.ReactNode;
    policyControl?: React.ReactNode;
    enabled: boolean;
    toggleDisabled?: boolean;
    toggleAriaLabel: string;
    onToggleEnabled: (checked: boolean) => void;
    dimmed?: boolean;
    extraDetails?: React.ReactNode;
};

const MCPToolRow = ({
    displayName,
    description,
    inputSchema,
    badges,
    policyControl,
    enabled,
    toggleDisabled = false,
    toggleAriaLabel,
    onToggleEnabled,
    dimmed = false,
    extraDetails,
}: MCPToolRowProps) => {
    const intl = useIntl();
    const [detailsExpanded, setDetailsExpanded] = useState(false);

    const hasSchema = Boolean(inputSchema && Object.keys(inputSchema).length > 0);
    const hasDetails = Boolean(extraDetails || description || hasSchema);

    return (
        <ToolRowContainer>
            <ToolRowMain>
                <ToolRowLeft $dimmed={dimmed}>
                    <ToolNameLine>
                        <ToolName>{displayName}</ToolName>
                        {badges}
                    </ToolNameLine>
                    {description && (
                        <ToolDescription>{description}</ToolDescription>
                    )}
                </ToolRowLeft>
                <ToolRowRight>
                    {policyControl && (
                        <PolicySlot $dimmed={dimmed}>
                            {policyControl}
                        </PolicySlot>
                    )}
                    <ToggleWrapper $dimmed={dimmed}>
                        <ToggleSwitch
                            checked={enabled}
                            onChange={(checked) => {
                                if (!toggleDisabled) {
                                    onToggleEnabled(checked);
                                }
                            }}
                            disabled={toggleDisabled}
                            size='small'
                            ariaLabel={toggleAriaLabel}
                        />
                    </ToggleWrapper>
                    <DetailsButtonSlot>
                        {hasDetails && (
                            <IconButton
                                icon={<Icon glyph={detailsExpanded ? <ChevronDownIcon/> : <ChevronRightIcon/>}/>}
                                size='small'
                                onClick={() => setDetailsExpanded((prev) => !prev)}
                                aria-label={intl.formatMessage({defaultMessage: 'Show tool details'})}
                                aria-expanded={detailsExpanded}
                            />
                        )}
                    </DetailsButtonSlot>
                </ToolRowRight>
            </ToolRowMain>
            {detailsExpanded && hasDetails && (
                <ExpandedContainer>
                    {description && (
                        <FullDescription>{description}</FullDescription>
                    )}
                    {extraDetails}
                    {hasSchema && (
                        <SchemaContainer>
                            {JSON.stringify(inputSchema, null, 2)}
                        </SchemaContainer>
                    )}
                </ExpandedContainer>
            )}
        </ToolRowContainer>
    );
};

const ToolRowContainer = styled.div`
    display: flex;
    flex-direction: column;
`;

const ToolRowMain = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-m);
    padding: 0 var(--spacing-l) 0 var(--spacing-xxl);
`;

const ToolRowLeft = styled.div<{$dimmed?: boolean}>`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    opacity: ${(props) => (props.$dimmed ? 0.56 : 1)};
`;

const ToolRowRight = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-m);
    flex-shrink: 0;
`;

const PolicySlot = styled.div<{$dimmed?: boolean}>`
    display: flex;
    align-items: center;
    justify-content: flex-end;
    flex-shrink: 0;
    opacity: ${(props) => (props.$dimmed ? 0.64 : 1)};
`;

const ToolNameLine = styled.div`
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--spacing-xs);
`;

const ToolName = styled.div`
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 13px;
    font-weight: 400;
    color: var(--center-channel-color);
    line-height: 20px;
`;

const ToolDescription = styled.div`
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    color: rgba(var(--center-channel-color-rgb), 0.75);
    line-height: var(--line-height-75);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const ToggleWrapper = styled.div<{$dimmed?: boolean}>`
    display: flex;
    align-items: center;
    opacity: ${(props) => (props.$dimmed ? 0.64 : 1)};
`;

const DetailsButtonSlot = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    width: var(--spacing-xxxl);
    flex-shrink: 0;
`;

const ExpandedContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    margin-top: var(--spacing-xs);
    margin-left: var(--spacing-xxl);
    margin-right: var(--spacing-l);
`;

const FullDescription = styled.div`
    font-size: var(--font-size-75);
    line-height: var(--line-height-100);
    color: rgba(var(--center-channel-color-rgb), 0.8);
    white-space: pre-wrap;
`;

const SchemaContainer = styled.pre`
    margin: 0;
    padding: var(--spacing-xs);
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: var(--radius-s);
    font-family: 'Menlo', 'Monaco', 'Courier New', monospace;
    font-size: 11px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    max-height: 200px;
    overflow: auto;
    white-space: pre;
`;

export default MCPToolRow;
