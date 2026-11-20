// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import {Dropdown} from '@mattermost/compass-ui/components/dropdown';

// Caps the button so a long chat title never pushes the buttons beside it off the bar. No compass
// token for a width of this size.
const TITLE_MAX_WIDTH = 'min(100%, 40rem)';

type DropdownButtonProps = Omit<React.ComponentProps<typeof Dropdown>, 'children' | 'size' | 'isOpen' | 'ref' | 'onClick'>;

interface ChatTitleDropdownProps extends DropdownButtonProps {

    // The floating menu's reference: the button, so focus returns to it when the menu closes.
    ref?: React.Ref<HTMLButtonElement>;

    // The chat's title, or the agent's name in a new chat.
    label: string;
    ariaLabel: string;
    expanded: boolean;
    onClick: () => void;
    testId: string;

    // Renders the label as the page's level-1 heading (the product chat header).
    heading?: boolean;
}

// The title of an agent chat as a compass Dropdown trigger: the product's chat header and the RHS
// title open the same menu from it. The menu's reference (the ref) and its other reference props
// (handlers, aria attributes) are passed on to the button. Compass Dropdown (0.1.0-alpha.36+)
// ellipsizes a long label once the host constrains the button's available width.
const ChatTitleDropdown = ({ref, label, ariaLabel, expanded, onClick, testId, heading = false, ...buttonProps}: ChatTitleDropdownProps) => {
    // React 19 passes `ref` to a function component as a prop, and the compass Dropdown spreads its
    // other props onto its button, so the ref reaches the button; its props type does not list it.
    const refProp = {ref} as Record<string, unknown>;
    return (
        <Trigger>
            <TitleDropdown
                {...buttonProps}
                {...refProp}
                size='large'
                isOpen={expanded}
                aria-label={ariaLabel}
                aria-haspopup='menu'
                aria-expanded={expanded}
                title={label}
                data-testid={testId}
                onClick={onClick}
            >
                {heading ? <Heading>{label}</Heading> : label}
            </TitleDropdown>
        </Trigger>
    );
};

export default ChatTitleDropdown;

// Host shrink path: a flex header will not give a standalone button a definite width unless this
// wrapper participates with min-width: 0. Grow stays off so a short title does not stretch and push
// the agent name away. The absolute max lives on TitleDropdown so the button itself is capped.
const Trigger = styled.span`
    display: flex;
    flex: 0 1 auto;
    justify-content: flex-start;
    min-width: 0;
    max-width: 100%;
    overflow: hidden;
`;

// Fill the Trigger and cap the button so a long title ellipsizes instead of eating the header
// (compass handles overflow, start-align, and __content/__label shrink).
const TitleDropdown = styled(Dropdown)`
    && {
        width: 100%;
        max-width: ${TITLE_MAX_WIDTH};
    }
`;

// Takes the label's typography (the compass Dropdown sets it for its size). Ellipsis stays on the
// heading itself because text-overflow on the compass label does not apply through a block child.
const Heading = styled.h1`
    margin: 0;
    min-width: 0;
    max-width: 100%;
    overflow: hidden;
    font: inherit;
    text-align: start;
    text-overflow: ellipsis;
    white-space: nowrap;
`;
