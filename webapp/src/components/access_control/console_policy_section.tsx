// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

import {PolicyResourceType} from '@/types/access_control';
import {useABACSupport} from '@/utils/access_control';

import PolicyEditor from './policy_editor';

type Props = {
    resourceType: PolicyResourceType;
    resourceId: string;
    resourceDisplayName: string;
};

// ConsolePolicySection is the collapsible "Access policy" block appended to
// the system console service and MCP server panels. Admin-only surface:
// advanced (CEL) editor only.
//
// Callers render this only for entries with a persisted id: IDs are minted
// server-side on save (normalizeAdminConfig) and adopted from the PUT
// /admin/config response, so an id-bearing entry is always persisted and
// policy PUTs can never orphan a policy against an unsaved resource.
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
                    {/* Keyed remount: a resource identity change must never
                        leave stale editor state (open delete dialog, advanced
                        lock, draft expression) aimed at the new resource. */}
                    <PolicyEditor
                        key={`${resourceType}-${resourceId}`}
                        resourceType={resourceType}
                        resourceId={resourceId}
                        resourceDisplayName={resourceDisplayName}
                        allowSimplified={false}
                        allowAdvanced={true}
                    />
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

export default ConsolePolicySection;
