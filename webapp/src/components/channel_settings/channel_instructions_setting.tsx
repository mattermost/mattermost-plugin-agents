// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {LightbulbOutlineIcon} from '@mattermost/compass-icons/components';

import {setChannelInstructionsSaveError, useChannelInstructionsDraft} from './channel_instructions_state';

// Mirrors channelcontext.MaxInstructionsRunes on the server. maxLength counts
// UTF-16 code units, which is never fewer than the server's rune count.
export const MAX_CHANNEL_INSTRUCTIONS_LENGTH = 8000;

const TEXTAREA_ID = 'channel-instructions-textarea';
const HELP_ID = 'channel-instructions-help';

type Props = {
    informChange: (name: string, value: string) => void;
};

// The custom instructions setting for the channel-settings Agents tab. Like
// the auto-reply agent picker, it gets nothing but informChange from the host,
// so the channel and saved text come from the module-level draft seeded by
// loadValues, and it shares the picker's documented Reset limitation: the
// textarea keeps a local edit after the host's Reset.
export const ChannelInstructionsSetting = ({informChange}: Props) => {
    const intl = useIntl();
    const draft = useChannelInstructionsDraft();

    // null follows the draft so remote changes show through until the user types.
    const [localValue, setLocalValue] = useState<string | null>(null);

    if (!draft) {
        return (
            <ErrorText>
                <FormattedMessage defaultMessage='Channel instructions could not be loaded. Close the dialog and try again.'/>
            </ErrorText>
        );
    }

    const value = localValue ?? draft.saved;

    return (
        <Container>
            <TextArea
                id={TEXTAREA_ID}
                aria-label={intl.formatMessage({defaultMessage: 'Channel instructions for agents'})}
                aria-describedby={HELP_ID}
                data-testid='channel-instructions-textarea'
                rows={6}
                maxLength={MAX_CHANNEL_INSTRUCTIONS_LENGTH}
                placeholder={intl.formatMessage({defaultMessage: 'For example: This channel coordinates the payments team. Deploys freeze on Fridays, and bugs are tracked in the PAY Jira project.'})}
                value={value}
                onChange={(e) => {
                    setLocalValue(e.target.value);
                    setChannelInstructionsSaveError(null);
                    informChange('instructions', e.target.value);
                }}
            />
            <Footer>
                <HelpText id={HELP_ID}>
                    <FormattedMessage defaultMessage='Agents working in this channel receive these instructions with every request, as optional background they use only when it is relevant.'/>
                </HelpText>
                <Counter>{`${value.length}/${MAX_CHANNEL_INSTRUCTIONS_LENGTH}`}</Counter>
            </Footer>
            <Hint data-testid='channel-context-pin-hint'>
                <HintIcon size={16}/>
                <span>
                    <FormattedMessage
                        defaultMessage='You can also pin individual posts to agent context: open the AI Actions menu on a post and select <b>Pin to agent context</b>. Agent context pins are separate from the channel’s pinned messages.'
                        values={{b: (chunks: React.ReactNode) => <strong>{chunks}</strong>}}
                    />
                </span>
            </Hint>
            {draft.saveError === 'forbidden' && (
                <ErrorText>
                    <FormattedMessage defaultMessage='You don’t have permission to change the instructions for this channel.'/>
                </ErrorText>
            )}
            {draft.saveError === 'invalid' && (
                <ErrorText>
                    <FormattedMessage
                        defaultMessage='Instructions can be at most {max} characters.'
                        values={{max: MAX_CHANNEL_INSTRUCTIONS_LENGTH}}
                    />
                </ErrorText>
            )}
            {draft.saveError === 'generic' && (
                <ErrorText>
                    <FormattedMessage defaultMessage='Failed to save channel instructions. Please try again.'/>
                </ErrorText>
            )}
        </Container>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const TextArea = styled.textarea`
    width: 100%;
    min-height: 120px;
    padding: 8px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;
    line-height: 20px;
    resize: vertical;

    &:focus {
        border-color: var(--button-bg);
        box-shadow: inset 0 0 0 1px var(--button-bg);
        outline: none;
    }
`;

const Footer = styled.div`
    display: flex;
    justify-content: space-between;
    gap: 16px;
`;

const HelpText = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
`;

const Counter = styled.div`
    flex-shrink: 0;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 12px;
`;

const Hint = styled.div`
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 8px 12px;
    border-radius: 4px;
    background: rgba(var(--button-bg-rgb), 0.08);
    color: rgba(var(--center-channel-color-rgb), 0.88);
    font-size: 12px;
    line-height: 16px;
`;

const HintIcon = styled(LightbulbOutlineIcon)`
    flex-shrink: 0;
    color: var(--button-bg);
`;

const ErrorText = styled.div`
    color: var(--error-text);
    font-size: 12px;
`;
