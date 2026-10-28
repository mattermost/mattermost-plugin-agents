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

    // A dim second line under the title: the agent's name, once the chat has a title of its own.
    subtitle?: string;
}

// The title of an agent chat as a compass Dropdown trigger: the product's chat header and the RHS
// title open the same menu from it. The menu's reference (the ref) and its other reference props
// (handlers, aria attributes) are passed on to the button. The wrapper only limits the width.
const ChatTitleDropdown = ({ref, label, ariaLabel, expanded, onClick, testId, heading = false, subtitle, ...buttonProps}: ChatTitleDropdownProps) => {
    // React 19 passes `ref` to a function component as a prop, and the compass Dropdown spreads its
    // other props onto its button, so the ref reaches the button; its props type does not list it.
    const refProp = {ref} as Record<string, unknown>;
    const titleLabel = heading ? <Heading>{label}</Heading> : <Line>{label}</Line>;
    let content: React.ReactNode = label;
    if (heading) {
        content = <Heading>{label}</Heading>;
    }
    if (subtitle) {
        content = (
            <Stack>
                {titleLabel}
                <Subtitle>{subtitle}</Subtitle>
            </Stack>
        );
    }
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
                $stacked={Boolean(subtitle)}
                onClick={onClick}
            >
                {content}
            </TitleDropdown>
        </Trigger>
    );
};

export default ChatTitleDropdown;

const Trigger = styled.span`
    display: inline-flex;
    min-width: 0;
    max-width: ${TITLE_MAX_WIDTH};
`;

// The compass label is nowrap and none of the wrappers around it (the button's own label span
// included) shrink below their content, so a long title would run past the bar: let them shrink and
// end the label in an ellipsis. (The class names are compass's generated ones.)
const TitleDropdown = styled(Dropdown)<{$stacked: boolean}>`
    && {
        min-width: 0;
        max-width: 100%;
        ${({$stacked}) => $stacked && 'height: auto;'}
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

// The title over the agent's name, both ending in an ellipsis. The title keeps the label's type;
// the name is a dim, smaller line (host 64% text).
const Stack = styled.span`
    display: flex;
    min-width: 0;
    flex-direction: column;
    align-items: flex-start;
    text-align: start;
`;

const Line = styled.span`
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Subtitle = styled(Line)`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-75);
`;
