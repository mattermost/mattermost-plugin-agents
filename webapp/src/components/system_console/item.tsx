// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import CreatableSelect from 'react-select/creatable';
import {StylesConfig, SingleValue} from 'react-select';

export const ItemList = styled.div`
	display: grid;
	grid-template-columns: minmax(auto, 275px) 1fr;
	grid-column-gap: 16px;
	grid-row-gap: 24px;
`;

export type TextItemProps = {
    label: string,
    value: string,
    type?: string,
    helptext?: string,
    multiline?: boolean,
    placeholder?: string,
    maxLength?: number,
    step?: string,
    min?: string,
    max?: string,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => void,
    disabled?: boolean,
};

export const TextItem = (props: TextItemProps) => {
    return (
        <>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                <StyledInput
                    as={props.multiline ? 'textarea' : 'input'}
                    value={props.value}
                    type={props.type ? props.type : 'text'}
                    placeholder={props.placeholder ? props.placeholder : props.label}
                    onChange={props.onChange}
                    maxLength={props.maxLength}
                    step={props.step}
                    min={props.min}
                    max={props.max}
                    disabled={props.disabled}
                />
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </>
    );
};

export type SelectionItemProps = {
    label: string
    value: string
    onChange: (e: React.ChangeEvent<HTMLSelectElement>) => void
    children: React.ReactNode
    helptext?: string
    disabled?: boolean
};

export const SelectionItem = (props: SelectionItemProps) => {
    return (
        <>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                <StyledInput
                    as='select'
                    value={props.value}
                    onChange={props.onChange}
                    disabled={props.disabled}
                >
                    {props.children}
                </StyledInput>
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </>
    );
};

export const SelectionItemOption = styled.option`
`;

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
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => void
};

type SelectOption = {
    value: string
    label: string
}

export const ComboboxItem = (props: ComboboxItemProps) => {
    // Convert ComboboxOption[] to SelectOption[] for react-select
    const selectOptions: SelectOption[] = props.options.map((opt) => ({
        value: opt.id,
        label: opt.displayName,
    }));

    // Find current selection or create custom option
    const currentValue: SelectOption | null = props.value ? selectOptions.find((opt) => opt.value === props.value) || {value: props.value, label: props.value} : null;

    const handleChange = (newValue: SingleValue<SelectOption>) => {
        // Create a synthetic event to match the existing onChange signature
        const syntheticEvent = {
            target: {
                value: newValue?.value || '',
            },
        } as React.ChangeEvent<HTMLInputElement>;

        props.onChange(syntheticEvent);
    };

    const selectStyles: StylesConfig<SelectOption, false> = {
        control: (base, state) => ({
            ...base,
            minHeight: '35px',
            height: '35px',
            borderRadius: '2px',
            borderColor: state.isFocused ? '#66afe9' : 'rgba(var(--center-channel-color-rgb), 0.16)',
            boxShadow: state.isFocused ? 'inset 0 1px 1px rgba(0, 0, 0, 0.075), 0 0 8px rgba(102, 175, 233, 0.75)' : '0px 1px 1px rgba(0, 0, 0, 0.075) inset',
            '&:hover': {
                borderColor: state.isFocused ? '#66afe9' : 'rgba(var(--center-channel-color-rgb), 0.16)',
            },
        }),
        valueContainer: (base) => ({
            ...base,
            height: '35px',
            padding: '0 12px',
        }),
        input: (base) => ({
            ...base,
            margin: '0',
            padding: '0',
        }),
        indicatorSeparator: () => ({
            display: 'none',
        }),
        clearIndicator: (base) => ({
            ...base,
            padding: '4px',
            color: 'rgba(var(--center-channel-color-rgb), 0.56)',
            cursor: 'pointer',
            '&:hover': {
                color: 'rgba(var(--center-channel-color-rgb), 0.72)',
            },
        }),
        dropdownIndicator: (base) => ({
            ...base,
            padding: '4px',
            color: 'rgba(var(--center-channel-color-rgb), 0.56)',
            '&:hover': {
                color: 'rgba(var(--center-channel-color-rgb), 0.72)',
            },
        }),
        menu: (base) => ({
            ...base,
            zIndex: 9999,
        }),
    };

    return (
        <>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                <CreatableSelect<SelectOption, false>
                    value={currentValue}
                    onChange={handleChange}
                    options={selectOptions}
                    placeholder={props.placeholder || props.label}
                    styles={selectStyles}
                    isClearable={props.isClearable ?? true}
                    formatCreateLabel={(inputValue: string) => `Use custom model: ${inputValue}`}
                />
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </>
    );
};

export const ItemLabel = styled.label`
	font-size: 14px;
	font-weight: 600;
	line-height: 20px;
`;

const TextFieldContainer = styled.div`
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

export const StyledInput = styled.input<{ as?: string }>`
	appearance: none;
	display: flex;
	padding: 7px 12px;
	align-items: flex-start;
	border-radius: 2px;
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
	box-shadow: 0px 1px 1px rgba(0, 0, 0, 0.075) inset;
	height: 35px;
	background: white;

	font-size: 14px;
	font-weight: 400;
	line-height: 20px;

	${(props) => props.as === 'textarea' && `
		resize: vertical;
		height: 120px;
	`}

	&:focus {
		border-color: $66afe9;
		box-shadow: inset 0 1px 1px rgba(0, 0, 0, 0.075), 0 0 8px rgba(102, 175, 233, 0.75);
		outline: 0;
	}
`;

export const StyledRadio = styled.input`
	appearance: none;
	display: grid;
	color: rgba(var(--center-channel-color-rgb), 0.24);
	width: 1.6rem;
	height: 1.6rem;
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

type BooleanItemProps = {
    label: React.ReactNode
    value: boolean
    onChange: (to: boolean) => void
    helpText?: string
};

export const BooleanItem = (props: BooleanItemProps) => {
    return (
        <>
            <ItemLabel>{props.label}</ItemLabel>
            <TextFieldContainer>
                <BooleanItemRow>
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
                </BooleanItemRow>
                {props.helpText &&
                <HelpText>{props.helpText}</HelpText>
                }
            </TextFieldContainer>
        </>
    );
};

const BooleanItemRow = styled.div`
	display: flex;
	flex-direction: row;
	gap: 8px;
	align-items: center;
`;
