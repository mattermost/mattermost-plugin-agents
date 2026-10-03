// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';

import manifest from '@/manifest';

const agentsPath = `/plug/${manifest.id}/agents`;

const BotsMovedNotice = () => {
    return (
        <SectionNotice
            type='info'
            title={<FormattedMessage defaultMessage='AI bot configuration has moved'/>}
            description={
                <FormattedMessage defaultMessage='Create and manage AI agents from the Agents page. System administrators can still set the default bot below.'/>
            }
            primaryButtonLabel={<FormattedMessage defaultMessage='Open Agents'/>}
            onPrimaryAction={() => window.location.assign(agentsPath)}
        />
    );
};

export default BotsMovedNotice;
