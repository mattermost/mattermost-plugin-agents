// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {useIsMultiLLMLicensed} from '@/license';

type Props = {
    children: React.ReactNode;
}

const AgentsLicenseGate = (props: Props) => {
    const isLicensed = useIsMultiLLMLicensed();

    if (isLicensed) {
        return <>{props.children}</>;
    }

    return (
        <UpgradeContainer>
            <UpgradeTitle>
                <FormattedMessage defaultMessage='Self-Service Agents'/>
            </UpgradeTitle>
            <UpgradeDescription>
                <FormattedMessage defaultMessage='Create and manage custom AI agents for your workspace. Self-service agents require a qualifying Mattermost plan.'/>
            </UpgradeDescription>
            <UpgradeLink
                href='https://mattermost.com/pricing'
                target='_blank'
                rel='noopener noreferrer'
            >
                <FormattedMessage defaultMessage='Learn about Mattermost plans'/>
            </UpgradeLink>
        </UpgradeContainer>
    );
};

// --- Styled Components ---

const UpgradeContainer = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 80px 40px;
    text-align: center;
`;

const UpgradeTitle = styled.h2`
    font-size: 24px;
    font-weight: 600;
    color: var(--center-channel-color);
    margin: 0 0 12px 0;
`;

const UpgradeDescription = styled.p`
    font-size: 14px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    max-width: 480px;
    line-height: 20px;
    margin: 0 0 24px 0;
`;

const UpgradeLink = styled.a`
    font-size: 14px;
    font-weight: 600;
    color: var(--button-bg);

    &:hover {
        text-decoration: underline;
    }
`;

export default AgentsLicenseGate;
