// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {PANE_DEFAULT_WIDTH, PANE_KEYBOARD_STEP, PANE_MIN_WIDTH, clampPaneWidth} from './pane_geometry';

type Props = {

    // The pane's current width and the width of the center column it shares.
    width: number;
    available: number;

    // Called while dragging, with the clamped width to show, and once more when the drag ends.
    onResize: (width: number) => void;
    onResizeEnd: (width: number) => void;
    onDragChange: (dragging: boolean) => void;
};

// The handle on the pane's left edge. Dragging left widens the pane; the arrow keys move it in
// steps, and a double-click goes back to the default width.
const ResizeHandle = ({width, available, onResize, onResizeEnd, onDragChange}: Props) => {
    const intl = useIntl();
    const drag = useRef<{startX: number; startWidth: number; width: number} | null>(null);

    const finish = (el: HTMLDivElement, pointerId: number) => {
        const current = drag.current;
        drag.current = null;
        if (el.hasPointerCapture?.(pointerId)) {
            el.releasePointerCapture(pointerId);
        }
        onDragChange(false);
        if (current) {
            onResizeEnd(current.width);
        }
    };

    const max = available > 0 ? clampPaneWidth(available, Number.MAX_SAFE_INTEGER) : Math.max(width, PANE_MIN_WIDTH);

    /* eslint-disable agents/no-noninteractive-element-interactions, agents/no-noninteractive-tabindex -- a focusable separator is the ARIA window splitter, which takes pointer and arrow-key input */
    return (
        <Handle
            role='separator'
            aria-orientation='vertical'
            aria-label={intl.formatMessage({defaultMessage: 'Resize the project pane'})}
            aria-valuenow={width}
            aria-valuemin={PANE_MIN_WIDTH}
            aria-valuemax={max}
            tabIndex={0}
            data-testid='project-pane-resize-handle'
            onPointerDown={(e) => {
                if (e.button !== 0) {
                    return;
                }
                e.preventDefault();
                e.currentTarget.setPointerCapture?.(e.pointerId);
                drag.current = {startX: e.clientX, startWidth: width, width};
                onDragChange(true);
            }}
            onPointerMove={(e) => {
                const current = drag.current;
                if (!current) {
                    return;
                }
                current.width = clampPaneWidth(available, current.startWidth + (current.startX - e.clientX));
                onResize(current.width);
            }}
            onPointerUp={(e) => finish(e.currentTarget, e.pointerId)}
            onPointerCancel={(e) => finish(e.currentTarget, e.pointerId)}
            onDoubleClick={() => onResizeEnd(clampPaneWidth(available, PANE_DEFAULT_WIDTH))}
            onKeyDown={(e) => {
                let next: number | null = null;
                if (e.key === 'ArrowLeft') {
                    next = width + PANE_KEYBOARD_STEP;
                } else if (e.key === 'ArrowRight') {
                    next = width - PANE_KEYBOARD_STEP;
                } else if (e.key === 'Home') {
                    next = PANE_MIN_WIDTH;
                } else if (e.key === 'End' && available > 0) {
                    next = max;
                }
                if (next !== null) {
                    e.preventDefault();
                    e.stopPropagation();
                    onResizeEnd(clampPaneWidth(available, next));
                }
            }}
        />
    );
    /* eslint-enable agents/no-noninteractive-element-interactions, agents/no-noninteractive-tabindex */
};

export default ResizeHandle;

const Handle = styled.div`
    position: absolute;
    z-index: 1;
    top: 0;
    bottom: 0;
    left: -4px;
    width: 8px;
    cursor: col-resize;
    touch-action: none;

    &::after {
        position: absolute;
        top: 0;
        bottom: 0;
        left: 3px;
        width: 2px;
        background: transparent;
        content: '';
        transition: background var(--duration-fast, 100ms) ease-in-out;
    }

    &:hover::after,
    &:focus-visible::after {
        background: var(--button-bg);
    }

    &:focus-visible {
        outline: none;
    }
`;
