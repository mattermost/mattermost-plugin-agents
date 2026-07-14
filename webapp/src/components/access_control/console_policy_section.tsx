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

    // Entries added this admin-console session are not yet persisted
    // server-side; policy PUTs are immediate while config saves are deferred,
    // so authoring against them would orphan the policy.
    isPersisted: boolean;
};

// ConsolePolicySection is the collapsible "Access policy" block appended to
// the system console service and MCP server panels. Admin-only surface:
// advanced (CEL) editor only.
const ConsolePolicySection = (props: Props) => {
    const {resourceType, resourceId, resourceDisplayName, isPersisted} = props;
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
                    {isPersisted ? (
                        <PolicyEditor
                            resourceType={resourceType}
                            resourceId={resourceId}
                            resourceDisplayName={resourceDisplayName}
                            allowSimplified={false}
                            allowAdvanced={true}
                        />
                    ) : (
                        <UnsavedNote>
                            <FormattedMessage defaultMessage='Save the configuration first to define an access policy.'/>
                        </UnsavedNote>
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

const UnsavedNote = styled.div`
    font-size: 13px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

export default ConsolePolicySection;
