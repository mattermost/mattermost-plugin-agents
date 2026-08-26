// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {useChannelAutoReplyDraft} from './autoreply_state';

type Props = {
    informChange: (name: string, value: string) => void;
};

// Static ids are safe: only one Channel Settings modal exists at a time.
const INSTRUCTIONS_ID = 'autoreply-instructions';
const ANALYSIS_MODEL_ID = 'autoreply-analysis-model';

// Ambient extras for the schema-driven channel-settings tab. The host hands
// custom settings nothing but informChange, so saved values come from the
// module-level draft seeded by loadValues.
//
// Known host limitation (documented, do not work around): SaveChangesPanel
// Reset restores host-owned values only — custom components get no signal —
// so after a Reset these fields keep displaying local text while the host
// reverted to baseline. Saving then persists baseline extras; reopening
// re-hydrates. Calling informChange from an effect to compensate would make
// the tab instantly dirty on open. POC assumption: extras stay always
// visible; they cannot follow the live radio.

export const AutoReplyInstructionsField = ({informChange}: Props) => {
    const draft = useChannelAutoReplyDraft();
    const [local, setLocal] = useState<string | null>(null);

    if (!draft) {
        return null;
    }

    const value = local ?? draft.saved.instructions;

    return (
        <Container>
            <Label htmlFor={INSTRUCTIONS_ID}>
                <FormattedMessage defaultMessage='Ambient instructions'/>
            </Label>
            <InstructionsTextArea
                id={INSTRUCTIONS_ID}
                data-testid='autoreply-instructions'
                value={value}
                onChange={(event: React.ChangeEvent<HTMLTextAreaElement>) => {
                    setLocal(event.target.value);
                    informChange('instructions', event.target.value);
                }}
            />
            <HelpText>
                <FormattedMessage defaultMessage='Instructions for when the agent should reply. Used when Ambient is selected.'/>
            </HelpText>
        </Container>
    );
};

// TODO(ambient-poc): replace this free-text input with a channel-authorized
// model discovery/validation endpoint. POC assumption: the value is an
// unvalidated model id string.
export const AutoReplyAnalysisModelField = ({informChange}: Props) => {
    const draft = useChannelAutoReplyDraft();
    const [local, setLocal] = useState<string | null>(null);

    if (!draft) {
        return null;
    }

    const value = local ?? draft.saved.analysis_model;

    return (
        <Container>
            <Label htmlFor={ANALYSIS_MODEL_ID}>
                <FormattedMessage defaultMessage='Analysis model'/>
            </Label>
            <ModelInput
                id={ANALYSIS_MODEL_ID}
                data-testid='autoreply-analysis-model'
                type='text'
                value={value}
                onChange={(event: React.ChangeEvent<HTMLInputElement>) => {
                    setLocal(event.target.value);
                    informChange('analysis_model', event.target.value);
                }}
            />
            <HelpText>
                <FormattedMessage defaultMessage='Model id used to decide whether to reply. Used when Ambient is selected.'/>
            </HelpText>
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

const HelpText = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
`;

const fieldChrome = `
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

    &:focus {
        border-color: var(--button-bg);
    }
`;

const InstructionsTextArea = styled.textarea`
    ${fieldChrome}
    resize: vertical;
    min-height: 120px;
`;

const ModelInput = styled.input`
    ${fieldChrome}
`;
