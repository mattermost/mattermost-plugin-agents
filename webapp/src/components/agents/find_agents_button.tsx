// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import MagnifyIcon from '@mattermost/compass-icons/components/magnify';
import {Icon} from '@mattermost/compass-ui/components/icon';

import FindAgentsModal from './find_agents_modal';

const isMacPlatform = () => (/Mac|iPhone|iPad|iPod/i).test(navigator.platform || navigator.userAgent || '');

// Cmd+K on Mac, Ctrl+K on Windows and Linux (the same split the host uses for its channel
// switcher). Shift/Alt variants and the other platform's modifier are left alone.
export const isFindAgentsShortcut = (e: KeyboardEvent, mac: boolean) => {
    const modifier = mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
    const isK = e.key?.toLowerCase() === 'k' || e.code === 'KeyK';
    return modifier && !e.shiftKey && !e.altKey && isK;
};

// Styled like the host's "Find channels" sidebar button. The host's own Cmd/Ctrl+K belongs to the
// Channels sidebar's channel navigator, which is not mounted in the Agents product, so this
// registers the shortcut while the Agents sidebar (and so this button) is on screen.
const FindAgentsButton = () => {
    const intl = useIntl();
    const buttonRef = useRef<HTMLButtonElement>(null);
    const [open, setOpen] = useState(false);
    const [mac] = useState(isMacPlatform);

    const close = useCallback(() => {
        setOpen(false);
        buttonRef.current?.focus();
    }, []);

    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            if (!isFindAgentsShortcut(e, mac)) {
                return;
            }
            e.preventDefault();
            setOpen((wasOpen) => !wasOpen);
        };
        document.addEventListener('keydown', onKeyDown);
        return () => document.removeEventListener('keydown', onKeyDown);
    }, [mac]);

    const label = intl.formatMessage({defaultMessage: 'Find agents'});
    const shortcutHint = mac ? '\u2318K' : 'Ctrl+K';

    return (
        <>
            <FindButton
                ref={buttonRef}
                type='button'
                aria-label={label}
                aria-haspopup='dialog'
                aria-keyshortcuts={mac ? 'Meta+K' : 'Control+K'}
                data-testid='find-agents-button'
                onClick={() => setOpen(true)}
            >
                <Label>
                    <Icon
                        size='16'
                        glyph={<MagnifyIcon/>}
                    />
                    <span>{label}</span>
                </Label>
                <ShortcutHint aria-hidden='true'>{shortcutHint}</ShortcutHint>
            </FindButton>
            <FindAgentsModal
                show={open}
                onClose={close}
            />
        </>
    );
};

// No compass equivalent for the sidebar's full-width find row, so this is a bare
// button (declared next to its handler for the a11y lint rules).
const FindButton = styled.button`
    position: relative;
    display: flex;
    align-items: center;
    width: 100%;
    height: calc(var(--spacing-xl) + var(--spacing-xs));
    margin: 0;
    padding: 0 var(--spacing-xxxs) 0 var(--spacing-xxs);
    border: 0;
    border-radius: var(--radius-s);
    background: transparent;
    color: var(--sidebar-text);
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    text-align: left;
    cursor: pointer;

    &::before {
        content: '';
        position: absolute;
        inset: 0;
        border-radius: inherit;
        background: currentColor;
        opacity: 0.08;
        pointer-events: none;
    }

    &:hover::before {
        opacity: 0.16;
    }

    &:focus-visible {
        outline: var(--spacing-xxxxs) solid var(--sidebar-text);
        outline-offset: calc(-1 * var(--spacing-xxxxs));
    }
`;

const ShortcutHint = styled.span`
    position: relative;
    flex-shrink: 0;
    margin-left: auto;
    padding: 0 var(--spacing-xs);
    opacity: 0.64;
    font-size: var(--font-size-50);
    line-height: var(--line-height-50);
    white-space: nowrap;

    ${FindButton}:hover & {
        opacity: 1;
    }
`;

const Label = styled.span`
    position: relative;
    display: flex;
    align-items: center;
    gap: var(--spacing-xxxs);
    min-width: 0;
    opacity: 0.64;

    span {
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    ${FindButton}:hover & {
        opacity: 1;
    }
`;

export default FindAgentsButton;
