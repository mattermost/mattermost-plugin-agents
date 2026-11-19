// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {CogOutlineIcon} from '@mattermost/compass-icons/components';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenuDivider} from '@mattermost/compass-ui/components/popover-menu';

import {menuIcon} from '@/components/menu/menu_parts';
import {getCustomPrompts, getPinnedPromptIds} from '@/selectors';
import {fetchCustomPrompts, fetchPinnedPromptIds, ShowCustomPromptsModalHandler} from '@/redux';
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

interface Props {
    draft: any;
    getSelectedText: () => {start: number; end: number};
    updateText: (message: string) => void;
    channelId: string;
    isRHS: boolean;
}

const CustomPromptsDropdown = ({updateText, channelId}: Props) => {
    const dispatch = useDispatch();
    const allPrompts = useSelector(getCustomPrompts);
    const pinnedIds = useSelector(getPinnedPromptIds);

    // Pinned prompts (set in Manage prompts) come first.
    const prompts = useMemo(() => {
        const pinned = new Set(pinnedIds);
        return [...allPrompts.filter((p) => pinned.has(p.id)), ...allPrompts.filter((p) => !pinned.has(p.id))];
    }, [allPrompts, pinnedIds]);

    // Selection is backed by the shared selected-agent preference so the
    // "GENERATE WITH:" menu stays in sync with the RHS and all other surfaces.
    const {bots: botlist, activeBot, setActiveBot} = useBotlist();
    const bots = botlist ?? EMPTY_BOTS;
    const isBotDMChannel = bots.some((b: LLMBot) => b.dmChannelID === channelId);
    const selectedBot = activeBot;

    useEffect(() => {
        dispatch(fetchCustomPrompts() as any);
        dispatch(fetchPinnedPromptIds() as any);
    }, [dispatch]);

    const handlePromptClick = useCallback(async (prompt: CustomPrompt) => {
        dismissMenu();
        try {
            const botUsername = selectedBot?.username;
            const result = await renderCustomPrompt(prompt.id, channelId, selectedBot?.id);
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
            {prompts.length > 0 ? (
                prompts.map((prompt) => (
                    <MenuItem
                        key={prompt.id}
                        role='menuitem'
                        label={prompt.name}
                        leadingElement={false}
                        onClick={() => handlePromptClick(prompt)}
                    />
                ))
            ) : (
                <MenuItem
                    role='menuitem'
                    label={<FormattedMessage defaultMessage='No skills yet'/>}
                    leadingElement={false}
                    disabled={true}
                />
            )}
            <PopoverMenuDivider/>
            <MenuItem
                role='menuitem'
                label={<FormattedMessage defaultMessage='Manage skills'/>}
                leadingVisual={menuIcon(<CogOutlineIcon/>)}
                onClick={handleCreateClick}
            />
        </>
    );
};

export default CustomPromptsDropdown;
