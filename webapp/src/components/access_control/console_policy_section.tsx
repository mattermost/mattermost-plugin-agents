// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {PolicyResourceType} from '@/types/access_control';
import {isValidMattermostId, useABACSupport} from '@/utils/access_control';

import Accordion from '../system_console/accordion';

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
        <Accordion
            title={<FormattedMessage defaultMessage='Access policy'/>}
            expanded={expanded}
            onToggle={toggleExpanded}
            keepMounted={hasOpened}
            contentCollapsed={!expanded}
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
        </Accordion>
    );
};

const LegacyIDNote = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    text-align: left;
`;

export default ConsolePolicySection;
