// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {PlusIcon} from '@mattermost/compass-icons/components';

import {getCustomPrompts, getSelectedBotId} from '@/selectors';
import {fetchCustomPrompts, ShowCustomPromptsModalHandler, SelectedBotIdHandler} from '@/redux';
import {renderCustomPrompt} from '@/client';
import {CustomPrompt} from '@/types';
import {LLMBot} from '@/bots';
import manifest from '@/manifest';
import {DropdownBotSelector} from '@/components/bot_selector';
import {MenuItem, MenuSeparator} from '@/mm_webapp';

const EMPTY_BOTS: LLMBot[] = [];

const AgentSelectorWrapper = styled.div`
    padding: 0 4px;
    margin-bottom: 4px;
`;

interface SubMenuHeaderProps {
    draft: any;
    getSelectedText: () => {start: number; end: number};
    updateText: (message: string) => void;
    channelId: string;
}

export const CustomPromptsSubMenuHeader = ({}: SubMenuHeaderProps) => {
    const dispatch = useDispatch();
    const bots = useSelector((state: any) =>
        state[`plugins-${manifest.id}`]?.bots ?? EMPTY_BOTS,
    );

    const selectedBotId = useSelector(getSelectedBotId);
    const selectedBot = bots.find((b: LLMBot) => b.id === selectedBotId) ?? bots[0] ?? null;

    const isRHS = useSelector((state: any) => {
        const rhsState = state.views?.rhs;
        return rhsState?.isSidebarOpen === true;
    });

    useEffect(() => {
        if (bots.length > 0 && !selectedBotId) {
            dispatch({type: SelectedBotIdHandler, botId: bots[0].id});
        }
    }, [bots, selectedBotId, dispatch]);

    const setSelectedBot = useCallback((bot: LLMBot) => {
        dispatch({type: SelectedBotIdHandler, botId: bot.id});
    }, [dispatch]);

    if (isRHS || bots.length === 0) {
        return null;
    }

    return (
        <AgentSelectorWrapper>
            <DropdownBotSelector
                bots={bots}
                activeBot={selectedBot}
                setActiveBot={setSelectedBot}
            />
        </AgentSelectorWrapper>
    );
};

interface Props {
    draft: any;
    getSelectedText: () => {start: number; end: number};
    updateText: (message: string) => void;
    channelId: string;
}

const CustomPromptsDropdown = ({updateText, channelId}: Props) => {
    const dispatch = useDispatch();
    const prompts = useSelector(getCustomPrompts);
    const bots = useSelector((state: any) =>
        state[`plugins-${manifest.id}`]?.bots ?? EMPTY_BOTS,
    );

    const selectedBotId = useSelector(getSelectedBotId);
    const selectedBot = bots.find((b: LLMBot) => b.id === selectedBotId) ?? bots[0] ?? null;

    const isRHS = useSelector((state: any) => {
        const rhsState = state.views?.rhs;
        return rhsState?.isSidebarOpen === true;
    });

    useEffect(() => {
        dispatch(fetchCustomPrompts() as any);
    }, [dispatch]);

    useEffect(() => {
        if (bots.length > 0 && !selectedBotId) {
            dispatch({type: SelectedBotIdHandler, botId: bots[0].id});
        }
    }, [bots, selectedBotId, dispatch]);

    const handlePromptClick = useCallback(async (prompt: CustomPrompt) => {
        try {
            const botUsername = selectedBot?.username;
            const result = await renderCustomPrompt(prompt.id, channelId, botUsername);
            if (!isRHS && botUsername) {
                updateText(`@${botUsername} ${result.rendered}`);
            } else {
                updateText(result.rendered);
            }
        } catch {
            // Handle error silently
        }
    }, [channelId, updateText, selectedBot, isRHS]);

    const handleCreateClick = useCallback(() => {
        dispatch({type: ShowCustomPromptsModalHandler, show: true});
    }, [dispatch]);

    return (
        <>
            {prompts && prompts.length > 0 ? (
                prompts.map((prompt) => (
                    <MenuItem
                        key={prompt.id}
                        labels={<span>{prompt.name}</span>}
                        onClick={() => handlePromptClick(prompt)}
                    />
                ))
            ) : (
                <MenuItem
                    labels={<span><FormattedMessage defaultMessage='No custom prompts yet'/></span>}
                    isDisabled={true}
                />
            )}
            <MenuSeparator/>
            <MenuItem
                labels={<span><FormattedMessage defaultMessage='Create a prompt'/></span>}
                leadingElement={<PlusIcon size={16}/>}
                onClick={handleCreateClick}
            />
        </>
    );
};

export default CustomPromptsDropdown;
