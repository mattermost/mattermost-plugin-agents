// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {CSSTransition} from 'react-transition-group';
import styled, {css} from 'styled-components';

/** Apply to the centered panel (card) inside the overlay so enter/exit slide + fade run on the sheet. */
export const MODAL_SHEET_CLASS = 'mmAiModal__sheet';

/**
 * Match the longest transition for CSSTransition `timeout`.
 * Mattermost GenericModal uses Bootstrap: .modal-dialog transform 0.3s ease-out; .fade opacity 0.15s linear.
 */
export const MODAL_TRANSITION_MS = 300;

/**
 * Enter/exit phases for `classNames="mm-ai-modal"` (react-transition-group).
 * Timings/motion aligned with host webapp: react-bootstrap Fade (0.15s linear) + .modal-dialog slide (0.3s ease-out, -25%).
 */
export const modalTransitionPhases = css`
    &.mm-ai-modal-enter,
    &.mm-ai-modal-appear {
        opacity: 0;
    }

    &.mm-ai-modal-enter-active,
    &.mm-ai-modal-appear-active {
        opacity: 1;
        transition: opacity 0.15s linear;
    }

    &.mm-ai-modal-exit {
        opacity: 1;
    }

    &.mm-ai-modal-exit-active {
        opacity: 0;
        transition: opacity 0.15s linear;
    }

    &.mm-ai-modal-enter .${MODAL_SHEET_CLASS},
    &.mm-ai-modal-appear .${MODAL_SHEET_CLASS} {
        opacity: 0;
        transform: translateY(-25%);
    }

    &.mm-ai-modal-enter-active .${MODAL_SHEET_CLASS},
    &.mm-ai-modal-appear-active .${MODAL_SHEET_CLASS} {
        opacity: 1;
        transform: translateY(0);
        transition: opacity 0.15s linear, transform 0.3s ease-out;
    }

    &.mm-ai-modal-exit .${MODAL_SHEET_CLASS} {
        opacity: 1;
        transform: translateY(0);
    }

    &.mm-ai-modal-exit-active .${MODAL_SHEET_CLASS} {
        opacity: 0;
        transform: translateY(-25%);
        transition: opacity 0.15s linear, transform 0.3s ease-out;
    }
`;

const ShellRoot = styled.div<{$zIndex: number}>`
    ${modalTransitionPhases}
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(0, 0, 0, 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: ${(p) => p.$zIndex};
`;

type ShellProps = {
    show: boolean;
    children: React.ReactNode;
    onBackdropClick?: () => void;
    zIndex?: number;
};

/**
 * Full-screen dimmed overlay with enter/exit animation (fade + sheet from top),
 * aligned with Mattermost GenericModal (Bootstrap fade + modal-dialog motion).
 */
export const AnimatedModalShell = ({show, children, onBackdropClick, zIndex = 2000}: ShellProps) => {
    const nodeRef = useRef<HTMLDivElement>(null);
    return (
        <CSSTransition
            nodeRef={nodeRef}
            in={show}
            timeout={MODAL_TRANSITION_MS}
            classNames='mm-ai-modal'
            unmountOnExit={true}
            mountOnEnter={true}
            appear={true}
        >
            <ShellRoot
                ref={nodeRef}
                $zIndex={zIndex}
                onClick={(e) => {
                    if (e.target === e.currentTarget) {
                        onBackdropClick?.();
                    }
                }}
            >
                {children}
            </ShellRoot>
        </CSSTransition>
    );
};
