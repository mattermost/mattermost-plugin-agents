// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {ShieldLockOutlineIcon} from '@mattermost/compass-icons/components';

// The artifact decides when this prompt appears, so it could time it under
// the viewer's cursor while they click quickly. Answers are ignored until the
// prompt has been on screen this long.
export const CONSENT_ARM_DELAY_MS = 600;

interface Props {
    fileName: string;
    onAllow: () => void;
    onDeny: () => void;
}

const ArtifactConsentPrompt = ({fileName, onAllow, onDeny}: Props) => {
    const denyRef = useRef<HTMLButtonElement>(null);
    const dialogRef = useRef<HTMLDivElement>(null);
    const [armed, setArmed] = useState(false);

    // Buttons that received a pointerdown after the prompt armed, while the
    // pointer was already on them.
    const pressedRef = useRef(new Set<string>());

    // Buttons the pointer entered after the prompt armed. A click at a spot
    // the cursor was already resting on (or that the artifact lured the viewer
    // to click repeatedly) never counts.
    const enteredRef = useRef(new Set<string>());

    // Allow only: whether it received focus after the prompt armed. Together
    // with allowKeyDownRef this keeps an artifact from training a Tab then
    // Space/Enter habit and timing the prompt so that it lands on Allow. A
    // lured Deny is harmless, so it only waits for arming.
    const allowFocusedRef = useRef(false);

    // Allow only: whether an Enter/Space key press started on it after the
    // prompt armed. Enter activates on keydown and Space on keyup, so the
    // keydown is what shows the press began after arming.
    const allowKeyDownRef = useRef(false);

    // Focus the safe choice so a stray Enter never grants access.
    // Focus returns to where it was once the prompt goes away, unless it
    // has already moved elsewhere (e.g. to the fullscreen viewer).
    useEffect(() => {
        const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        const dialog = dialogRef.current;
        denyRef.current?.focus({preventScroll: true});
        const timer = window.setTimeout(() => setArmed(true), CONSENT_ARM_DELAY_MS);
        return () => {
            window.clearTimeout(timer);
            const active = document.activeElement;
            const focusInPrompt = !active || active === document.body || Boolean(dialog?.contains(active));
            if (focusInPrompt && previous?.isConnected) {
                previous.focus({preventScroll: true});
            }
        };
    }, []);

    const guarded = useCallback((key: string, action: () => void) => (e: React.MouseEvent) => {
        if (!armed) {
            return;
        }

        // detail > 0 means a pointer click; it only counts when the pointer
        // entered this button after the prompt armed and the press started on
        // it. Keyboard activation (detail 0) of Allow needs focus to have
        // arrived and the key press to have started, both after arming.
        let counts = key === 'deny' || (allowFocusedRef.current && allowKeyDownRef.current);
        if (e.detail > 0) {
            counts = pressedRef.current.has(key) && enteredRef.current.has(key);
        }
        if (key === 'allow') {
            allowKeyDownRef.current = false;
        }
        if (!counts) {
            return;
        }
        action();
    }, [armed]);

    const onPointerEnter = (key: string) => () => {
        if (armed) {
            enteredRef.current.add(key);
        }
    };

    const onPointerDown = (key: string) => () => {
        if (armed && enteredRef.current.has(key)) {
            pressedRef.current.add(key);
        }
    };

    const onAllowFocus = () => {
        if (armed) {
            allowFocusedRef.current = true;
        }
    };

    const onAllowBlur = () => {
        allowFocusedRef.current = false;
        allowKeyDownRef.current = false;
    };

    // A held key repeats keydown, so repeats say nothing about when it began.
    const onAllowKeyDown = (e: React.KeyboardEvent) => {
        if (armed && !e.repeat && (e.key === 'Enter' || e.key === ' ')) {
            allowKeyDownRef.current = true;
        }
    };

    return (
        <Backdrop>
            <Dialog
                ref={dialogRef}
                role='alertdialog'
                aria-modal='true'
                aria-labelledby='html-artifact-consent-heading'
                aria-describedby='html-artifact-consent-text'
                data-testid='html-artifact-consent'
            >
                <Heading id='html-artifact-consent-heading'>
                    <Icon
                        size='20'
                        glyph={<ShieldLockOutlineIcon/>}
                    />
                    <FormattedMessage defaultMessage='Allow access to your profile?'/>
                </Heading>
                <Text id='html-artifact-consent-text'>
                    <FormattedMessage
                        defaultMessage='<b>{fileName}</b> wants to read your name (first name, last name, nickname and display name) and username.'
                        values={{
                            fileName,
                            b: (chunks: React.ReactNode) => <strong>{chunks}</strong>,
                        }}
                    />
                </Text>
                <Actions
                    $armed={armed}
                    data-armed={armed}
                >
                    <Button
                        ref={denyRef}
                        emphasis='tertiary'
                        size='small'
                        aria-disabled={!armed}
                        onPointerEnter={onPointerEnter('deny')}
                        onPointerDown={onPointerDown('deny')}
                        onClick={guarded('deny', onDeny)}
                        data-testid='html-artifact-consent-deny'
                    >
                        <FormattedMessage defaultMessage='Deny'/>
                    </Button>
                    <Button
                        emphasis='primary'
                        size='small'
                        aria-disabled={!armed}
                        onPointerEnter={onPointerEnter('allow')}
                        onPointerDown={onPointerDown('allow')}
                        onFocus={onAllowFocus}
                        onBlur={onAllowBlur}
                        onKeyDown={onAllowKeyDown}
                        onClick={guarded('allow', onAllow)}
                        data-testid='html-artifact-consent-allow'
                    >
                        <FormattedMessage defaultMessage='Allow'/>
                    </Button>
                </Actions>
            </Dialog>
        </Backdrop>
    );
};

export default ArtifactConsentPrompt;

const Backdrop = styled.div`
    position: absolute;
    inset: 0;
    display: flex;
    padding: 16px;
    overflow: auto;
    background: rgba(var(--center-channel-color-rgb), 0.32);
`;

// margin: auto centers the dialog but keeps it scrollable when it is taller
// than the frame (a very long file name).
const Dialog = styled.div`
    margin: auto;
    max-width: 400px;
    padding: 16px 20px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 8px;
    background: var(--center-channel-bg);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
    color: var(--center-channel-color);
`;

const Heading = styled.h3`
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 0 6px;
    color: var(--center-channel-color);
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;

    > :first-child {
        flex: none;
        color: var(--button-bg);
    }
`;

const Text = styled.p`
    margin: 0 0 16px;
    color: rgba(var(--center-channel-color-rgb), 0.8);
    font-size: 14px;
    line-height: 20px;
    overflow-wrap: anywhere;
`;

const Actions = styled.div<{$armed: boolean}>`
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    opacity: ${(props) => (props.$armed ? 1 : 0.48)};
    transition: opacity 0.15s ease-out;

    button {
        cursor: ${(props) => (props.$armed ? 'pointer' : 'not-allowed')};
    }
`;
