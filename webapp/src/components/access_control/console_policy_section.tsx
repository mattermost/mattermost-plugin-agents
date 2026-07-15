// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

import {PolicyResourceType} from '@/types/access_control';
import {isValidMattermostId, useABACSupport} from '@/utils/access_control';

import PolicyEditor from './policy_editor';

type Props = {
    resourceType: PolicyResourceType;
    resourceId: string;
    resourceDisplayName: string;
};

// legacyIDNote explains why the editor is absent for a resource whose stored
// ID is a hand-crafted legacy string (raw config PUT before server-side
// minting): such IDs can never carry a policy — the PDP short-circuits them
// to no_policy — so authoring against them is meaningless.
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

// ConsolePolicySection is the collapsible "Access policy" block appended to
// the system console service and MCP server panels. Admin-only surface:
// advanced (CEL) editor only.
//
// Callers render this only for entries with a persisted id: IDs are minted
// server-side on save (normalizeAdminConfig) and adopted from the PUT
// /admin/config response, so an id-bearing entry is always persisted and
// policy PUTs can never orphan a policy against an unsaved resource.
// Persisted legacy IDs (hand-set before minting existed) still reach here;
// they get an explanatory note instead of the editor.
const ConsolePolicySection = (props: Props) => {
    const {resourceType, resourceId, resourceDisplayName} = props;
    const {supported} = useABACSupport();
    const [expanded, setExpanded] = useState(false);

    if (!supported || !resourceId) {
        return null;
    }

    return (
        <SectionContainer>
            <SectionHeader
                role='button'
                tabIndex={0}
                aria-expanded={expanded}
                onClick={() => setExpanded(!expanded)}
                onKeyDown={(e: React.KeyboardEvent) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        setExpanded(!expanded);
                    }
                }}
            >
                {expanded ? <ChevronDownIcon size={16}/> : <ChevronRightIcon size={16}/>}
                <SectionTitle>
                    <FormattedMessage defaultMessage='Access policy'/>
                </SectionTitle>
            </SectionHeader>
            {expanded && (
                <SectionContent>
                    {isValidMattermostId(resourceId) ? (
                        <PolicyEditor
                            resourceType={resourceType}
                            resourceId={resourceId}
                            resourceDisplayName={resourceDisplayName}
                            allowSimplified={false}
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

// --- Styled Components ---

const SectionContainer = styled.div`
    margin-top: 16px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    padding-top: 12px;
`;

const SectionHeader = styled.div`
    display: flex;
    align-items: center;
    gap: 4px;
    cursor: pointer;
    user-select: none;
`;

const SectionTitle = styled.div`
    font-size: 14px;
    font-weight: 600;
`;

const SectionContent = styled.div`
    margin-top: 12px;
`;

const LegacyIDNote = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

export default ConsolePolicySection;
