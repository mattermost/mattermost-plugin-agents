// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import MagnifyIcon from '@mattermost/compass-icons/components/magnify';
import {Icon} from '@mattermost/compass-ui/components/icon';

import FindAgentsModal from './find_agents_modal';

// Styled like the host's "Find channels" sidebar button. It has no keyboard
// shortcut hint because the host owns Ctrl/Cmd+K and this does not register one.
const FindAgentsButton = () => {
    const intl = useIntl();
    const buttonRef = useRef<HTMLButtonElement>(null);
    const [open, setOpen] = useState(false);

    const close = useCallback(() => {
        setOpen(false);
        buttonRef.current?.focus();
    }, []);

    const label = intl.formatMessage({defaultMessage: 'Find agents'});

    return (
        <>
            <FindButton
                ref={buttonRef}
                type='button'
                aria-label={label}
                aria-haspopup='dialog'
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
