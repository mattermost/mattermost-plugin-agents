// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon} from '@mattermost/compass-icons/components';

type Props = {
    hint: React.ReactNode;
    expanded: boolean;
    onToggle: () => void;
    children: React.ReactNode;
}

const AdvancedSection = ({hint, expanded, onToggle, children}: Props) => (
    <Section>
        <Header
            type='button'
            aria-expanded={expanded}
            onClick={onToggle}
        >
            <ChevronContainer aria-hidden={true}>
                {expanded ? <ChevronDownIcon size={16}/> : <ChevronRightIcon size={16}/>}
            </ChevronContainer>
            <HeaderText>
                <FormattedMessage defaultMessage='Advanced configuration'/>
            </HeaderText>
            <HeaderHint>{hint}</HeaderHint>
        </Header>
        {expanded && <Content>{children}</Content>}
    </Section>
);

const Section = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    overflow: hidden;
`;

const Header = styled.button`
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 12px 16px;
    border: none;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    cursor: pointer;
    text-align: left;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ChevronContainer = styled.span`
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    flex-shrink: 0;
`;

const HeaderText = styled.span`
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const HeaderHint = styled.span`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    margin-left: auto;
`;

const Content = styled.div`
    padding: 24px 16px;
`;

export default AdvancedSection;
