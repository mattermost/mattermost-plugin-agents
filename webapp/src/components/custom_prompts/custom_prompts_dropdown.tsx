// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useCallback, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {CogOutlineIcon, SendIcon} from '@mattermost/compass-icons/components';

import {getCustomPrompts} from '@/selectors';
import {fetchCustomPrompts, ShowCustomPromptsModalHandler} from '@/redux';
import {renderCustomPrompt} from '@/client';
import {CustomPrompt} from '@/types';
import {LLMBot, useBotlist} from '@/bots';
import {DropdownBotSelector} from '@/components/bot_selector';

import {useRunPromptImmediately} from './use_run_prompt';

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

// Marks prompts that post on selection, so the menu doesn't send without warning.
const SendAffordance = styled.span`
    display: inline-flex;
    margin-left: auto;
    padding-left: 8px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ErrorItem = styled.li`
    padding: 6px 20px;
    font-family: 'Open Sans', sans-serif;
    font-size: 12px;
    line-height: 16px;
    color: var(--error-text);
    list-style: none;
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

const CustomPromptsDropdown = ({draft, updateText, channelId}: Props) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const prompts = useSelector(getCustomPrompts);
    const runPromptImmediately = useRunPromptImmediately();
    const [error, setError] = useState('');

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
        const botUsername = selectedBot?.username;
        const mentionBot = !isBotDMChannel && Boolean(botUsername);

        // Prompts marked "send without review" skip the draft entirely and
        // post the rendered text, matching the pinned-button behavior in
        // the Agents pane. The draft is left untouched either way.
        if (prompt.run_immediately) {
            // Hold the menu open until the post lands. This path leaves no
            // draft behind, so a failure that only logged to the console
            // would be indistinguishable from a successful send.
            setError('');
            try {
                await runPromptImmediately(prompt.id, {
                    channelId,
                    botUsername,
                    mentionBot,
                    rootId: draft?.rootId,
                });
                dismissMenu();
            } catch (e) {
                console.error('Failed to run custom prompt:', e); // eslint-disable-line no-console
                setError(intl.formatMessage({defaultMessage: 'Could not send the prompt. Nothing was posted — try again.'}));
            }
            return;
        }

        dismissMenu();
        try {
            const result = await renderCustomPrompt(prompt.id, channelId, botUsername);
            if (mentionBot) {
                updateText(`@${botUsername} ${result.rendered}`);
            } else {
                updateText(result.rendered);
            }
        } catch (e) {
            console.error('Failed to run custom prompt:', e); // eslint-disable-line no-console
        }
    }, [channelId, draft, updateText, selectedBot, isBotDMChannel, runPromptImmediately, intl]);

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
                        {prompt.run_immediately && (
                            <SendAffordance
                                aria-label={intl.formatMessage({defaultMessage: 'Sends without review'})}
                                title={intl.formatMessage({defaultMessage: 'Sends without review'})}
                            >
                                <SendIcon size={14}/>
                            </SendAffordance>
                        )}
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
            {error && (
                <ErrorItem role='alert'>{error}</ErrorItem>
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
