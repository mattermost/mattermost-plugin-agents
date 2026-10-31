// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {Spinner} from '@mattermost/compass-ui/components/spinner';

// Shown while an agent chat's DM channel is set up (created if missing, and loaded), in the Agents
// product and the RHS.
const ChatSetupStatus = () => {
    const intl = useIntl();
    return (
        <Status role='status'>
            <Spinner
                size='20'
                aria-hidden={true}
            />
            <span>{intl.formatMessage({defaultMessage: 'Setting up chat…'})}</span>
        </Status>
    );
};

const Status = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--spacing-s);
    min-height: 0;
    padding: var(--spacing-xl);
`;

export default ChatSetupStatus;
