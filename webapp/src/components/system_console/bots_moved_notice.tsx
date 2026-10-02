// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';

import manifest from '@/manifest';

const Description = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const agentsPath = `/plug/${manifest.id}/agents`;

const BotsMovedNotice = () => {
    return (
        <SectionNotice
            type='info'
            title={<FormattedMessage defaultMessage='AI bot configuration has moved'/>}
            description={(
                <Description>
                    <span>
                        <FormattedMessage
                            defaultMessage='Create and manage AI agents from the <link>Agents page</link>. System administrators can still set the default bot below.'
                            values={{
                                link: (chunks: React.ReactNode) => (
                                    <a href={agentsPath}>{chunks}</a>
                                ),
                            }}
                        />
                    </span>
                    <span>
                        <a href={agentsPath}>
                            <FormattedMessage defaultMessage='Open Agents'/>
                        </a>
                    </span>
                </Description>
            )}
        />
    );
};

export default BotsMovedNotice;
