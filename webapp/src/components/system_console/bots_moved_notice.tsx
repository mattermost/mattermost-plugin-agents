// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import manifest from '@/manifest';

const Notice = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px 16px;
    background: rgba(63, 67, 80, 0.04);
    border-radius: 4px;
    border: 1px solid rgba(63, 67, 80, 0.12);
`;

const Title = styled.div`
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
`;

const Body = styled.div`
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(63, 67, 80, 0.72);
`;

const agentsPath = `/plug/${manifest.id}/agents`;

const BotsMovedNotice = () => {
    return (
        <Notice>
            <Title>
                <FormattedMessage defaultMessage='AI bot configuration has moved'/>
            </Title>
            <Body>
                <FormattedMessage
                    defaultMessage='Create and manage AI agents from the <link>Agents page</link>. System administrators can still set the default bot below.'
                    values={{
                        link: (chunks: React.ReactNode) => (
                            <a href={agentsPath}>{chunks}</a>
                        ),
                    }}
                />
            </Body>
            <div>
                <a href={agentsPath}>
                    <FormattedMessage defaultMessage='Open Agents'/>
                </a>
            </div>
        </Notice>
    );
};

export default BotsMovedNotice;
