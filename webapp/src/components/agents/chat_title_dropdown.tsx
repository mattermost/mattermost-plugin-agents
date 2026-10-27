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

    // The floating menu's reference: the wrapper element (the compass Dropdown itself takes no ref).
    ref?: React.Ref<HTMLSpanElement>;

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
// title open the same menu from it. The floating menu needs an element that takes a ref, which the
// compass Dropdown does not, hence the wrapper; and the menu's other reference props (its
// handlers and aria attributes) belong on the button, so they are passed on to it.
const ChatTitleDropdown = ({ref, label, ariaLabel, expanded, onClick, testId, heading = false, ...buttonProps}: ChatTitleDropdownProps) => (
    <Trigger ref={ref}>
        <TitleDropdown
            {...buttonProps}
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

export default ChatTitleDropdown;

const Trigger = styled.span`
    display: inline-flex;
    min-width: 0;
    max-width: ${TITLE_MAX_WIDTH};
`;

// The compass label is nowrap and none of the wrappers around it (the button's own label span
// included) shrink below their content, so a long title would run past the bar: let them shrink and
// end the label in an ellipsis. (The class names are compass's generated ones.)
const TitleDropdown = styled(Dropdown)`
    && {
        min-width: 0;
        max-width: 100%;
    }

    && [class*='button__label'],
    && [class*='dropdown__trigger'],
    && [class*='dropdown__content'],
    && [class*='dropdown__label'] {
        min-width: 0;
    }

    && [class*='button__label'] {
        overflow: hidden;
    }

    /* An inline-flex box takes the width of its nowrap text (its min-content) instead of the space
       there is: filling the label wrapper lets the content inside shrink. */
    && [class*='dropdown__trigger'] {
        display: flex;
        width: 100%;
    }

    && [class*='dropdown__content'],
    && [class*='dropdown__label'] {
        overflow: hidden;
        flex-shrink: 1;
    }

    && [class*='dropdown__label'] {
        text-overflow: ellipsis;
    }
`;

// Takes the label's typography (the compass Dropdown sets it for its size).
const Heading = styled.h1`
    margin: 0;
    overflow: hidden;
    font: inherit;
    text-overflow: ellipsis;
    white-space: nowrap;
`;
