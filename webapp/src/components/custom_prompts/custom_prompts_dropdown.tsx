// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {CogOutlineIcon} from '@mattermost/compass-icons/components';

import {getCustomPrompts} from '@/selectors';
import {fetchCustomPrompts, ShowCustomPromptsModalHandler} from '@/redux';
import {renderCustomPrompt} from '@/client';
import {CustomPrompt} from '@/types';
import {LLMBot, useBotlist} from '@/bots';
import {DropdownBotSelector} from '@/components/bot_selector';

const EMPTY_BOTS: LLMBot[] = [];

function dismissMenu() {
    document.getElementById('backdropForMenuComponent')?.click();
}

const AgentSelectorWrapper = styled.div`
    padding: 0 4px;
    margin-bottom: 4px;
`;

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

    &[aria-disabled='true'] {
        cursor: default;
        color: rgba(var(--center-channel-color-rgb), 0.40);

        &:hover {
            background: none;
        }
    }
`;

const StyledMenuSeparator = styled.li`
    height: 1px;
    margin: 4px 0;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    list-style: none;
`;

interface Props {
    draft: any;
    getSelectedText: () => {start: number; end: number};
    updateText: (message: string) => void;
    channelId: string;
    isRHS: boolean;
}

const CustomPromptsDropdown = ({updateText, channelId}: Props) => {
    const dispatch = useDispatch();
    const prompts = useSelector(getCustomPrompts);

    // Selection is backed by the shared selected-agent preference so the
    // "GENERATE WITH:" menu stays in sync with the RHS and all other surfaces.
    const {bots: botlist, activeBot, setActiveBot} = useBotlist();
    const bots = botlist ?? EMPTY_BOTS;
    const isBotDMChannel = bots.some((b: LLMBot) => b.dmChannelID === channelId);
    const selectedBot = activeBot;

    useEffect(() => {
        dispatch(fetchCustomPrompts() as any);
    }, [dispatch]);

    const handlePromptClick = useCallback(async (prompt: CustomPrompt) => {
        dismissMenu();
        try {
            const botUsername = selectedBot?.username;
            const result = await renderCustomPrompt(prompt.id, channelId, botUsername);
            if (!isBotDMChannel && botUsername) {
                updateText(`@${botUsername} ${result.rendered}`);
            } else {
                updateText(result.rendered);
            }
        } catch (e) {
            console.error('Failed to render custom prompt:', e); // eslint-disable-line no-console
        }
    }, [channelId, updateText, selectedBot, isBotDMChannel]);

    const handleCreateClick = useCallback(() => {
        dismissMenu();
        dispatch({type: ShowCustomPromptsModalHandler, show: true});
    }, [dispatch]);

    const showBotSelector = !isBotDMChannel && bots.length > 0;

    return (
        <>
            {showBotSelector && (
                <AgentSelectorWrapper>
                    <DropdownBotSelector
                        bots={bots}
                        activeBot={selectedBot}
                        setActiveBot={setActiveBot}
                    />
                </AgentSelectorWrapper>
            )}
            {prompts && prompts.length > 0 ? (
                prompts.map((prompt) => (
                    <StyledMenuItem
                        key={prompt.id}
                        role='menuitem'
                        onClick={() => handlePromptClick(prompt)}
                    >
                        <span>{prompt.name}</span>
                    </StyledMenuItem>
                ))
            ) : (
                <StyledMenuItem
                    role='menuitem'
                    aria-disabled='true'
                >
                    <span><FormattedMessage defaultMessage='No custom prompts yet'/></span>
                </StyledMenuItem>
            )}
            <StyledMenuSeparator role='separator'/>
            <StyledMenuItem
                role='menuitem'
                onClick={handleCreateClick}
            >
                <CogOutlineIcon size={16}/>
                <span><FormattedMessage defaultMessage='Manage prompts'/></span>
            </StyledMenuItem>
        </>
    );
};

export default CustomPromptsDropdown;
