// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {CogOutlineIcon} from '@mattermost/compass-icons/components';

import {AGENTS_ROUTE} from './agents_page';

function dismissMenu() {
    document.getElementById('backdropForMenuComponent')?.click();
}

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

const AgentsDropdown = () => {
    const handleManageAgents = useCallback(() => {
        dismissMenu();
        if (window.WebappUtils?.browserHistory?.push) {
            window.WebappUtils.browserHistory.push(AGENTS_ROUTE);
            return;
        }
        window.location.assign(AGENTS_ROUTE);
    }, []);

    return (
        <StyledMenuItem
            role='menuitem'
            onClick={handleManageAgents}
        >
            <CogOutlineIcon size={16}/>
            <span><FormattedMessage defaultMessage='Manage agents'/></span>
        </StyledMenuItem>
    );
};

export default AgentsDropdown;
