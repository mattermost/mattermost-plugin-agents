// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {ComponentProps, cloneElement, useState} from 'react';
import styled from 'styled-components';

import {
    FloatingFocusManager,
    FloatingPortal,
    Placement,
    autoUpdate,
    flip,
    offset,
    shift,
    useDismiss,
    useFloating,
    useInteractions,
    useRole,
} from '@floating-ui/react-dom-interactions';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

/** Marks a rendered popup menu so a modal can let Escape close the menu first. */
export const OPEN_POPUP_ATTRIBUTE = 'data-open-popup';

// The compass PopoverMenu grows from its top left corner; this points that at
// the corner or edge touching the trigger instead. && outranks compass's rule.
const FloatingContainer = styled.div`
    min-width: 16rem;
    z-index: 2100;

    && > * {
        transform-origin: var(--popover-transform-origin, top left);
    }
`;

/**
 * Where a popup placed at `placement` should scale from: the side facing its
 * trigger, aligned like the popup. A menu below a left-aligned button opens
 * from "top left"; `bottom-end` opens from "top right".
 */
export function popoverTransformOrigin(placement: Placement): string {
    const [side, alignment = 'center'] = placement.split('-');
    const horizontal = {start: 'left', end: 'right', center: 'center'}[alignment as 'start' | 'end' | 'center'];
    const vertical = {start: 'top', end: 'bottom', center: 'center'}[alignment as 'start' | 'end' | 'center'];
    switch (side) {
    case 'top':
        return `bottom ${horizontal}`;
    case 'left':
        return `${vertical} right`;
    case 'right':
        return `${vertical} left`;
    default:
        return `top ${horizontal}`;
    }
}

// The host tooltip layer (--z-index-tooltip, 1350) sits below this plugin's
// modals (2000) and their menus (2100), so a trigger inside a modal would show
// its tooltip behind the modal. && outranks the host's .tooltipContainer rule.
export const TooltipAboveModals = styled(WithTooltip)`
    && {
        z-index: 2200;
    }
`;

const TooltipTrigger = styled.span`
    display: inline-flex;
`;

type DropdownProps = {
    target: React.JSX.Element;
    children: React.ReactNode;
    placement?: Placement;
    offset?: Parameters<typeof offset>[0];
    flip?: Parameters<typeof flip>[0];
    shift?: Parameters<typeof shift>[0];
    focusManager?: boolean | Omit<ComponentProps<typeof FloatingFocusManager>, 'context' | 'children'>;
    portal?: boolean;
    floatingClassName?: string;
    isOpen: boolean;
    onOpenChange?: ((open: boolean) => void);

    /** Host-style tooltip for the trigger; suppressed while the menu is open. */
    tooltip?: string;
};

const Dropdown = (props: DropdownProps) => {
    const [isOpen, setIsOpen] = useState(props.isOpen);

    const open = props.isOpen ?? isOpen;

    const setOpen = (updatedOpen: boolean) => {
        props.onOpenChange?.(updatedOpen);
        setIsOpen(updatedOpen);
    };

    const {strategy, x, y, reference, floating, context, placement} = useFloating<HTMLElement>({
        open,
        onOpenChange: setOpen,
        placement: props.placement ?? 'bottom-start',
        middleware: [offset(props.offset ?? 2), flip(props.flip), shift(props.shift ?? {padding: 2})],
        whileElementsMounted: autoUpdate,
        strategy: 'fixed',
    });

    const {getReferenceProps, getFloatingProps} = useInteractions([
        useRole(context),
        useDismiss(context),
    ]);

    const MaybePortal = (props.portal ?? true) ? FloatingPortal : React.Fragment; // 🤷

    let content = (
        <FloatingContainer
            className={props.floatingClassName}
            {...{[OPEN_POPUP_ATTRIBUTE]: ''}}
            {...getFloatingProps({
                ref: floating,
                style: {
                    position: strategy,
                    top: y ?? 0,
                    left: x ?? 0,

                    // Follows the placement floating-ui settled on, flips included.
                    ['--popover-transform-origin' as string]: popoverTransformOrigin(placement),
                },
            })}
        >
            {props.children}
        </FloatingContainer>
    );

    if (props.focusManager ?? true) {
        content = (
            <FloatingFocusManager
                {...typeof props.focusManager === 'boolean' ? false : props.focusManager}
                context={context}
            >
                {content}
            </FloatingFocusManager>
        );
    }

    // Attach floating-ui reference props first. When a tooltip is present, wrap
    // the trigger in a span so WithTooltip's aria-describedby lands on the
    // wrapper — icon-only triggers already expose their name via aria-label.
    const trigger = cloneElement(props.target, getReferenceProps({ref: reference, ...props.target.props}));
    const triggerWithTooltip = props.tooltip ? (
        <TooltipAboveModals
            title={props.tooltip}
            disabled={open}
        >
            <TooltipTrigger>
                {trigger}
            </TooltipTrigger>
        </TooltipAboveModals>
    ) : trigger;

    return (
        <>
            {triggerWithTooltip}
            <MaybePortal>
                {open && content}
            </MaybePortal>
        </>
    );
};

export default Dropdown;
