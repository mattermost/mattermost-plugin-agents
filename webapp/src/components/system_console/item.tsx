// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled, {createGlobalStyle} from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, CloseIcon} from '@mattermost/compass-icons/components';
import CreatableSelect from 'react-select/creatable';
import {components, StylesConfig, SingleValue, type ClearIndicatorProps, type DropdownIndicatorProps} from 'react-select';

import {getPortalTarget} from '../../utils/dom';

// Portaled Combobox menus need to stack above the agent config modal overlay
// (z-index 2000). Targets react-select's classNamePrefix='SystemConsoleCombobox'
// so the z-index lives in styled-components rather than inline style props.
// react-select v5 emits its default menuPortalCSS (z-index: 1) via an
// @emotion/react generated className, so whether that or our global rule wins
// depends on CSS declaration order at runtime. Use !important to make the
// override deterministic regardless of which stylesheet is parsed last.
const ComboboxPortalStyles = createGlobalStyle`
    .SystemConsoleCombobox__menu-portal {
        z-index: 10000 !important;
    }
`;

export const ItemList = styled.div`
	display: flex;
	flex-direction: column;
	gap: 24px;
`;

export const FormRow = styled.div`
	display: grid;
	grid-template-columns: minmax(auto, 275px) 1fr;
	grid-column-gap: 16px;
	align-items: start;
`;

export const FieldControlRow = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	min-height: 35px;
	gap: 8px;
	width: 100%;

	> input:not([type='radio']):not([type='checkbox']),
	> textarea {
		width: 100%;
	}

	> div {
		width: 100%;
	}
`;

export const FieldErrorText = styled.div`
	color: var(--dnd-indicator, #D24B4E);
	font-size: 12px;
`;

export type TextItemProps = {
    label: string,
    value: string,
    type?: string,
    helptext?: React.ReactNode,
    multiline?: boolean,
    placeholder?: string,
    maxLength?: number,
    step?: string,
    min?: string,
    max?: string,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => void,
    onBlur?: (e: React.FocusEvent<HTMLInputElement>) => void,
    onFocus?: (e: React.FocusEvent<HTMLInputElement>) => void,
    disabled?: boolean,
    readOnly?: boolean,
    error?: string,
};

export const TextItem = (props: TextItemProps) => {
    const label = props.readOnly ? (
        <ItemLabelWithTag
            label={props.label}
            readOnly={true}
            $multiline={props.multiline}
        />
    ) : (
        <ItemLabel $multiline={props.multiline}>{props.label}</ItemLabel>
    );

    return (
        <FormRow>
            {label}
            <TextFieldContainer>
                {props.error && <FieldErrorText>{props.error}</FieldErrorText>}
                <FieldControlRow>
                    <StyledInput
                        as={props.multiline ? 'textarea' : 'input'}
                        readOnly={props.readOnly}
                        value={props.value}
                        type={props.type ? props.type : 'text'}
                        placeholder={props.placeholder ? props.placeholder : props.label}
                        onChange={props.onChange}
                        onBlur={props.onBlur}
                        onFocus={props.onFocus}
                        maxLength={props.maxLength}
                        step={props.step}
                        min={props.min}
                        max={props.max}
                        disabled={props.disabled}
                    />
                </FieldControlRow>
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </FormRow>
    );
};

export const SelectionItemOption = styled.option`
`;

export type SelectionItemProps = {
    label: string
    value: string
    onChange: (e: React.ChangeEvent<HTMLSelectElement>) => void
    children: React.ReactNode
    helptext?: string
    disabled?: boolean
    error?: string
};

export const SelectionItem = (props: SelectionItemProps) => {
    return (
        <FormRow>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                {props.error && <FieldErrorText>{props.error}</FieldErrorText>}
                <FieldControlRow>
                    <SelectField
                        value={props.value}
                        onChange={props.onChange}
                        disabled={props.disabled}
                    >
                        {props.children}
                    </SelectField>
                </FieldControlRow>
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </FormRow>
    );
};

export type ComboboxOption = {
    id: string
    displayName: string
}

export type ComboboxItemProps = {
    label: string
    value: string
    options: ComboboxOption[]
    placeholder?: string
    helptext?: string
    isClearable?: boolean
    onChange: (value: string) => void
};

type SelectOption = {
    value: string
    label: string
}

function ComboboxDropdownIndicator(props: DropdownIndicatorProps<SelectOption>) {
    return (
        <components.DropdownIndicator {...props}>
            <ChevronDownIcon size={16}/>
        </components.DropdownIndicator>
    );
}

function ComboboxClearIndicator(props: ClearIndicatorProps<SelectOption>) {
    return (
        <components.ClearIndicator {...props}>
            <CloseIcon size={16}/>
        </components.ClearIndicator>
    );
}

function buildComboboxStyles<Option>(): StylesConfig<Option, false> {
    return {
        control: (base, state) => ({
            ...base,
            minHeight: '35px',
            height: '35px',
            alignItems: 'center',
            borderRadius: '4px',
            backgroundColor: 'var(--center-channel-bg)',
            borderColor: state.isFocused ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.16)',
            boxShadow: state.isFocused ? 'none' : '0px 1px 1px rgba(0, 0, 0, 0.075) inset',
            cursor: 'pointer',
            '&:hover': {
                borderColor: state.isFocused ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.16)',
            },
        }),
        valueContainer: (base) => ({
            ...base,
            padding: '0 8px',
        }),
        singleValue: (base) => ({
            ...base,
            color: 'var(--center-channel-color)',
        }),
        placeholder: (base) => ({
            ...base,
            color: 'rgba(var(--center-channel-color-rgb), 0.48)',
        }),
        input: (base) => ({
            ...base,
            margin: '0',
            padding: '0',
            color: 'var(--center-channel-color)',
        }),
        indicatorSeparator: () => ({
            display: 'none',
        }),
        clearIndicator: (base) => ({
            ...base,
            padding: '0 4px',
            color: 'rgba(var(--center-channel-color-rgb), 0.56)',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            '&:hover': {
                color: 'rgba(var(--center-channel-color-rgb), 0.72)',
            },
        }),
        dropdownIndicator: (base) => ({
            ...base,
            padding: '0 8px',
            color: 'rgba(var(--center-channel-color-rgb), 0.56)',
            display: 'flex',
            alignItems: 'center',
            '&:hover': {
                color: 'rgba(var(--center-channel-color-rgb), 0.72)',
            },
        }),
        menu: (base) => ({
            ...base,
            zIndex: 9999,
            backgroundColor: 'var(--center-channel-bg)',
            border: '1px solid rgba(var(--center-channel-color-rgb), 0.16)',
            borderRadius: '4px',
            boxShadow: '0 4px 12px rgba(0, 0, 0, 0.15)',
        }),
        menuPortal: (base) => ({
            ...base,
            zIndex: 10000,
        }),
        option: (base, state) => {
            let backgroundColor = 'transparent';
            if (state.isSelected) {
                backgroundColor = 'rgba(var(--center-channel-color-rgb), 0.12)';
            } else if (state.isFocused) {
                backgroundColor = 'rgba(var(--center-channel-color-rgb), 0.08)';
            }
            return {
                ...base,
                backgroundColor,
                color: 'var(--center-channel-color)',
            };
        },
    };
}

export const ComboboxItem = (props: ComboboxItemProps) => {
    const intl = useIntl();

    // Convert ComboboxOption[] to SelectOption[] for react-select
    const selectOptions: SelectOption[] = props.options.map((opt) => ({
        value: opt.id,
        label: opt.displayName,
    }));

    // Find current selection or create custom option
    const currentValue: SelectOption | null = props.value ? selectOptions.find((opt) => opt.value === props.value) || {value: props.value, label: props.value} : null;

    const handleChange = (newValue: SingleValue<SelectOption>) => {
        props.onChange(newValue?.value ?? '');
    };

    const selectStyles = buildComboboxStyles<SelectOption>();

    return (
        <FormRow>
            <ComboboxPortalStyles/>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                <FieldControlRow>
                    <CreatableSelect<SelectOption, false>
                        classNamePrefix='SystemConsoleCombobox'
                        value={currentValue}
                        onChange={handleChange}
                        options={selectOptions}
                        placeholder={props.placeholder || props.label}
                        styles={selectStyles}
                        components={{
                            DropdownIndicator: ComboboxDropdownIndicator,
                            ClearIndicator: ComboboxClearIndicator,
                        }}
                        isClearable={props.isClearable ?? true}
                        formatCreateLabel={(inputValue: string) => intl.formatMessage(
                            {defaultMessage: 'Use custom model: {modelName}'},
                            {modelName: inputValue},
                        )}
                        menuPortalTarget={getPortalTarget()}
                        menuPosition='fixed'
                    />
                </FieldControlRow>
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </FormRow>
    );
};

export const ItemLabel = styled.label<{$multiline?: boolean}>`
	font-size: 14px;
	font-weight: 600;
	line-height: 20px;
	margin: 0;
	padding: 0;
	box-sizing: border-box;
	display: flex;
	align-items: center;
	height: 35px;
	flex-shrink: 0;

	${({$multiline}) => $multiline && `
		align-items: flex-start;
		padding-top: 7px;
		height: auto;
		min-height: 35px;
	`}
`;

export const ReadOnlyTag = styled.span`
	padding: 2px 8px;
	border-radius: 4px;
	background: rgba(var(--center-channel-color-rgb), 0.08);
	color: rgba(var(--center-channel-color-rgb), 0.64);
	font-size: 11px;
	font-weight: 600;
	line-height: 16px;
	white-space: nowrap;
	flex-shrink: 0;
`;

export const ItemLabelRow = styled.div<{$multiline?: boolean}>`
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 8px;
	min-width: 0;
	height: 35px;
	flex-shrink: 0;
	box-sizing: border-box;

	${({$multiline}) => $multiline && `
		align-items: flex-start;
		padding-top: 7px;
		height: auto;
		min-height: 35px;
	`}
`;

type ItemLabelWithTagProps = {
    label: React.ReactNode;
    readOnly?: boolean;
    $multiline?: boolean;
};

export const ItemLabelWithTag = (props: ItemLabelWithTagProps) => {
    return (
        <ItemLabelRow $multiline={props.$multiline}>
            <ItemLabelText>{props.label}</ItemLabelText>
            {props.readOnly &&
            <ReadOnlyTag>
                <FormattedMessage defaultMessage='Read only'/>
            </ReadOnlyTag>
            }
        </ItemLabelRow>
    );
};

const ItemLabelText = styled.span`
	font-size: 14px;
	font-weight: 600;
	line-height: 20px;
`;

export const TextFieldContainer = styled.div`
	display: flex;
	flex-direction: column;
	gap: 8px;
`;

export const HelpText = styled.div`
	font-size: 12px;
	font-weight: 400;
	line-height: 16px;
	color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const SelectFieldWrapper = styled.div<{$maxWidth?: string}>`
	position: relative;
	width: 100%;
	max-width: ${({$maxWidth}) => $maxWidth || 'none'};
`;

const SelectChevron = styled.span`
	position: absolute;
	top: 50%;
	right: 12px;
	transform: translateY(-50%);
	display: flex;
	align-items: center;
	justify-content: center;
	color: rgba(var(--center-channel-color-rgb), 0.56);
	pointer-events: none;
`;

export const StyledSelect = styled.select`
	appearance: none;
	width: 100%;
	padding: 7px 36px 7px 12px;
	height: 35px;
	border-radius: 4px;
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
	box-shadow: 0px 1px 1px rgba(0, 0, 0, 0.075) inset;
	background: var(--center-channel-bg);
	color: var(--center-channel-color);
	font-size: 14px;
	font-weight: 400;
	line-height: 20px;
	cursor: pointer;

	&:focus {
		border-color: var(--button-bg);
		outline: none;
		box-shadow: none;
	}

	&:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}
`;

type SelectFieldProps = {
    value: string;
    onChange: (e: React.ChangeEvent<HTMLSelectElement>) => void;
    disabled?: boolean;
    maxWidth?: string;
    children: React.ReactNode;
};

export const SelectField = (props: SelectFieldProps) => {
    return (
        <SelectFieldWrapper $maxWidth={props.maxWidth}>
            <StyledSelect
                value={props.value}
                onChange={props.onChange}
                disabled={props.disabled}
            >
                {props.children}
            </StyledSelect>
            <SelectChevron aria-hidden='true'>
                <ChevronDownIcon size={16}/>
            </SelectChevron>
        </SelectFieldWrapper>
    );
};

export const StyledInput = styled.input<{ as?: string }>`
	appearance: none;
	display: flex;
	padding: 7px 12px;
	align-items: flex-start;
	border-radius: 2px;
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
	box-shadow: 0px 1px 1px rgba(0, 0, 0, 0.075) inset;
	height: 35px;
	background: var(--center-channel-bg);
	color: var(--center-channel-color);
	width: 100%;

	font-size: 14px;
	font-weight: 400;
	line-height: 20px;

	&::placeholder {
		color: rgba(var(--center-channel-color-rgb), 0.48);
	}

	${(props) => props.as === 'textarea' && `
		resize: vertical;
		height: 120px;
	`}

	&:focus {
		border-color: var(--button-bg);
		outline: none;
		box-shadow: none;
	}

	&:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}
`;

export const StyledRadio = styled.input`
	appearance: none;
	display: grid;
	color: rgba(var(--center-channel-color-rgb), 0.24);
	width: 1.6rem;
	height: 1.6rem;
	min-width: 1.6rem;
	flex-shrink: 0;
	border: 1px solid rgba(var(--center-channel-color-rgb),0.24);
	border-radius: 50%;
	margin: 0;
	cursor: pointer;
	place-content: center;

	&:checked {
		border-color: var(--button-bg);
		&:before {
			transform: scale(1);
		}
	}

	&:before {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--button-bg);
		content: '';
		transform: scale(0);
		transform-origin: center center;
		transition: 200ms transform ease-in-out;
	}
`;

export const StyledCheckbox = styled.input`
	appearance: none;
	width: 16px;
	height: 16px;
	min-width: 16px;
	min-height: 16px;
	margin: 0;
	flex-shrink: 0;
	cursor: pointer;
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.24);
	border-radius: 2px;
	background: var(--center-channel-bg);
	display: grid;
	place-content: center;

	&:checked {
		background: var(--button-bg);
		border-color: var(--button-bg);
	}

	&:checked::before {
		content: '';
		width: 4px;
		height: 8px;
		border: solid var(--button-color, #fff);
		border-width: 0 2px 2px 0;
		transform: rotate(45deg);
		margin-top: -2px;
	}

	&:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}
`;

const CheckboxControlLabel = styled.label`
	display: inline-flex;
	align-items: center;
	gap: 8px;
	cursor: pointer;
	font-size: 14px;
	font-weight: 400;
	line-height: 20px;
`;

const CheckboxControlText = styled.span`
	position: relative;
	top: 1px;
	color: var(--center-channel-color);
`;

export type InlineCheckboxProps = {
    label: string;
    checked: boolean;
    onChange: (checked: boolean) => void;
    testId?: string;
    inputAriaLabel?: string;
    disabled?: boolean;
};

export const InlineCheckbox = (props: InlineCheckboxProps) => {
    return (
        <CheckboxControlLabel data-testid={props.testId}>
            <StyledCheckbox
                type='checkbox'
                checked={props.checked}
                disabled={props.disabled}
                aria-label={props.inputAriaLabel}
                onChange={(e) => props.onChange(e.target.checked)}
            />
            <CheckboxControlText>{props.label}</CheckboxControlText>
        </CheckboxControlLabel>
    );
};

type BooleanItemProps = {
    label: React.ReactNode
    value: boolean
    onChange: (to: boolean) => void
    helpText?: string
};

export const BooleanItem = (props: BooleanItemProps) => {
    return (
        <FormRow>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                <FieldControlRow>
                    <StyledRadio
                        type='radio'
                        value='true'
                        checked={props.value}
                        onChange={() => props.onChange(true)}
                    />
                    <FormattedMessage defaultMessage='true'/>
                    <StyledRadio
                        type='radio'
                        value='false'
                        checked={!props.value}
                        onChange={() => props.onChange(false)}
                    />
                    <FormattedMessage defaultMessage='false'/>
                </FieldControlRow>
                {props.helpText &&
                <HelpText>{props.helpText}</HelpText>
                }
            </TextFieldContainer>
        </FormRow>
    );
};
