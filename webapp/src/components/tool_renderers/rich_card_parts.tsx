// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Shared building blocks for the rich per-tool cards: layout primitives, a
// message preview with a Show more toggle, and labeled pills.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {ToolCardShellProps} from './tool_card_shell';

// Rich cards accept the same props ToolCard does; they supply the body and
// forward the rest to ToolCardShell.
export type RichCardProps = Omit<ToolCardShellProps, 'children'>;

// Two-column label/value grid per the tool-card designs (matches the generic
// field list): bold labels left, values right, columns aligned across rows.
export const RichBody = styled.div`
    margin-top: 12px;
    display: grid;
    grid-template-columns: minmax(96px, max-content) 1fr;
    column-gap: 32px;
    row-gap: 12px;
    align-items: baseline;
`;

// display: contents so the label and value become sibling grid cells while
// each section stays one component. Sections must have exactly two children.
export const Section = styled.div`
    display: contents;
`;

export const SectionLabel = styled.span`
    font-size: 13px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
`;

export const SectionRow = styled.div`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    min-width: 0;
`;

const PreviewWrap = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    min-width: 0;
`;

const PreviewText = styled.div<{$clamped: boolean}>`
    font-size: 13px;
    font-weight: 400;
    line-height: 20px;
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
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    overflow-wrap: anywhere;
`;

const PillKey = styled.span`
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

// MessagePreview clamps long messages behind a Show more toggle; the full text
// must always be reachable since the user is approving it. Wrapped in a single
// element so it can sit in a grid value cell.
export const MessagePreview: React.FC<{text: string}> = ({text}) => {
    const [expanded, setExpanded] = useState(false);
    const isLong = text.length > 220 || text.split('\n').length > 4;

    return (
        <PreviewWrap>
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
        </PreviewWrap>
    );
};

interface LabeledPillProps {
    label: React.ReactNode;
    value: string;
}

export const LabeledPill: React.FC<LabeledPillProps> = ({label, value}) => (
    <LabeledPillEl>
        <PillKey>{label}</PillKey>
        <span>{value}</span>
    </LabeledPillEl>
);

// TagPill is a small indicator pill (e.g. "Reply", "With thread").
export const TagPill = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;
