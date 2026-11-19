// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {MicrophoneIcon, MicrophoneOutlineIcon} from '@mattermost/compass-icons/components';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
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

const ButtonAnchor = styled.span`
    position: relative;
    display: inline-flex;
`;

// While listening, a glow around the button grows and brightens with the
// microphone level so the user sees they are heard. --dictation-level (0 to
// 1) is set on the anchor once per frame. With reduced motion the glow keeps
// its size and only its brightness follows the level.
const DictationIconButton = styled(IconButton)`
    &[data-status='listening'] {
        box-shadow: 0 0 0 calc(2px + var(--dictation-level, 0) * 8px) rgba(var(--error-text-color-rgb), calc(0.3 + var(--dictation-level, 0) * 0.45));
        transition: box-shadow 80ms linear;

        @media (prefers-reduced-motion: reduce) {
            box-shadow: 0 0 0 3px rgba(var(--error-text-color-rgb), calc(0.2 + var(--dictation-level, 0) * 0.6));
            transition: none;
        }
    }
`;

// Speech reaches a microphone at roughly -45 to -15 dBFS RMS depending on the
// device, distance and gain control, so the glow follows the level in
// decibels: SILENCE_DB and below shows nothing, FULL_DB and above shows the
// full glow.
const SILENCE_DB = -50;
const FULL_DB = -20;

export function displayLevel(rms: number): number {
    if (rms <= 0) {
        return 0;
    }
    const db = 20 * Math.log10(rms);
    return Math.min(1, Math.max(0, (db - SILENCE_DB) / (FULL_DB - SILENCE_DB)));
}

// useLiveLevel copies the microphone level to --dictation-level on target,
// throttled to animation frames, while active.
function useLiveLevel(target: React.RefObject<HTMLElement | null>, levelRef: React.MutableRefObject<number>, active: boolean) {
    useEffect(() => {
        const element = target.current;
        if (!active || !element) {
            return undefined; // eslint-disable-line no-undefined
        }
        let frame = 0;
        let shown = -1;
        const draw = () => {
            const level = displayLevel(levelRef.current);
            if (Math.abs(level - shown) >= 0.02) {
                element.style.setProperty('--dictation-level', level.toFixed(2));
                shown = level;
            }
            frame = requestAnimationFrame(draw);
        };
        frame = requestAnimationFrame(draw);
        return () => {
            cancelAnimationFrame(frame);
            element.style.removeProperty('--dictation-level');
        };
    }, [target, levelRef, active]);
}

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
    const {status, errorReason, toggle, active, levelRef} = useDictation({draft, getSelectedText, updateText, createTransport, maxSessionMs});
    const anchorRef = useRef<HTMLSpanElement>(null);
    useLiveLevel(anchorRef, levelRef, status === 'listening');
    const gettingReady = status === 'requesting_permission' || status === 'connecting';

    const errorMessage = useCallback((reason: DictationErrorReason | null) => {
        switch (reason) {
        case 'mic_blocked':
            return intl.formatMessage({defaultMessage: 'Microphone access is blocked. Allow it in your browser settings and try again.'});
        case 'no_mic':
            return intl.formatMessage({defaultMessage: 'No microphone was found.'});
        case 'rate_limited':
            return intl.formatMessage({defaultMessage: 'Dictation can\'t start right now: it was started too often, or you\'re already dictating in another window. Stop dictating there, or wait a minute, and try again.'});
        case 'offer_too_large':
            return intl.formatMessage({defaultMessage: 'Your browser\'s connection request was too large. Reload the page and try again.'});
        case 'session_time_limit':
            return intl.formatMessage({defaultMessage: 'Dictation stopped at its time limit. Start it again to keep going.'});
        case 'session_policy':
            return intl.formatMessage({defaultMessage: 'Dictation stopped because its session settings were changed. Start it again.'});
        case 'session_shutdown':
            return intl.formatMessage({defaultMessage: 'Dictation stopped because the Agents plugin restarted. Start it again.'});
        case 'forbidden':
            return intl.formatMessage({defaultMessage: 'You don\'t have access to dictation.'});
        case 'audio_blocked':
            return intl.formatMessage({defaultMessage: 'Your browser didn\'t start audio processing. Reload the page and try again.'});
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
        tooltip = intl.formatMessage({defaultMessage: 'Getting ready. Allow microphone access if your browser asks.'});
    } else if (status === 'connecting') {
        tooltip = intl.formatMessage({defaultMessage: 'Getting ready. Wait before speaking.'});
    } else if (status === 'listening') {
        tooltip = intl.formatMessage({defaultMessage: 'Listening. Speak now. Select to stop, or press Esc.'});
    } else if (status === 'stopping') {
        tooltip = intl.formatMessage({defaultMessage: 'Finishing transcription'});
    } else if (status === 'error') {
        tooltip = errorMessage(errorReason);
    }

    return (
        <WithTooltip title={tooltip}>
            <ButtonAnchor
                ref={anchorRef}
                data-testid='dictation-indicator'
            >
                <DictationIconButton
                    size='small'
                    icon={gettingReady ? (
                        <Spinner
                            size='16'
                            inverted={true}
                            aria-hidden={true}
                            data-testid='dictation-getting-ready'
                        />
                    ) : (
                        <Icon glyph={active ? <MicrophoneIcon/> : <MicrophoneOutlineIcon/>}/>
                    )}
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
                    {active || status === 'error' ? tooltip : ''}
                </VisuallyHidden>
            </ButtonAnchor>
        </WithTooltip>
    );
};

export default DictationButton;
