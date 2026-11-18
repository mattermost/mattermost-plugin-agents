// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import {useIntl} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import {MicrophoneIcon, MicrophoneOutlineIcon} from '@mattermost/compass-icons/components';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {VisuallyHidden} from '@/components/visually_hidden';
import {useIsLicensedFor} from '@/license';
import {useDictationAvailable} from '@/dictation/availability';
import {DictationErrorReason, DictationTransport} from '@/dictation/types';
import {WebRTCTranscriber, isDictationSupported} from '@/dictation/webrtc_transcriber';

import {EditorDraft, SelectedText, useDictation} from './use_dictation';

// Props Mattermost passes to a post editor action component.
export type DictationButtonProps = {
    draft: EditorDraft;
    getSelectedText?: () => SelectedText;
    updateText: (message: string) => void;

    // Tests replace the WebRTC transport and shorten the session cap.
    createTransport?: () => DictationTransport;
    maxSessionMs?: number;
};

const createWebRTCTranscriber = () => new WebRTCTranscriber();

const pulse = keyframes`
    0% { transform: scale(1); opacity: 1; }
    50% { transform: scale(1.6); opacity: 0.4; }
    100% { transform: scale(1); opacity: 1; }
`;

const ButtonAnchor = styled.span<{$listening: boolean}>`
    position: relative;
    display: inline-flex;

    ${({$listening}) => $listening && css`
        &::after {
            content: '';
            position: absolute;
            top: var(--spacing-xxxs);
            right: var(--spacing-xxxs);
            width: 6px;
            height: 6px;
            border-radius: 50%;
            background: var(--error-text);
            pointer-events: none;
            animation: ${pulse} 1.2s ease-in-out infinite;

            @media (prefers-reduced-motion: reduce) {
                animation: none;
            }
        }
    `}
`;

const DictationButton = (props: DictationButtonProps) => {
    const available = useDictationAvailable();
    const licensed = useIsLicensedFor('dictation');
    if (!available || !licensed) {
        return null;
    }
    return <DictationControl {...props}/>;
};

const DictationControl = ({draft, getSelectedText, updateText, createTransport = createWebRTCTranscriber, maxSessionMs}: DictationButtonProps) => {
    const intl = useIntl();
    const supported = isDictationSupported();
    const {status, errorReason, toggle, active} = useDictation({draft, getSelectedText, updateText, createTransport, maxSessionMs});

    const errorMessage = useCallback((reason: DictationErrorReason | null) => {
        switch (reason) {
        case 'mic_blocked':
            return intl.formatMessage({defaultMessage: 'Microphone access is blocked. Allow it in your browser settings and try again.'});
        case 'no_mic':
            return intl.formatMessage({defaultMessage: 'No microphone was found.'});
        case 'rate_limited':
            return intl.formatMessage({defaultMessage: 'Dictation was started too often. Wait a minute and try again.'});
        case 'forbidden':
            return intl.formatMessage({defaultMessage: 'You don\'t have access to dictation.'});
        case 'connection_lost':
            return intl.formatMessage({defaultMessage: 'The connection to the speech service was lost.'});
        case 'provider_error':
            return intl.formatMessage({defaultMessage: 'The speech service reported an error.'});
        case 'unavailable':
            return intl.formatMessage({defaultMessage: 'Dictation is not available right now. Ask your System Admin.'});
        default:
            return intl.formatMessage({defaultMessage: 'The speech service could not be reached.'});
        }
    }, [intl]);

    const label = active ? intl.formatMessage({defaultMessage: 'Stop dictation'}) : intl.formatMessage({defaultMessage: 'Start dictation'});

    let tooltip = label;
    if (!supported) {
        tooltip = intl.formatMessage({defaultMessage: 'Dictation isn\'t supported in this browser'});
    } else if (status === 'requesting_permission') {
        tooltip = intl.formatMessage({defaultMessage: 'Waiting for microphone permission'});
    } else if (status === 'connecting') {
        tooltip = intl.formatMessage({defaultMessage: 'Connecting'});
    } else if (status === 'listening') {
        tooltip = intl.formatMessage({defaultMessage: 'Listening. Select to stop, or press Esc.'});
    } else if (status === 'stopping') {
        tooltip = intl.formatMessage({defaultMessage: 'Finishing transcription'});
    } else if (status === 'error') {
        tooltip = errorMessage(errorReason);
    }

    return (
        <WithTooltip title={tooltip}>
            <ButtonAnchor $listening={status === 'listening'}>
                <IconButton
                    size='small'
                    icon={<Icon glyph={active ? <MicrophoneIcon/> : <MicrophoneOutlineIcon/>}/>}
                    toggled={active}
                    destructive={status === 'error'}
                    disabled={!supported || status === 'stopping'}
                    aria-label={label}
                    onClick={toggle}
                    data-testid='dictation-button'
                    data-status={status}
                />
                <VisuallyHidden
                    role='status'
                    aria-live='polite'
                >
                    {status === 'listening' || status === 'error' ? tooltip : ''}
                </VisuallyHidden>
            </ButtonAnchor>
        </WithTooltip>
    );
};

export default DictationButton;
