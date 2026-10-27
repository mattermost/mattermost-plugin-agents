// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import ArrowDownIcon from '@mattermost/compass-icons/components/arrow-down';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {WaveDots} from '@/components/llmbot_post/thinking_dots';
import {noMotionWhenReduced, prefersReducedMotion} from '@/components/llmbot_post/motion';

interface JumpToLatestProps {

    // Shown whenever the chat is scrolled up from its newest message.
    visible: boolean;

    // The agent is answering: the button shows the wave, and the arrow only on hover or focus.
    // Otherwise it is a plain down-arrow button.
    responding: boolean;
    agentName: string;
    onClick: () => void;
}

// Floats above the composer whenever the user is scrolled up. One click scrolls to the newest
// message.
const JumpToLatest = ({visible, responding, agentName, onClick}: JumpToLatestProps) => {
    const intl = useIntl();

    // Not shown, but announced to screen readers.
    const label = agentName ? intl.formatMessage({defaultMessage: '{name} is responding'}, {name: agentName}) : intl.formatMessage({defaultMessage: 'Responding'});
    const ariaLabel = responding ? intl.formatMessage({defaultMessage: '{label}. Scroll to the latest message'}, {label}) : intl.formatMessage({defaultMessage: 'Scroll to the latest message'});

    return (
        <Floating
            data-testid='jump-to-latest'
            data-visible={visible}
        >
            <Pill>
                <RoundButton
                    size='small'
                    padding='compact'
                    rounded={true}
                    icon={(
                        <Faces data-responding={responding}>
                            <Wave>
                                <WaveDots
                                    animate={!prefersReducedMotion()}
                                    half={true}
                                />
                            </Wave>
                            <Arrow>
                                <Icon glyph={<ArrowDownIcon/>}/>
                            </Arrow>
                        </Faces>
                    )}
                    tabIndex={visible ? 0 : -1}
                    aria-label={ariaLabel}
                    data-responding={responding}
                    onClick={onClick}
                />
            </Pill>
        </Floating>
    );
};

export default JumpToLatest;

// Sits at the bottom of the message area, so just above the composer; only the pill takes clicks.
// Hidden, it also leaves the tab order (visibility) once its fade has finished.
const Floating = styled.div`
    position: absolute;
    right: 0;
    bottom: var(--spacing-m);
    left: 0;
    z-index: 2;
    display: flex;
    justify-content: center;
    pointer-events: none;
    opacity: 0;
    visibility: hidden;
    transform: translateY(var(--spacing-xs));
    transition:
        opacity var(--duration-moderate) var(--ease-exit),
        transform var(--duration-moderate) var(--ease-exit),
        visibility 0s linear var(--duration-moderate);

    &[data-visible='true'] {
        opacity: 1;
        visibility: visible;
        transform: translateY(0);
        transition:
            opacity var(--duration-moderate) var(--ease-entrance),
            transform var(--duration-moderate) var(--ease-entrance),
            visibility 0s;

        & > * {
            pointer-events: auto;
        }
    }

    ${noMotionWhenReduced}
`;

// A solid surface over the messages (the icon button itself is transparent), in the host's neutral
// colors, with the standard border and a low elevation.
const Pill = styled.span`
    display: inline-flex;
    border: var(--border-default);
    border-radius: var(--radius-pill);
    background: var(--center-channel-bg);
    box-shadow: var(--elevation-2);
`;

// The wave (three dots and their rise) is wider than the button's 16px icon area: it must not be
// clipped by the button or the icon slot, nor squeezed by them.
const RoundButton = styled(IconButton)`
    && {
        overflow: visible;
    }

    && > * {
        width: auto;
        overflow: visible;
    }
`;

// The wave and the arrow share one cell: the wave while the agent answers, the arrow once it has
// finished or while the button is hovered or focused.
const Faces = styled.span`
    display: inline-grid;
    flex-shrink: 0;
    min-width: max-content;
    overflow: visible;
    align-items: center;
    justify-items: center;

    & > * {
        grid-area: 1 / 1;
        transition: opacity var(--duration-quick) var(--ease-transition);
    }

    ${noMotionWhenReduced}
`;

const Wave = styled.span`
    display: inline-flex;
    opacity: 0;

    [data-responding='true'] > & {
        opacity: 1;
    }

    button:hover [data-responding='true'] > &,
    button:focus-visible [data-responding='true'] > & {
        opacity: 0;
    }
`;

const Arrow = styled.span`
    display: inline-flex;
    opacity: 1;

    [data-responding='true'] > & {
        opacity: 0;
    }

    button:hover [data-responding='true'] > &,
    button:focus-visible [data-responding='true'] > & {
        opacity: 1;
    }
`;
