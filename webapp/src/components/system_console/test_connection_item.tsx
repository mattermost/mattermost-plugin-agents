// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {testService} from '@/client';

import {TertiaryButton} from '../assets/buttons';

import {FieldControlRow, FieldErrorText, FormRow, HelpText, ItemLabel, TextFieldContainer} from './item';
import {connectionFingerprint} from './service_connection';

// Type-only: service.tsx imports this component back, and erasing the type
// import keeps that cycle out of the emitted module graph.
import type {LLMService} from './service';

const SuccessText = styled.div`
	color: var(--online-indicator, #3DB887);
	font-size: 12px;
	display: flex;
	align-items: center;
	gap: 4px;
`;

// ResultText keeps a provider's own error readable: these can be long, and
// wrapping them beats truncating the one piece of information the admin needs.
const ResultText = styled(FieldErrorText)`
	white-space: pre-wrap;
	overflow-wrap: anywhere;
`;

type TestState =
    | {status: 'idle'}
    | {status: 'testing'}
    | {status: 'success'}
    | {status: 'failure'; error: string};

type Props = {
    service: LLMService
}

export const TestConnectionItem = (props: Props) => {
    const intl = useIntl();
    const [state, setState] = useState<TestState>({status: 'idle'});

    // A result describes the configuration that produced it, so editing a field
    // the provider call depends on clears it rather than leaving a stale
    // "Connection successful" next to a key the admin has since changed.
    // Keyed on the shared fingerprint so this and the save-time probe can never
    // disagree about which fields matter.
    const fingerprint = connectionFingerprint(props.service);
    useEffect(() => {
        setState({status: 'idle'});
    }, [fingerprint]);

    const runTest = async () => {
        setState({status: 'testing'});
        try {
            const result = await testService(props.service);
            if (result.ok) {
                setState({status: 'success'});
            } else {
                setState({
                    status: 'failure',
                    error: result.error || intl.formatMessage({defaultMessage: 'The provider did not accept the request.'}),
                });
            }
        } catch (error) {
            setState({
                status: 'failure',
                error: intl.formatMessage({defaultMessage: 'Could not reach the server to run the test.'}),
            });
        }
    };

    return (
        <FormRow>
            <ItemLabel>{intl.formatMessage({defaultMessage: 'Connection'})}</ItemLabel>
            <TextFieldContainer>
                <FieldControlRow>
                    <TertiaryButton
                        onClick={runTest}
                        disabled={state.status === 'testing'}
                    >
                        {state.status === 'testing' ? intl.formatMessage({defaultMessage: 'Testing...'}) : intl.formatMessage({defaultMessage: 'Test connection'})}
                    </TertiaryButton>
                </FieldControlRow>
                {state.status === 'success' && (
                    <SuccessText>
                        <i className='icon icon-check'/>
                        {intl.formatMessage({defaultMessage: 'Connection successful'})}
                    </SuccessText>
                )}
                {state.status === 'failure' && (
                    <ResultText>{state.error}</ResultText>
                )}
                {state.status === 'idle' && (
                    <HelpText>
                        {intl.formatMessage({defaultMessage: 'Sends a short message to the provider to check these settings. Unsaved changes above are included in the test.'})}
                    </HelpText>
                )}
            </TextFieldContainer>
        </FormRow>
    );
};
