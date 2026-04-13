// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';

const DropdownContainer = styled.div`
    position: absolute;
    right: 0;
    z-index: 100;
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
    min-width: 300px;
    max-height: 320px;
    overflow-y: auto;
    padding: 8px 0;
`;

const DropdownHeader = styled.div`
    padding: 6px 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    letter-spacing: 0.48px;
    text-transform: uppercase;
`;

const VariableItem = styled.button`
    display: flex;
    flex-direction: column;
    width: 100%;
    padding: 8px 16px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const VariableName = styled.span`
    font-family: 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'Liberation Mono', monospace;
    font-size: 13px;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const VariablePreview = styled.span`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
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
    onClose: () => void;
}

const ContextVariablesDropdown = ({onSelect, onClose}: Props) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const variables = useTemplateVariables();

    useEffect(() => {
        const handleClickOutside = (e: MouseEvent) => {
            if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
                onClose();
            }
        };
        document.addEventListener('mousedown', handleClickOutside);
        return () => document.removeEventListener('mousedown', handleClickOutside);
    }, [onClose]);

    return (
        <DropdownContainer ref={containerRef}>
            <DropdownHeader>
                <FormattedMessage defaultMessage='Context Variables'/>
            </DropdownHeader>
            {variables.map((variable) => (
                <VariableItem
                    key={variable.name}
                    onClick={() => onSelect(variable.name)}
                >
                    <VariableName>{variable.name}</VariableName>
                    {variable.preview && (
                        <VariablePreview>{variable.preview}</VariablePreview>
                    )}
                </VariableItem>
            ))}
        </DropdownContainer>
    );
};

export default ContextVariablesDropdown;
