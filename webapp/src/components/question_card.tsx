// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
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
}

// parseQuestionArgs extracts a renderable question from tool call arguments.
// Returns null when the arguments are missing or malformed (e.g. redacted for
// non-requesters) so the caller can fall back to the generic tool card.
export function parseQuestionArgs(args: ToolCall['arguments']): QuestionArgs | null {
    if (args == null || typeof args !== 'object' || Array.isArray(args)) {
        return null;
    }
    const obj = args as {[key: string]: unknown};
    const question = obj.question;
    const options = obj.options;
    if (typeof question !== 'string' || question === '' || !Array.isArray(options) || options.length === 0) {
        return null;
    }
    const parsedOptions: QuestionOption[] = [];
    for (const opt of options) {
        if (opt == null || typeof opt !== 'object' || Array.isArray(opt)) {
            return null;
        }
        const optObj = opt as {[key: string]: unknown};
        if (typeof optObj.label !== 'string' || optObj.label === '') {
            return null;
        }
        parsedOptions.push({
            label: optObj.label,
            description: typeof optObj.description === 'string' ? optObj.description : undefined, // eslint-disable-line no-undefined
        });
    }
    return {
        question,
        options: parsedOptions,
        multiSelect: obj.multi_select === true,
    };
}

// parseSelectedFromResult extracts the selected option labels from the tool
// result content ({"selected": [...]}, see mmtools.AskUserQuestionResult).
function parseSelectedFromResult(result?: string): string[] {
    if (!result) {
        return [];
    }
    try {
        const parsed = JSON.parse(result);
        if (parsed && Array.isArray(parsed.selected)) {
            return parsed.selected.filter((s: unknown) => typeof s === 'string');
        }
    } catch {
        // Not JSON — no selections to highlight.
    }
    return [];
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
    onAnswer?: (selections: string[]) => void;
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
    const [selections, setSelections] = useState<string[]>([]);

    const isPending = tool.status === ToolCallStatus.Pending || tool.status === ToolCallStatus.Accepted;
    const isAnswered = tool.status === ToolCallStatus.Success;
    const isSkipped = tool.status === ToolCallStatus.Rejected;
    const hasLocalDecision = localDecision != null;
    const interactive = isPending && canAnswer && !isProcessing && !hasLocalDecision && Boolean(onAnswer && onSkip);

    const answeredSelections = useMemo(() => parseSelectedFromResult(tool.result), [tool.result]);
    const shownSelections = isAnswered ? answeredSelections : selections;

    const toggleOption = (label: string) => {
        if (!interactive) {
            return;
        }
        if (question.multiSelect) {
            setSelections((prev) => (
                prev.includes(label) ? prev.filter((l) => l !== label) : [...prev, label]
            ));
        } else {
            setSelections([label]);
        }
    };

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
            </OptionList>
            {interactive && (
                <Footer>
                    {question.multiSelect && (
                        <SelectedCount>
                            <FormattedMessage
                                id='ai.question.selected_count'
                                defaultMessage='{count, plural, =0 {None selected} one {# selected} other {# selected}}'
                                values={{count: selections.length}}
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
                            disabled={selections.length === 0}
                            onClick={() => onAnswer?.(selections)}
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
