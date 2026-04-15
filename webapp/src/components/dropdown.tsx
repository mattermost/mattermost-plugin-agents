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

const FloatingContainer = styled.div`
    min-width: 16rem;
	z-index: 2100;
`;

type DropdownProps = {
    target: JSX.Element;
    children: React.ReactNode;
    placement?: Placement;
    offset?: Parameters<typeof offset>[0];
    flip?: Parameters<typeof flip>[0];
    shift?: Parameters<typeof shift>[0];
    focusManager?: boolean | Omit<ComponentProps<typeof FloatingFocusManager>, 'context' | 'children'>;
    portal?: boolean;
    isOpen: boolean;
    onOpenChange?: ((open: boolean) => void);
};

const Dropdown = (props: DropdownProps) => {
    const [isOpen, setIsOpen] = useState(props.isOpen);

    const open = props.isOpen ?? isOpen;

    const setOpen = (updatedOpen: boolean) => {
        props.onOpenChange?.(updatedOpen);
        setIsOpen(updatedOpen);
    };

    const {strategy, x, y, reference, floating, context} = useFloating<HTMLElement>({
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
            {...getFloatingProps({
                ref: floating,
                style: {
                    position: strategy,
                    top: y ?? 0,
                    left: x ?? 0,
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

    return (
        <>
            {cloneElement(props.target, getReferenceProps({ref: reference, ...props.target.props}))}
            <MaybePortal>
                {open && content}
            </MaybePortal>
        </>
    );
};

export default Dropdown;
