// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {CheckIcon} from '@mattermost/compass-icons/components';

// One selectable choice offered by a question. Mirrors
// mmtools.AskUserQuestionOption on the server.
export interface QuestionOption {
    label: string;
    description?: string;
}

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

export const FreeFormTextarea = styled.textarea`
    flex: 1;
    min-width: 0;
    padding: 6px 10px;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.24);
    border-radius: 4px;
    resize: vertical;

    &:focus {
        outline: none;
        border-color: var(--button-bg);
    }

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.42);
    }
`;

// Selection state machine shared by the question surfaces (QuestionCard,
// AskUserPost). Callers gate the toggles on their own interactivity rules.
export interface OptionSelection {
    selections: string[];
    freeFormSelected: boolean;
    customText: string;
    trimmedCustom: string;
    customAnswered: boolean;
    selectedCount: number;
    toggleOption: (label: string) => void;
    toggleFreeForm: () => void;
    setCustomText: (text: string) => void;
}

export function useOptionSelection(multiSelect: boolean): OptionSelection {
    const [selections, setSelections] = useState<string[]>([]);

    // Whether the free-form "Something else…" row is selected, plus the text
    // typed into it. The row behaves like any other option for select rules.
    const [freeFormSelected, setFreeFormSelected] = useState(false);
    const [customText, setCustomText] = useState('');

    const toggleOption = (label: string) => {
        if (multiSelect) {
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
        if (multiSelect) {
            setFreeFormSelected((prev) => !prev);
        } else {
            // Single-select: choosing free-form replaces any predefined choice.
            setFreeFormSelected(true);
            setSelections([]);
        }
    };

    const trimmedCustom = customText.trim();
    const customAnswered = freeFormSelected && trimmedCustom !== '';
    const selectedCount = selections.length + (customAnswered ? 1 : 0);

    return {
        selections,
        freeFormSelected,
        customText,
        trimmedCustom,
        customAnswered,
        selectedCount,
        toggleOption,
        toggleFreeForm,
        setCustomText,
    };
}

interface QuestionOptionsProps {
    options: QuestionOption[];
    multiSelect: boolean;
    allowFreeForm: boolean;
    interactive: boolean;

    // Render the free-form field as a textarea instead of the inline input.
    multilineFreeForm?: boolean;

    // Values to display; the caller decides whether these come from live
    // state or from a resolved answer (QuestionCard's shown* pattern).
    selections: string[];
    freeFormSelected: boolean;
    customText: string;

    onToggleOption: (label: string) => void;
    onToggleFreeForm: () => void;
    onChangeCustomText: (text: string) => void;
}

const QuestionOptions: React.FC<QuestionOptionsProps> = ({
    options,
    multiSelect,
    allowFreeForm,
    interactive,
    multilineFreeForm = false,
    selections,
    freeFormSelected,
    customText,
    onToggleOption,
    onToggleFreeForm,
    onChangeCustomText,
}) => {
    const {formatMessage} = useIntl();

    const freeFormPlaceholder = formatMessage({
        id: 'ai.question.something_else',
        defaultMessage: 'Something else…',
    });

    return (
        <OptionList>
            {options.map((opt, idx) => {
                const selected = selections.includes(opt.label);
                return (
                    <OptionRow
                        key={`${idx}-${opt.label}`}
                        type='button'
                        $selected={selected}
                        $disabled={!interactive}
                        disabled={!interactive}
                        onClick={() => onToggleOption(opt.label)}
                    >
                        {multiSelect ? (
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
            {allowFreeForm && (interactive || freeFormSelected) && (

                // Selected: the "Something else…" label becomes the
                // placeholder of the free-form field.
                freeFormSelected ? (
                    <FreeFormRow>
                        <FreeFormToggle
                            type='button'
                            $disabled={!interactive}
                            disabled={!interactive}
                            onClick={onToggleFreeForm}
                        >
                            {multiSelect ? (
                                <Checkbox $checked={true}>
                                    <CheckIcon size={16}/>
                                </Checkbox>
                            ) : (
                                <NumberBadge $selected={true}>{options.length + 1}</NumberBadge>
                            )}
                        </FreeFormToggle>
                        {multilineFreeForm ? (
                            <FreeFormTextarea
                                rows={3}
                                value={customText}
                                placeholder={freeFormPlaceholder}
                                aria-label={freeFormPlaceholder}
                                disabled={!interactive}
                                onChange={(e) => onChangeCustomText(e.target.value)}
                            />
                        ) : (
                            <FreeFormInput
                                value={customText}
                                placeholder={freeFormPlaceholder}
                                aria-label={freeFormPlaceholder}
                                disabled={!interactive}
                                onChange={(e) => onChangeCustomText(e.target.value)}
                            />
                        )}
                    </FreeFormRow>
                ) : (
                    <OptionRow
                        type='button'
                        $selected={false}
                        $disabled={!interactive}
                        disabled={!interactive}
                        onClick={onToggleFreeForm}
                    >
                        {multiSelect ? (
                            <Checkbox $checked={false}/>
                        ) : (
                            <NumberBadge $selected={false}>{options.length + 1}</NumberBadge>
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
    );
};

export default QuestionOptions;
