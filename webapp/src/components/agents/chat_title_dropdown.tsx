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
// (handlers, aria attributes) are passed on to the button. The wrapper caps width and supplies the
// flex shrink path a standalone button needs to ellipsize in the header.
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
                {heading ? <Heading>{label}</Heading> : <Truncate>{label}</Truncate>}
            </TitleDropdown>
        </Trigger>
    );
};

export default ChatTitleDropdown;

// A flex header will not ellipsize a standalone button unless this wrapper is a real flex
// participant with a definite shrink path. Keep grow off so a short title does not stretch and push
// the agent name away. Start-align so a content-sized button never hard-clips both ends.
const Trigger = styled.span`
    display: flex;
    flex: 0 1 auto;
    justify-content: flex-start;
    min-width: 0;
    max-width: ${TITLE_MAX_WIDTH};
    overflow: hidden;
`;

// The compass label is nowrap and the wrappers around it shrink no smaller than their content, so a
// long title would run past the bar: let them shrink and end the label in an ellipsis. (The class
// names are compass's generated ones.) flex: 1 1 0% + width: 100% gives the button a definite
// shrink width inside Trigger; start-align overrides the UA button text-align: center.
const TitleDropdown = styled(Dropdown)`
    && {
        flex: 1 1 0%;
        width: 100%;
        min-width: 0;
        max-width: 100%;
        overflow: hidden;
        justify-content: flex-start;
        text-align: start;
    }

    && [class*='dropdown__trigger'],
    && [class*='dropdown__content'],
    && [class*='dropdown__label'] {
        min-width: 0;
    }

    /* An inline-flex box takes the width of its nowrap text (its min-content) instead of the space
       there is: filling the label wrapper lets the content inside shrink. */
    && [class*='dropdown__trigger'] {
        display: flex;
        justify-content: flex-start;
        width: 100%;
        max-width: 100%;
    }

    && [class*='dropdown__content'] {
        display: flex;
        flex: 1 1 0%;
        max-width: 100%;
        overflow: hidden;
    }

    && [class*='dropdown__label'] {
        display: block;
        flex: 1 1 0%;
        max-width: 100%;
        overflow: hidden;
        text-align: start;
        text-overflow: ellipsis;
        white-space: nowrap;
    }
`;

// Owned ellipsis surface so truncation does not depend only on compass label styles winning.
const Truncate = styled.span`
    display: block;
    min-width: 0;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

// Takes the label's typography (the compass Dropdown sets it for its size).
const Heading = styled.h1`
    margin: 0;
    overflow: hidden;
    font: inherit;
    text-align: start;
    text-overflow: ellipsis;
    white-space: nowrap;
`;
