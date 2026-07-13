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
    margin: 0;
    padding-left: 24px;
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const NoParams = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 18px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const FieldList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const Field = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
`;

const FieldLabel = styled.span`
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

// Plain-text string value. Rendered as text (never through formatText/markdown)
// so LLM-supplied argument values cannot create a link-spoofing surface.
const StringValue = styled.span<{$clamped: boolean}>`
    font-size: 12px;
    font-weight: 400;
    line-height: 18px;
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

// A compact "value pill" for scalars and primitive-array items. Reserved
// styling: pills NEVER carry an icon/avatar. Phase 3 entity chips (resolved
// channels/users) always carry one, so "icon = resolved entity, plain pill =
// raw model-supplied value" stays unambiguous on the approval surface.
const ValuePill = styled.span`
    display: inline-flex;
    align-items: center;
    max-width: 100%;
    padding: 1px 6px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    font-family: var(--font-family-monospace, monospace);
    font-size: 11px;
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

interface ToolArgumentsProps {
    arguments: ToolCall['arguments'];
}

/**
 * Renders a tool call's arguments as a labeled, readable field list, with a
 * card-level "Show more" (expands all clamped values) and a required "View raw"
 * toggle that reveals the exact pretty-printed JSON payload — the approval
 * surface must always let the user inspect what they are approving. All values
 * render as plain text via styled-components; never through formatText/markdown.
 */
const ToolArguments: React.FC<ToolArgumentsProps> = ({arguments: args}) => {
    const [expanded, setExpanded] = useState(false);
    const [showRaw, setShowRaw] = useState(false);

    // Object entries in insertion order — identical on the live and persisted
    // paths (both JSON.parse the same bytes), so the layout doesn't shift when
    // a post swaps from websocket state to refetched conversation data.
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

    const rawJSON = JSON.stringify(args, null, 2);

    // Top-level arrays/primitives are not the normal tool-args shape (always a
    // JSON object). Fall back to the raw block so nothing is misrepresented.
    const canRenderFields = entries.length > 0;

    return (
        <Container>
            {showRaw || !canRenderFields ? (
                <RawJson>{rawJSON}</RawJson>
            ) : (
                <FieldList>
                    {entries.map(([key, value]) => (
                        <Field key={key}>
                            <FieldLabel>{prettifyKey(key)}</FieldLabel>
                            <FieldValue
                                value={value}
                                clamped={!expanded && isLongValue(value)}
                            />
                        </Field>
                    ))}
                </FieldList>
            )}

            <ToggleRow>
                {!showRaw && canRenderFields && hasClampable && (
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
                {canRenderFields && (
                    <ToggleButton
                        type='button'
                        onClick={() => setShowRaw((prev) => !prev)}
                    >
                        {showRaw ? (
                            <FormattedMessage
                                id='ai.tool_call.hide_raw'
                                defaultMessage='Hide raw'
                            />
                        ) : (
                            <FormattedMessage
                                id='ai.tool_call.view_raw'
                                defaultMessage='View raw'
                            />
                        )}
                    </ToggleButton>
                )}
            </ToggleRow>
        </Container>
    );
};

export default ToolArguments;
