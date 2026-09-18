// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {ChevronDownIcon, ChevronUpIcon} from '@mattermost/compass-icons/components';

import {PolicyResourceType} from '@/types/access_control';
import {isValidMattermostId, useABACSupport} from '@/utils/access_control';

import PolicyEditor from './policy_editor';

type Props = {
    resourceType: PolicyResourceType;
    resourceId: string;
    resourceDisplayName: string;
};

// legacyIDNote explains why the editor is absent for a resource with a
// legacy ID: such IDs can never carry a policy.
function legacyIDNote(resourceType: PolicyResourceType) {
    switch (resourceType) {
    case 'service':
        return <FormattedMessage defaultMessage="Access policies aren't available for this service because it has a legacy ID."/>;
    case 'mcp':
        return <FormattedMessage defaultMessage="Access policies aren't available for this MCP server because it has a legacy ID."/>;
    case 'agent':
        return <FormattedMessage defaultMessage="Access policies aren't available for this agent because it has a legacy ID."/>;
    default: {
        const exhaustive: never = resourceType;
        throw new Error(`unknown resource type: ${exhaustive}`);
    }
    }
}

// ConsolePolicySection is the collapsible "Access policy" block on the system
// console service and MCP server panels; admin-only, with Simple (table) and
// Advanced (CEL) editors matching the sysadmin agent Access tab.
// Callers render it only for entries with a persisted id (minted server-side
// on save), so policy PUTs can never orphan a policy against an unsaved
// resource. Persisted legacy IDs get an explanatory note instead.
const ConsolePolicySection = (props: Props) => {
    const {resourceType, resourceId, resourceDisplayName} = props;
    const {supported} = useABACSupport();
    const [expanded, setExpanded] = useState(false);
    const [hasOpened, setHasOpened] = useState(false);

    if (!supported || !resourceId) {
        return null;
    }

    const toggleExpanded = () => {
        if (!expanded) {
            setHasOpened(true);
        }
        setExpanded(!expanded);
    };

    return (
        <SectionContainer $collapsed={hasOpened && !expanded}>
            <SectionHeader
                role='button'
                tabIndex={0}
                aria-expanded={expanded}
                onClick={toggleExpanded}
                onKeyDown={(e: React.KeyboardEvent) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        toggleExpanded();
                    }
                }}
            >
                <SectionTitle>
                    <FormattedMessage defaultMessage='Access policy'/>
                </SectionTitle>
                {expanded ? <ChevronUpIcon size={16}/> : <ChevronDownIcon size={16}/>}
            </SectionHeader>
            {(expanded || hasOpened) && (
                <SectionContent
                    $collapsed={!expanded}
                    {...collapsedInert(expanded)}
                >
                    {isValidMattermostId(resourceId) ? (
                        <PolicyEditor
                            resourceType={resourceType}
                            resourceId={resourceId}
                            resourceDisplayName={resourceDisplayName}
                            allowSimplified={true}
                            allowAdvanced={true}
                        />
                    ) : (
                        <LegacyIDNote>{legacyIDNote(resourceType)}</LegacyIDNote>
                    )}
                </SectionContent>
            )}
        </SectionContainer>
    );
};

// Omit inert when expanded: React 18 serializes inert={false} as inert="false",
// which browsers still treat as inert.
function collapsedInert(expanded: boolean): {inert?: ''} {
    return expanded ? {} : {inert: ''};
}

// --- Styled Components ---

const SectionContainer = styled.div<{$collapsed: boolean}>`
    display: flex;
    flex-direction: column;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    overflow: hidden;
    text-align: left;
    ${({$collapsed}) => $collapsed && `
        position: relative;
    `}
`;

const SectionHeader = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 12px 14px;
    cursor: pointer;
    user-select: none;
    background-color: rgba(var(--center-channel-color-rgb), 0.02);
    color: rgba(var(--center-channel-color-rgb), 0.56);

    &:hover {
        background-color: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const SectionTitle = styled.div`
    font-weight: 600;
    font-size: 13px;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const SectionContent = styled.div<{$collapsed: boolean; inert?: ''}>`
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 16px;
    padding: 16px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    text-align: left;
    ${({$collapsed}) => $collapsed && `
        visibility: hidden;
        position: absolute;
        left: 0;
        right: 0;
        overflow: hidden;
        clip-path: inset(50%);
        pointer-events: none;
    `}
`;

const LegacyIDNote = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    text-align: left;
`;

export default ConsolePolicySection;
