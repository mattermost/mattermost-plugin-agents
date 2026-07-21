// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {JSONValue, ToolCall} from './tool_types';

// Values longer than this (characters) are clamped behind the card-level
// "Show more" toggle. Applies to string values and inline JSON blocks.
const CLAMP_CHAR_THRESHOLD = 160;

const Container = styled.div`
    margin-top: 12px;
    display: flex;
    flex-direction: column;
    gap: 10px;
`;

const NoParams = styled.div`
    font-size: 13px;
    font-weight: 400;
    line-height: 18px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

// Two-column label/value grid per the tool-card designs: bold labels left,
// values right, columns aligned across rows.
const FieldList = styled.div`
    display: grid;
    grid-template-columns: minmax(96px, max-content) 1fr;
    column-gap: 32px;
    row-gap: 12px;
    align-items: baseline;
`;

// display: contents so the label and value become sibling grid cells while
// each field stays one component.
const Field = styled.div`
    display: contents;
`;

const FieldLabel = styled.span`
    font-size: 13px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const ValueCell = styled.div`
    min-width: 0;
`;

// Plain text, never formatText/markdown: LLM-supplied values must not become
// a link-spoofing surface.
const StringValue = styled.span<{$clamped: boolean}>`
    font-size: 13px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    white-space: pre-wrap;
    overflow-wrap: anywhere;

    ${(props) => props.$clamped && `
        display: -webkit-box;
        -webkit-line-clamp: 3;
        -webkit-box-orient: vertical;
        overflow: hidden;
    `}
`;

const PillRow = styled.div`
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
`;

// Compact pill for scalars and primitive-array items. Pills never carry an
// icon/avatar — that styling is reserved for resolved entity chips, so raw
// model values can't be mistaken for verified entities.
const ValuePill = styled.span`
    display: inline-flex;
    align-items: center;
    max-width: 100%;
    padding: 1px 6px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    font-family: var(--font-family-monospace, monospace);
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    overflow-wrap: anywhere;
`;

const InlineJson = styled.pre<{$clamped: boolean}>`
    margin: 0;
    padding: 8px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    font-family: var(--font-family-monospace, monospace);
    font-size: 11px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    white-space: pre-wrap;
    overflow-wrap: anywhere;

    ${(props) => props.$clamped && `
        display: -webkit-box;
        -webkit-line-clamp: 6;
        -webkit-box-orient: vertical;
        overflow: hidden;
    `}
`;

const RawJson = styled.pre`
    margin: 0;
    padding: 8px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    font-family: var(--font-family-monospace, monospace);
    font-size: 11px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
`;

const ToggleRow = styled.div`
    display: flex;
    gap: 12px;
`;

const ToggleButton = styled.button`
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

export function isEmptyToolArgumentsObject(argumentsValue: ToolCall['arguments']): boolean {
    return argumentsValue != null &&
        typeof argumentsValue === 'object' &&
        !Array.isArray(argumentsValue) &&
        Object.keys(argumentsValue).length === 0;
}

// hasInspectableArguments reports whether the arguments warrant a "View raw"
// toggle (a non-empty JSON object).
export function hasInspectableArguments(argumentsValue: ToolCall['arguments']): boolean {
    return argumentsValue != null &&
        typeof argumentsValue === 'object' &&
        !Array.isArray(argumentsValue) &&
        Object.keys(argumentsValue).length > 0;
}

function isPrimitive(value: JSONValue): value is string | number | boolean | null {
    return value === null || typeof value !== 'object';
}

/** Prettify an argument key: underscores → spaces, Title Case each word. */
function prettifyKey(key: string): string {
    return key.
        replace(/_/g, ' ').
        split(' ').
        map((word) => (word ? word.charAt(0).toUpperCase() + word.slice(1) : word)).
        join(' ');
}

function scalarText(value: string | number | boolean | null): string {
    if (value === null) {
        return 'null';
    }
    return String(value);
}

// A field's value is "long" (clampable behind Show more) when it is a
// multi-line/large string or a large inline JSON block. Scalars and
// primitive-array pills are always short.
function isLongValue(value: JSONValue): boolean {
    if (typeof value === 'string') {
        return value.length > CLAMP_CHAR_THRESHOLD || value.includes('\n');
    }
    if (value !== null && typeof value === 'object') {
        const isPrimitiveArray = Array.isArray(value) && value.every(isPrimitive);
        if (isPrimitiveArray) {
            return false;
        }
        return JSON.stringify(value, null, 2).length > CLAMP_CHAR_THRESHOLD;
    }
    return false;
}

interface FieldValueProps {
    value: JSONValue;
    clamped: boolean;
}

const FieldValue: React.FC<FieldValueProps> = ({value, clamped}) => {
    if (typeof value === 'string') {
        return <StringValue $clamped={clamped}>{value}</StringValue>;
    }
    if (isPrimitive(value)) {
        return <ValuePill>{scalarText(value)}</ValuePill>;
    }
    if (Array.isArray(value) && value.every(isPrimitive)) {
        if (value.length === 0) {
            return <ValuePill>{'[]'}</ValuePill>;
        }
        return (
            <PillRow>
                {value.map((item, idx) => (
                    <ValuePill key={idx}>{scalarText(item)}</ValuePill>
                ))}
            </PillRow>
        );
    }

    // Nested object or array-of-objects: compact inline JSON.
    return <InlineJson $clamped={clamped}>{JSON.stringify(value, null, 2)}</InlineJson>;
};

// ToolArgumentsRaw renders the exact pretty-printed JSON payload, shown by the
// card shell's "View raw" toggle.
export const ToolArgumentsRaw: React.FC<{arguments: ToolCall['arguments']}> = ({arguments: args}) => {
    if (args == null) {
        return null;
    }
    return (
        <Container>
            <RawJson>{JSON.stringify(args, null, 2)}</RawJson>
        </Container>
    );
};

interface ToolArgumentsProps {
    arguments: ToolCall['arguments'];
}

/**
 * Renders a tool call's arguments as a labeled field list with a card-level
 * "Show more" that expands all clamped values at once. Values render as plain
 * text only. The "View raw" toggle lives on ToolCardShell.
 */
const ToolArguments: React.FC<ToolArgumentsProps> = ({arguments: args}) => {
    const [expanded, setExpanded] = useState(false);

    // Insertion key order — identical bytes on the live and persisted paths,
    // so the layout doesn't shift when a post reloads.
    const entries = useMemo<Array<[string, JSONValue]>>(() => {
        if (args == null || typeof args !== 'object' || Array.isArray(args)) {
            return [];
        }
        return Object.entries(args as {[key: string]: JSONValue});
    }, [args]);

    const hasClampable = useMemo(
        () => entries.some(([, value]) => isLongValue(value)),
        [entries],
    );

    // Redacted (non-requester) or absent arguments: render nothing.
    if (args == null) {
        return null;
    }

    if (isEmptyToolArgumentsObject(args)) {
        return (
            <Container>
                <NoParams>
                    <FormattedMessage
                        id='ai.tool_call.no_parameters_required'
                        defaultMessage='No parameters required'
                    />
                </NoParams>
            </Container>
        );
    }

    // Non-object args (unexpected shape): show the raw payload verbatim.
    if (entries.length === 0) {
        return <ToolArgumentsRaw arguments={args}/>;
    }

    return (
        <Container>
            <FieldList>
                {entries.map(([key, value]) => (
                    <Field key={key}>
                        <FieldLabel>{prettifyKey(key)}</FieldLabel>
                        <ValueCell>
                            <FieldValue
                                value={value}
                                clamped={!expanded && isLongValue(value)}
                            />
                        </ValueCell>
                    </Field>
                ))}
            </FieldList>

            {hasClampable && (
                <ToggleRow>
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
                </ToggleRow>
            )}
        </Container>
    );
};

export default ToolArguments;
