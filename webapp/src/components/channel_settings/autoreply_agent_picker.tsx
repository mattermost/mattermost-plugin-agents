// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {ChevronDownIcon} from '@mattermost/compass-icons/components';

import {LLMBot, useBotlistForChannel} from '@/bots';
import {getProfilePictureUrl} from '@/client';
import {BotDropdown} from '@/components/bot_selector';

import {setChannelAutoReplySaveError, useChannelAutoReplyDraft} from './autoreply_state';

type Props = {
    informChange: (name: string, value: string) => void;
};

// The custom bot_id setting for the channel-settings auto-reply tab. The host
// hands custom settings nothing but informChange, so the current channel and
// saved values come from the module-level draft seeded by loadValues.
//
// Known host limitation (documented, do not work around): the SaveChangesPanel
// Reset restores host-owned values only — custom components get no signal — so
// after a Reset the picker keeps displaying a local pick while the host's
// bot_id reverted to baseline. Saving then persists baseline values (server
// state unchanged) and reopening the modal re-hydrates. Calling informChange
// from an effect to compensate would make the tab instantly dirty on open.
export const AutoReplyAgentPicker = ({informChange}: Props) => {
    const draft = useChannelAutoReplyDraft();
    const {bots} = useBotlistForChannel(draft?.channelId ?? '');

    // Local selection overrides the draft display after a user pick; null
    // means "follow the draft store" so websocket re-syncs show through until
    // the user touches the control.
    const [localBotId, setLocalBotId] = useState<string | null>(null);

    if (!draft) {
        // Only reachable when loadValues rejected (e.g. 403 from the
        // default-agent middleware) and the host fell back to schema defaults.
        // Saving in this state PUTs mode 'off'.
        return (
            <ErrorText>
                <FormattedMessage defaultMessage='Auto-reply settings could not be loaded. Close the dialog and try again.'/>
            </ErrorText>
        );
    }

    const selectedId = localBotId ?? draft.saved.bot_id;
    const selectedBot = bots.find((bot) => bot.id === selectedId) ?? null;

    return (
        <Container>
            <Label>
                <FormattedMessage defaultMessage='Auto-replying agent'/>
            </Label>
            <BotDropdown
                bots={bots}
                activeBot={selectedBot}
                setActiveBot={(bot: LLMBot) => {
                    setLocalBotId(bot.id);
                    setChannelAutoReplySaveError(null);
                    informChange('bot_id', bot.id);
                }}
                container={PickerButtonContainer}
                testId='autoreply-agent-picker'
            >
                {selectedBot ? (
                    <>
                        <AgentAvatar src={getProfilePictureUrl(selectedBot.id, selectedBot.lastIconUpdate)}/>
                        <AgentName>{selectedBot.displayName}</AgentName>
                        <ChevronDownIcon/>
                    </>
                ) : (
                    <>
                        <AgentName>
                            <FormattedMessage defaultMessage='Select an agent'/>
                        </AgentName>
                        <ChevronDownIcon/>
                    </>
                )}
            </BotDropdown>
            <HelpText>
                <FormattedMessage defaultMessage='This agent posts the automatic replies. The reply runs with the message author’s permissions.'/>
            </HelpText>
            {draft.saveError === 'forbidden' && (
                <ErrorText>
                    <FormattedMessage defaultMessage='You don’t have permission to change auto-reply settings for this channel.'/>
                </ErrorText>
            )}
            {draft.saveError === 'no_agent' && (
                <ErrorText>
                    <FormattedMessage defaultMessage='Select an agent to enable automatic replies.'/>
                </ErrorText>
            )}
            {draft.saveError === 'generic' && (
                <ErrorText>
                    <FormattedMessage defaultMessage='Failed to save auto-reply settings. Please try again.'/>
                </ErrorText>
            )}
        </Container>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-top: 16px;
`;

const Label = styled.div`
    font-weight: 600;
    font-size: 14px;
`;

const HelpText = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
`;

const ErrorText = styled.div`
    color: var(--error-text);
    font-size: 12px;
`;

const PickerButtonContainer = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    width: fit-content;
    cursor: pointer;
`;

const AgentAvatar = styled.img`
    border-radius: 50%;
    width: 24px;
    height: 24px;
`;

const AgentName = styled.span`
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
`;
