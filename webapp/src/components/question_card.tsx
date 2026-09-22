// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {CheckIcon, CloseCircleOutlineIcon} from '@mattermost/compass-icons/components';

import {ToolCall, ToolCallStatus} from './tool_types';
import LoadingSpinner from './assets/loading_spinner';

// Parsed shape of the AskUserQuestion tool arguments. Mirrors
// mmtools.AskUserQuestionArgs on the server.
export interface QuestionOption {
    label: string;
    description?: string;
}

export interface QuestionArgs {
    question: string;
    options: QuestionOption[];
    multiSelect: boolean;
    allowFreeForm: boolean;
}

// How far parseQuestionArgs digs through nested JSON-encoded strings.
const maxArgumentUnwrapDepth = 3;

// unwrapJSONString returns the payload of a JSON-encoded string when that
// payload is itself JSON. Models and providers sometimes stringify a nested
// value — most often the option list — instead of emitting it inline. The
// server repairs these shapes before persisting (see
// mmtools.NormalizeAskUserQuestionArguments); repeating the rules here keeps
// questions asked before that landed renderable.
function unwrapJSONString(value: unknown): unknown {
    if (typeof value !== 'string') {
        return null;
    }
    try {
        return JSON.parse(value.trim());
    } catch {
        return null;
    }
}

function parseOption(value: unknown): QuestionOption | null {
    let candidate = value;
    for (let depth = 0; typeof candidate === 'string' && depth < maxArgumentUnwrapDepth; depth++) {
        const unwrapped = unwrapJSONString(candidate);

        // A bare string that is not JSON is an unambiguous label.
        if (unwrapped == null) {
            return candidate === '' ? null : {label: candidate};
        }
        candidate = unwrapped;
    }
    if (candidate == null || typeof candidate !== 'object' || Array.isArray(candidate)) {
        return null;
    }
    const optObj = candidate as {[key: string]: unknown};
    if (typeof optObj.label !== 'string' || optObj.label === '') {
        return null;
    }
    return {
        label: optObj.label,
        description: typeof optObj.description === 'string' ? optObj.description : undefined, // eslint-disable-line no-undefined
    };
}

function parseOptions(value: unknown): QuestionOption[] | null {
    let candidate = value;
    for (let depth = 0; typeof candidate === 'string' && depth < maxArgumentUnwrapDepth; depth++) {
        candidate = unwrapJSONString(candidate);
    }

    if (candidate == null || typeof candidate !== 'object') {
        return null;
    }

    // A lone option object the model forgot to wrap in an array.
    const list = Array.isArray(candidate) ? candidate : [candidate];
    const parsed: QuestionOption[] = [];
    for (const entry of list) {
        const option = parseOption(entry);
        if (option == null) {
            return null;
        }
        parsed.push(option);
    }
    return parsed.length === 0 ? null : parsed;
}

// parseBool reads a boolean flag the model may have stringified or written as
// a number. An uncoercible value returns null so the caller keeps the schema
// default: a mangled flag must not cost the user an answerable question.
function parseBool(value: unknown): boolean | null {
    if (typeof value === 'boolean') {
        return value;
    }
    if (typeof value === 'number') {
        return value !== 0;
    }
    if (typeof value === 'string') {
        switch (value.trim().toLowerCase()) {
        case 'true':
        case 'yes':
        case '1':
            return true;
        case 'false':
        case 'no':
        case '0':
            return false;
        }
    }
    return null;
}

// parseQuestionArgs extracts a renderable question from tool call arguments.
// Returns null when the arguments are missing or beyond repair (e.g. redacted
// for non-requesters) so the caller can fall back to the generic tool card.
export function parseQuestionArgs(args: ToolCall['arguments']): QuestionArgs | null {
    let obj: unknown = args;
    for (let depth = 0; typeof obj === 'string' && depth < maxArgumentUnwrapDepth; depth++) {
        obj = unwrapJSONString(obj);
    }
    if (obj == null || typeof obj !== 'object' || Array.isArray(obj)) {
        return null;
    }
    const argsObj = obj as {[key: string]: unknown};
    const question = argsObj.question;
    if (typeof question !== 'string' || question === '') {
        return null;
    }
    const options = parseOptions(argsObj.options);
    if (options == null) {
        return null;
    }
    return {
        question,
        options,
        multiSelect: parseBool(argsObj.multi_select) === true,

        // Mirror the server pointer semantics (mmtools.AskUserQuestionArgs):
        // an absent key means enabled, an explicit false disables.
        allowFreeForm: parseBool(argsObj.allow_free_form) !== false,
    };
}

// parseAnswerFromResult extracts the selected option labels and any free-form
// text from the tool result content ({"selected": [...], "custom": "..."},
// see mmtools.AskUserQuestionResult).
function parseAnswerFromResult(result?: string): {selected: string[]; custom: string} {
    if (!result) {
        return {selected: [], custom: ''};
    }
    try {
        const parsed = JSON.parse(result);
        const selected = Array.isArray(parsed?.selected) ? parsed.selected.filter((s: unknown) => typeof s === 'string') : [];
        const custom = typeof parsed?.custom === 'string' ? parsed.custom : '';
        return {selected, custom};
    } catch {
        // Not JSON — no answer to highlight.
    }
    return {selected: [], custom: ''};
}

const Card = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 16px 16px 16px 12px;
    margin: 8px 0 12px;
    overflow: hidden;
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    box-shadow: 0 2px 3px rgba(0, 0, 0, 0.08);

    &::before {
        content: '';
        position: absolute;
        top: 0;
        left: 0;
        bottom: 0;
        width: 3px;
        background: var(--button-bg);
    }
`;

const QuestionTitle = styled.div`
    padding-left: 12px;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
    word-break: break-word;
`;

const OptionList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
`;

const OptionRow = styled.button<{$selected: boolean; $disabled: boolean}>`
    position: relative;
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 44px;
    padding: 12px;
    border: none;
    text-align: left;
    border-radius: 4px;
    background: ${(props) => (props.$selected ? 'rgba(var(--button-bg-rgb), 0.08)' : 'transparent')};
    cursor: ${(props) => (props.$disabled ? 'default' : 'pointer')};

    &:hover {
        background: ${(props) => {
        if (props.$disabled) {
            return props.$selected ? 'rgba(var(--button-bg-rgb), 0.08)' : 'transparent';
        }
        return props.$selected ? 'rgba(var(--button-bg-rgb), 0.12)' : 'rgba(var(--center-channel-color-rgb), 0.04)';
    }};
    }

    &:not(:last-child)::after {
        content: '';
        position: absolute;
        left: 12px;
        right: 12px;
        bottom: 0;
        height: 1px;
        background: ${(props) => (props.$selected ? 'transparent' : 'rgba(var(--center-channel-color-rgb), 0.08)')};
    }
`;

const Checkbox = styled.span<{$checked: boolean}>`
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 20px;
    height: 20px;
    margin: 0 2px;
    border-radius: 3px;
    border: ${(props) => (props.$checked ? 'none' : '1px solid rgba(var(--center-channel-color-rgb), 0.24)')};
    background: ${(props) => (props.$checked ? 'var(--button-bg)' : 'var(--center-channel-bg)')};
    color: var(--button-color);
`;

const NumberBadge = styled.span<{$selected: boolean}>`
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 24px;
    height: 24px;
    border-radius: 4px;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    background: ${(props) => (props.$selected ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};
    color: ${(props) => (props.$selected ? 'var(--button-color)' : 'rgba(var(--center-channel-color-rgb), 0.75)')};
`;

const OptionText = styled.span`
    display: flex;
    flex-direction: column;
    min-width: 0;
`;

const OptionLabel = styled.span`
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    color: var(--center-channel-color);
    word-break: break-word;
`;

const OptionDescription = styled.span`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    word-break: break-word;
`;

// FreeFormRow mirrors a selected OptionRow but is a div so it can hold the
// inline text input (an input cannot be nested inside the button OptionRow).
const FreeFormRow = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 44px;
    padding: 12px;
    border-radius: 4px;
    background: rgba(var(--button-bg-rgb), 0.08);
`;

const FreeFormToggle = styled.button<{$disabled: boolean}>`
    display: flex;
    align-items: center;
    flex-shrink: 0;
    padding: 0;
    border: none;
    background: none;
    cursor: ${(props) => (props.$disabled ? 'default' : 'pointer')};
`;

const FreeFormInput = styled.input`
    flex: 1;
    min-width: 0;
    padding: 6px 10px;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.24);
    border-radius: 4px;

    &:focus {
        outline: none;
        border-color: var(--button-bg);
    }

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.42);
    }
`;

const Footer = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-left: 12px;
    padding-top: 4px;
`;

const SelectedCount = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const FooterButtons = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    margin-left: auto;
`;

const FooterButton = styled.button<{$primary: boolean}>`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    padding: 8px 16px;
    border: none;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    cursor: pointer;
    background: ${(props) => (props.$primary ? 'var(--button-bg)' : 'rgba(var(--button-bg-rgb), 0.08)')};
    color: ${(props) => (props.$primary ? 'var(--button-color)' : 'var(--button-bg)')};

    &:hover:not(:disabled) {
        background: ${(props) => (props.$primary ? 'rgba(var(--button-bg-rgb), 0.88)' : 'rgba(var(--button-bg-rgb), 0.12)')};
    }

    &:disabled {
        cursor: default;
        opacity: 0.5;
    }
`;

const StatusLine = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    padding-left: 12px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const AnsweredIcon = styled(CheckIcon)`
    color: var(--online-indicator);
`;

const SkippedIcon = styled(CloseCircleOutlineIcon)`
    color: var(--dnd-indicator);
`;

const ProcessingSpinner = styled(LoadingSpinner)`
    width: 12px;
    height: 12px;
`;

interface QuestionCardProps {
    tool: ToolCall;
    question: QuestionArgs;
    isProcessing: boolean;
    localDecision?: boolean;
    canAnswer: boolean;
    onAnswer?: (selections: string[], custom: string) => void;
    onSkip?: () => void;
}

const QuestionCard: React.FC<QuestionCardProps> = ({
    tool,
    question,
    isProcessing,
    localDecision,
    canAnswer,
    onAnswer,
    onSkip,
}) => {
    const {formatMessage} = useIntl();
    const [selections, setSelections] = useState<string[]>([]);

    // Whether the free-form "Something else…" row is selected, plus the text
    // typed into it. The row behaves like any other option for select rules.
    const [freeFormSelected, setFreeFormSelected] = useState(false);
    const [customText, setCustomText] = useState('');

    const isPending = tool.status === ToolCallStatus.Pending || tool.status === ToolCallStatus.Accepted;
    const isAnswered = tool.status === ToolCallStatus.Success;
    const isSkipped = tool.status === ToolCallStatus.Rejected;
    const hasLocalDecision = localDecision != null;
    const interactive = isPending && canAnswer && !isProcessing && !hasLocalDecision && Boolean(onAnswer && onSkip);

    const answered = useMemo(() => parseAnswerFromResult(tool.result), [tool.result]);
    const shownSelections = isAnswered ? answered.selected : selections;
    const shownFreeFormSelected = isAnswered ? answered.custom !== '' : freeFormSelected;
    const shownCustomText = isAnswered ? answered.custom : customText;

    const toggleOption = (label: string) => {
        if (!interactive) {
            return;
        }
        if (question.multiSelect) {
            setSelections((prev) => (
                prev.includes(label) ? prev.filter((l) => l !== label) : [...prev, label]
            ));
        } else {
            // Single-select: a predefined choice replaces any other choice,
            // including the free-form row.
            setSelections([label]);
            setFreeFormSelected(false);
        }
    };

    const toggleFreeForm = () => {
        if (!interactive) {
            return;
        }
        if (question.multiSelect) {
            setFreeFormSelected((prev) => !prev);
        } else {
            // Single-select: choosing free-form replaces any predefined choice.
            setFreeFormSelected(true);
            setSelections([]);
        }
    };

    const trimmedCustom = customText.trim();
    const customAnswered = freeFormSelected && trimmedCustom !== '';

    // Accept requires at least one valid choice. When free-form is selected its
    // text must be non-empty; otherwise a predefined option must be selected.
    const canSubmit = freeFormSelected ? (customAnswered || selections.length > 0) : selections.length > 0;
    const selectedCount = selections.length + (customAnswered ? 1 : 0);

    const renderStatus = () => {
        if (isProcessing || (hasLocalDecision && isPending)) {
            return (
                <StatusLine>
                    <ProcessingSpinner/>
                    <FormattedMessage
                        id='ai.question.submitting'
                        defaultMessage='Submitting…'
                    />
                </StatusLine>
            );
        }
        if (isAnswered) {
            return (
                <StatusLine>
                    <AnsweredIcon size={16}/>
                    <FormattedMessage
                        id='ai.question.answered'
                        defaultMessage='Answered'
                    />
                </StatusLine>
            );
        }
        if (isSkipped) {
            return (
                <StatusLine>
                    <SkippedIcon size={16}/>
                    <FormattedMessage
                        id='ai.question.skipped'
                        defaultMessage='Skipped'
                    />
                </StatusLine>
            );
        }
        if (isPending && !canAnswer) {
            return (
                <StatusLine>
                    <FormattedMessage
                        id='ai.question.waiting_for_requester'
                        defaultMessage='Waiting for an answer from the requester'
                    />
                </StatusLine>
            );
        }
        return null;
    };

    return (
        <Card>
            <QuestionTitle>{question.question}</QuestionTitle>
            <OptionList>
                {question.options.map((opt, idx) => {
                    const selected = shownSelections.includes(opt.label);
                    return (
                        <OptionRow
                            key={opt.label}
                            type='button'
                            $selected={selected}
                            $disabled={!interactive}
                            onClick={() => toggleOption(opt.label)}
                        >
                            {question.multiSelect ? (
                                <Checkbox $checked={selected}>
                                    {selected && <CheckIcon size={16}/>}
                                </Checkbox>
                            ) : (
                                <NumberBadge $selected={selected}>{idx + 1}</NumberBadge>
                            )}
                            <OptionText>
                                <OptionLabel>{opt.label}</OptionLabel>
                                {opt.description && <OptionDescription>{opt.description}</OptionDescription>}
                            </OptionText>
                        </OptionRow>
                    );
                })}
                {question.allowFreeForm && (interactive || shownFreeFormSelected) && (

                    // Selected: the "Something else…" label becomes the
                    // placeholder of an inline single-line input.
                    shownFreeFormSelected ? (
                        <FreeFormRow>
                            <FreeFormToggle
                                type='button'
                                $disabled={!interactive}
                                onClick={toggleFreeForm}
                            >
                                {question.multiSelect ? (
                                    <Checkbox $checked={true}>
                                        <CheckIcon size={16}/>
                                    </Checkbox>
                                ) : (
                                    <NumberBadge $selected={true}>{question.options.length + 1}</NumberBadge>
                                )}
                            </FreeFormToggle>
                            <FreeFormInput
                                value={shownCustomText}
                                placeholder={formatMessage({
                                    id: 'ai.question.something_else',
                                    defaultMessage: 'Something else…',
                                })}
                                disabled={!interactive}
                                onChange={(e) => setCustomText(e.target.value)}
                            />
                        </FreeFormRow>
                    ) : (
                        <OptionRow
                            type='button'
                            $selected={false}
                            $disabled={!interactive}
                            onClick={toggleFreeForm}
                        >
                            {question.multiSelect ? (
                                <Checkbox $checked={false}/>
                            ) : (
                                <NumberBadge $selected={false}>{question.options.length + 1}</NumberBadge>
                            )}
                            <OptionText>
                                <OptionLabel>
                                    <FormattedMessage
                                        id='ai.question.something_else'
                                        defaultMessage='Something else…'
                                    />
                                </OptionLabel>
                            </OptionText>
                        </OptionRow>
                    )
                )}
            </OptionList>
            {interactive && (
                <Footer>
                    {question.multiSelect && (
                        <SelectedCount>
                            <FormattedMessage
                                id='ai.question.selected_count'
                                defaultMessage='{count, plural, =0 {None selected} one {# selected} other {# selected}}'
                                values={{count: selectedCount}}
                            />
                        </SelectedCount>
                    )}
                    <FooterButtons>
                        <FooterButton
                            type='button'
                            $primary={false}
                            onClick={onSkip}
                        >
                            <FormattedMessage
                                id='ai.question.skip'
                                defaultMessage='Skip'
                            />
                        </FooterButton>
                        <FooterButton
                            type='button'
                            $primary={true}
                            disabled={!canSubmit}
                            onClick={() => onAnswer?.(selections, customAnswered ? trimmedCustom : '')}
                        >
                            <FormattedMessage
                                id='ai.question.accept'
                                defaultMessage='Accept'
                            />
                        </FooterButton>
                    </FooterButtons>
                </Footer>
            )}
            {renderStatus()}
        </Card>
    );
};

export default QuestionCard;
