// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {CogOutlineIcon} from '@mattermost/compass-icons/components';

import {dismissLegacyMenu, getHostMenuComponents} from '@/components/ai_actions_menu_utils';

import {AGENTS_ROUTE} from './agents_page';

const StyledMenuItem = styled.li`
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 20px;
    cursor: pointer;
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);
    list-style: none;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const MenuLabel = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 8px;
`;

const AgentsDropdown = () => {
    const handleManageAgents = useCallback(() => {
        if (window.WebappUtils?.browserHistory?.push) {
            window.WebappUtils.browserHistory.push(AGENTS_ROUTE);
            return;
        }
        window.location.assign(AGENTS_ROUTE);
    }, []);

    const HostMenuItem = getHostMenuComponents()?.Item;
    if (HostMenuItem) {
        return (
            <HostMenuItem
                labels={<MenuLabel><FormattedMessage defaultMessage='Manage agents'/></MenuLabel>}
                leadingElement={<CogOutlineIcon size={16}/>}
                onClick={handleManageAgents}
            />
        );
    }

    return (
        <StyledMenuItem
            role='menuitem'
            onClick={() => {
                dismissLegacyMenu();
                handleManageAgents();
            }}
        >
            <CogOutlineIcon size={16}/>
            <span><FormattedMessage defaultMessage='Manage agents'/></span>
        </StyledMenuItem>
    );
};

export default AgentsDropdown;
