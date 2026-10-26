// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';

import {
    MAX_INSTRUCTIONS_RUNES,
    countRunes,
    setChannelAutoReplySaveError,
    useChannelAutoReplyDraft,
} from './autoreply_state';

type Props = {
    informChange: (name: string, value: string) => void;
};

// Static ids are safe: only one Channel Settings modal exists at a time.
const INPUT_ID = 'autoreply-instructions';
const DESCRIPTION_ID = 'autoreply-instructions-description';

// Ambient instructions for the schema-driven channel-settings tab. The host
// hands custom settings nothing but informChange, so saved values come from the
// module-level draft seeded by loadValues.
//
// Known host limitation (documented, do not work around): SaveChangesPanel
// Reset restores host-owned values only — custom components get no signal —
// so after a Reset this field keeps displaying local text while the host
// reverted to baseline. Saving then persists the baseline instructions;
// reopening re-hydrates. Calling informChange from an effect to compensate
// would make the tab instantly dirty on open.
export const AutoReplyInstructionsField = ({informChange}: Props) => {
    const draft = useChannelAutoReplyDraft();
    const [local, setLocal] = useState<string | null>(null);

    if (!draft) {
        return null;
    }

    const value = local ?? draft.saved.instructions;
    const length = countRunes(value);
    const overLimit = length > MAX_INSTRUCTIONS_RUNES;
    const onChange = (event: React.ChangeEvent<HTMLTextAreaElement>) => {
        setLocal(event.target.value);
        setChannelAutoReplySaveError(null);
        informChange('instructions', event.target.value);
    };

    return (
        <Container>
            <Label htmlFor={INPUT_ID}>
                <FormattedMessage defaultMessage='Ambient instructions'/>
            </Label>
            <InstructionsTextArea
                id={INPUT_ID}
                data-testid={INPUT_ID}
                value={value}
                onChange={onChange}
                aria-invalid={overLimit}
                aria-describedby={DESCRIPTION_ID}
            />
            <Footer id={DESCRIPTION_ID}>
                <HelpText>
                    <FormattedMessage defaultMessage='Instructions for when the agent should reply. Used by both ambient modes.'/>
                </HelpText>
                <Counter
                    data-testid='autoreply-instructions-counter'
                    $overLimit={overLimit}
                >
                    <FormattedMessage
                        defaultMessage='{count} / {max}'
                        values={{count: length, max: MAX_INSTRUCTIONS_RUNES}}
                    />
                </Counter>
            </Footer>
            {overLimit && (
                <ErrorMessage
                    message={(
                        <FormattedMessage
                            defaultMessage='Instructions are too long. Shorten them to {max} characters or fewer to save.'
                            values={{max: MAX_INSTRUCTIONS_RUNES}}
                        />
                    )}
                />
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

const Label = styled.label`
    font-weight: 600;
    font-size: 14px;
`;

const Footer = styled.div`
    display: flex;
    justify-content: space-between;
    gap: 8px;
`;

const HelpText = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
`;

const Counter = styled.div<{$overLimit: boolean}>`
    flex-shrink: 0;
    font-size: 12px;
    color: ${({$overLimit}) => ($overLimit ? 'var(--error-text)' : 'rgba(var(--center-channel-color-rgb), 0.72)')};
`;

const InstructionsTextArea = styled.textarea`
    width: 100%;
    padding: 6px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;
    line-height: 20px;
    box-sizing: border-box;
    outline: none;
    resize: vertical;
    min-height: 120px;

    &:focus {
        border-color: var(--button-bg);
    }

    &[aria-invalid='true'] {
        border-color: var(--error-text);
    }
`;
