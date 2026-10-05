// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';

import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuScroll, PopoverMenuTitle} from '@mattermost/compass-ui/components/popover-menu';

const Menu = styled(PopoverMenu)`
    min-width: 300px;
`;

const VariableName = styled.span`
    font-family: 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'Liberation Mono', monospace;
`;

interface TemplateVariable {
    name: string;
    preview: string;
}

function useTemplateVariables(): TemplateVariable[] {
    const currentUser = useSelector((state: any) => {
        const userId = state.entities?.users?.currentUserId;
        return userId ? state.entities?.users?.profiles?.[userId] : null;
    });
    const currentChannelId = useSelector((state: any) => state.entities?.channels?.currentChannelId);
    const currentChannel = useSelector((state: any) =>
        (currentChannelId ? state.entities?.channels?.channels?.[currentChannelId] : null),
    );
    const currentTeamId = useSelector((state: any) => state.entities?.teams?.currentTeamId);
    const currentTeam = useSelector((state: any) =>
        (currentTeamId ? state.entities?.teams?.teams?.[currentTeamId] : null),
    );

    return [
        {name: '{{.Username}}', preview: currentUser?.username ? `@${currentUser.username}` : ''},
        {name: '{{.FirstName}}', preview: currentUser?.first_name || ''},
        {name: '{{.LastName}}', preview: currentUser?.last_name || ''},
        {name: '{{.Channel}}', preview: currentChannel?.display_name || ''},
        {name: '{{.ChannelName}}', preview: currentChannel?.name || ''},
        {name: '{{.Team}}', preview: currentTeam?.display_name || ''},
        {name: '{{.TeamName}}', preview: currentTeam?.name || ''},
        {name: '{{.Time}}', preview: new Date().toUTCString()},
        {name: '{{.BotName}}', preview: ''},
    ];
}

interface Props {
    onSelect: (variable: string) => void;
}

const ContextVariablesDropdown = ({onSelect}: Props) => {
    const variables = useTemplateVariables();

    return (
        <Menu>
            <PopoverMenuTitle>
                <FormattedMessage defaultMessage='Context Variables'/>
            </PopoverMenuTitle>
            <PopoverMenuScroll maxHeight={280}>
                {variables.map((variable) => (
                    <MenuItem
                        key={variable.name}
                        label={<VariableName>{variable.name}</VariableName>}
                        {...(variable.preview ? {secondaryLabel: variable.preview} : {})}
                        leadingElement={false}
                        onClick={() => onSelect(variable.name)}
                    />
                ))}
            </PopoverMenuScroll>
        </Menu>
    );
};

export default ContextVariablesDropdown;
