// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useId} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

// eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {getPortalTarget} from '@/utils/dom';

const Badge = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 10px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 11px;
    font-weight: 600;
    white-space: nowrap;
    cursor: default;
`;

const MCPUnavailableBadge = () => {
    const tooltipId = useId();

    return (
        <OverlayTrigger
            placement='top'
            container={getPortalTarget}
            overlay={
                <Tooltip id={tooltipId}>
                    <FormattedMessage defaultMessage='This MCP server is set up for service account authentication, so it is not available to this agent. Use an agent with service accounts enabled to access it.'/>
                </Tooltip>
            }
        >
            <span>
                <Badge>
                    <FormattedMessage defaultMessage='Unavailable'/>
                </Badge>
            </span>
        </OverlayTrigger>
    );
};

export default MCPUnavailableBadge;
