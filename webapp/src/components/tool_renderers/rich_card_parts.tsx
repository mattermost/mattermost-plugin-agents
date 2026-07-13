// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Shared building blocks for the rich per-tool cards: layout primitives, a
// message preview with a Show more toggle, and labeled filter pills. Rich cards
// render only the "arguments body"; the surrounding approval chrome (header,
// buttons, result, View raw) is provided by ToolCardShell.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {ToolCardShellProps} from './tool_card_shell';

// Rich cards accept the same props ToolCard does; they supply the body and
// forward the rest to ToolCardShell.
export type RichCardProps = Omit<ToolCardShellProps, 'children'>;

export const RichBody = styled.div`
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding-left: 24px;
`;

export const Section = styled.div`
    display: flex;
    flex-direction: column;
    gap: 3px;
    min-width: 0;
`;

export const SectionLabel = styled.span`
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

export const SectionRow = styled.div`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    min-width: 0;
`;

const PreviewText = styled.div<{$clamped: boolean}>`
    font-size: 12px;
    font-weight: 400;
    line-height: 18px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    white-space: pre-wrap;
    overflow-wrap: anywhere;

    ${(props) => props.$clamped && `
        display: -webkit-box;
        -webkit-line-clamp: 4;
        -webkit-box-orient: vertical;
        overflow: hidden;
    `}
`;

const ToggleButton = styled.button`
    align-self: flex-start;
    margin-top: 4px;
    padding: 0;
    border: none;
    background: none;
    cursor: pointer;
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: var(--link-color);

    &:hover {
        text-decoration: underline;
    }
`;

const LabeledPillEl = styled.span`
    display: inline-flex;
    align-items: baseline;
    gap: 4px;
    max-width: 100%;
    padding: 1px 6px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    font-size: 11px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    overflow-wrap: anywhere;
`;

const PillKey = styled.span`
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

// MessagePreview shows a (possibly long) message. Long messages clamp with a
// Show more toggle — the user is approving a message that will be posted, so the
// full text must always be reachable.
export const MessagePreview: React.FC<{text: string}> = ({text}) => {
    const [expanded, setExpanded] = useState(false);
    const isLong = text.length > 220 || text.split('\n').length > 4;

    return (
        <>
            <PreviewText $clamped={isLong && !expanded}>{text}</PreviewText>
            {isLong && (
                <ToggleButton
                    type='button'
                    onClick={() => setExpanded((prev) => !prev)}
                >
                    {expanded ? (
                        <FormattedMessage
                            id='ai.tool_call.show_less'
                            defaultMessage='Show less'
                        />
                    ) : (
                        <FormattedMessage
                            id='ai.tool_call.show_more'
                            defaultMessage='Show more'
                        />
                    )}
                </ToggleButton>
            )}
        </>
    );
};

export interface LabeledPillData {
    label: string;
    value: string;
}

export const LabeledPill: React.FC<LabeledPillData> = ({label, value}) => (
    <LabeledPillEl>
        {label ? <PillKey>{label}</PillKey> : null}
        <span>{value}</span>
    </LabeledPillEl>
);

// TagPill is a small standalone indicator pill (e.g. "Reply", "With thread")
// whose content is an i18n'd node rather than a key/value pair.
export const TagPill = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;
