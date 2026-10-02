// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useId} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

// eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {Tag} from '@mattermost/compass-ui/components/tag';

import {getPortalTarget} from '@/utils/dom';

const BadgeTrigger = styled.span`
    display: inline-flex;
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
            <BadgeTrigger>
                <Tag
                    type='default'
                    size='small'
                    label={<FormattedMessage defaultMessage='Unavailable'/>}
                />
            </BadgeTrigger>
        </OverlayTrigger>
    );
};

export default MCPUnavailableBadge;
