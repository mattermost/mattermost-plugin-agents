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
    const [armed, setArmed] = useState(false);

    // Buttons that received a pointerdown after the prompt mounted.
    const pressedRef = useRef(new Set<string>());

    // Focus the safe choice so a stray Enter never grants access.
    useEffect(() => {
        denyRef.current?.focus({preventScroll: true});
        const timer = window.setTimeout(() => setArmed(true), CONSENT_ARM_DELAY_MS);
        return () => window.clearTimeout(timer);
    }, []);

    const guarded = useCallback((key: string, action: () => void) => (e: React.MouseEvent) => {
        if (!armed) {
            return;
        }

        // detail > 0 means a pointer click; it only counts when the press
        // started on this button after the prompt appeared.
        if (e.detail > 0 && !pressedRef.current.has(key)) {
            return;
        }
        action();
    }, [armed]);

    const onPointerDown = (key: string) => () => {
        pressedRef.current.add(key);
    };

    return (
        <Backdrop>
            <Dialog
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
                        defaultMessage='<b>{fileName}</b> wants to read your profile: name, username and language.'
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
                        onPointerDown={onPointerDown('allow')}
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
    align-items: center;
    justify-content: center;
    padding: 16px;
    background: rgba(var(--center-channel-color-rgb), 0.32);
`;

const Dialog = styled.div`
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
