// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {Tag} from '@mattermost/compass-ui/components/tag';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

const MCPUnavailableBadge = () => {
    return (
        <WithTooltip
            title={
                <FormattedMessage defaultMessage='This MCP server is set up for service account authentication, so it is not available to this agent. Use an agent with service accounts enabled to access it.'/>
            }
        >
            <Tag
                type='default'
                size='small'
                tabIndex={0}
                label={<FormattedMessage defaultMessage='Unavailable'/>}
            />
        </WithTooltip>
    );
};

export default MCPUnavailableBadge;
