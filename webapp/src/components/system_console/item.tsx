// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useId} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Checkbox} from '@mattermost/compass-ui/components/checkbox';
import {Combobox} from '@mattermost/compass-ui/components/combobox';
import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';
import {Radio} from '@mattermost/compass-ui/components/radio';
import {Select} from '@mattermost/compass-ui/components/select';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {TextArea} from '@mattermost/compass-ui/components/text-area';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {getPortalTarget} from '../../utils/dom';

// Portaled menus must stack above the agent config modal overlay (z-index 2000).
export const PORTALED_MENU_Z_INDEX = 10000;

// Matches the compass-ui medium field height so side labels line up with controls.
const FIELD_HEIGHT = '40px';

export const ItemList = styled.div`
	display: flex;
	flex-direction: column;
	gap: var(--spacing-xxl);
`;

export const FormRow = styled.div`
	display: grid;
	grid-template-columns: minmax(auto, 275px) 1fr;
	grid-column-gap: var(--spacing-l);
	align-items: start;
`;

export const FieldControlRow = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	min-height: ${FIELD_HEIGHT};
	gap: var(--spacing-xs);
	width: 100%;

	> div {
		width: 100%;
	}
`;

// FieldExtra keeps adornments such as the license chip at their natural width
// instead of the row's full-width rule for direct div children.
const FieldExtra = styled.span`
	display: inline-flex;
	flex: 0 0 auto;
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
    const id = useId();
    const label = props.readOnly ? (
        <ItemLabelWithTag
            htmlFor={id}
            label={props.label}
            readOnly={true}
            $multiline={props.multiline}
        />
    ) : (
        <ItemLabel
            htmlFor={id}
            $multiline={props.multiline}
        >
            {props.label}
        </ItemLabel>
    );

    const sharedProps = {
        id,
        value: props.value,
        placeholder: props.placeholder ? props.placeholder : props.label,
        maxLength: props.maxLength,
        disabled: props.disabled,
        readOnly: props.readOnly,
        invalid: Boolean(props.error),
    };

    return (
        <FormRow>
            {label}
            <TextFieldContainer>
                <FieldControlRow>
                    {props.multiline ? (

                        // Callers only read target.value, which textarea events share with input events.
                        <TextArea
                            {...sharedProps}
                            onChange={(e) => props.onChange(e as unknown as React.ChangeEvent<HTMLInputElement>)}
                            onBlur={(e) => props.onBlur?.(e as unknown as React.FocusEvent<HTMLInputElement>)}
                            onFocus={(e) => props.onFocus?.(e as unknown as React.FocusEvent<HTMLInputElement>)}
                        />
                    ) : (
                        <TextInput
                            {...sharedProps}
                            onChange={props.onChange}
                            onBlur={props.onBlur}
                            onFocus={props.onFocus}
                            type={props.type ? props.type : 'text'}
                            step={props.step}
                            min={props.min}
                            max={props.max}
                        />
                    )}
                </FieldControlRow>
                {props.error && <ErrorMessage message={props.error}/>}
                {props.helptext &&
                <HelpText>{props.helptext}</HelpText>
                }
            </TextFieldContainer>
        </FormRow>
    );
};

export type SelectionOption = {
    value: string;
    label: string;
    disabled?: boolean;
};

// compass-ui Select hides empty-value options from its menu and treats '' as
// "nothing selected". Settings that offer an explicit '' option (e.g. "No
// fallback") carry it through the menu as a sentinel; otherwise '' shows the
// placeholder.
const EMPTY_OPTION_VALUE = '__none__';
const toSelectValue = (value: string) => (value === '' ? EMPTY_OPTION_VALUE : value);
const fromSelectValue = (value: string) => (value === EMPTY_OPTION_VALUE ? '' : value);

type SelectFieldProps = {
    id?: string;
    value: string;
    options: SelectionOption[];
    onChange: (value: string) => void;
    disabled?: boolean;
    invalid?: boolean;
    maxWidth?: string;
    ariaLabel?: string;
    placeholder?: string;
};

export const SelectField = (props: SelectFieldProps) => {
    const hasEmptyOption = props.options.some((option) => option.value === '');
    return (
        <SelectFieldWrapper $maxWidth={props.maxWidth}>
            <Select
                id={props.id}
                value={hasEmptyOption ? toSelectValue(props.value) : props.value}
                options={props.options.map((option) => ({...option, value: toSelectValue(option.value)}))}
                placeholder={props.placeholder}
                onChange={(value) => props.onChange(fromSelectValue(value))}
                disabled={props.disabled}
                invalid={props.invalid}
                aria-label={props.ariaLabel}
                listboxLabel={props.ariaLabel ?? props.placeholder}
                portalContainer={getPortalTarget()}
                zIndex={PORTALED_MENU_Z_INDEX}
            />
        </SelectFieldWrapper>
    );
};

export type SelectionItemProps = {
    label: string
    value: string
    options: SelectionOption[]
    onChange: (value: string) => void
    helptext?: string
    disabled?: boolean
    error?: string
    extra?: React.ReactNode
    placeholder?: string
};

export const SelectionItem = (props: SelectionItemProps) => {
    const id = useId();
    return (
        <FormRow>
            <ItemLabel htmlFor={id}>{props.label}</ItemLabel>
            <TextFieldContainer>
                <FieldControlRow>
                    <SelectField
                        id={id}
                        value={props.value}
                        options={props.options}
                        onChange={props.onChange}
                        disabled={props.disabled}
                        invalid={Boolean(props.error)}
                        placeholder={props.placeholder ?? props.label}
                    />
                    {props.extra && <FieldExtra>{props.extra}</FieldExtra>}
                </FieldControlRow>
                {props.error && <ErrorMessage message={props.error}/>}
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

export const ComboboxItem = (props: ComboboxItemProps) => {
    const intl = useIntl();
    const id = useId();

    return (
        <FormRow>
            <ItemLabel htmlFor={id}>{props.label}</ItemLabel>
            <TextFieldContainer>
                <FieldControlRow>
                    <Combobox
                        id={id}
                        value={props.value || null}
                        options={props.options.map((opt) => ({value: opt.id, label: opt.displayName}))}
                        onChange={(value) => props.onChange(typeof value === 'string' ? value : '')}
                        creatable={true}
                        onCreateOption={props.onChange}
                        formatCreateLabel={(inputValue: string) => intl.formatMessage(
                            {defaultMessage: 'Use custom model: {modelName}'},
                            {modelName: inputValue},
                        )}
                        placeholder={props.placeholder || props.label}
                        clearable={props.isClearable ?? true}
                        clearLabel={intl.formatMessage({defaultMessage: 'Clear {label}'}, {label: props.label})}
                        listboxLabel={props.label}
                        portalContainer={getPortalTarget()}
                        zIndex={PORTALED_MENU_Z_INDEX}
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
	font-size: var(--font-size-100);
	font-weight: var(--font-weight-semibold);
	line-height: var(--line-height-100);
	margin: 0;
	padding: 0;
	box-sizing: border-box;
	display: flex;
	align-items: center;
	min-height: ${FIELD_HEIGHT};
	height: auto;
	flex-shrink: 0;

	${({$multiline}) => $multiline && `
		align-items: flex-start;
		padding-top: 10px;
	`}
`;

export const ItemLabelRow = styled.div<{$multiline?: boolean}>`
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: var(--spacing-xs);
	min-width: 0;
	height: ${FIELD_HEIGHT};
	flex-shrink: 0;
	box-sizing: border-box;

	${({$multiline}) => $multiline && `
		align-items: flex-start;
		padding-top: 10px;
		height: auto;
		min-height: ${FIELD_HEIGHT};
	`}
`;

type ItemLabelWithTagProps = {
    label: React.ReactNode;
    htmlFor?: string;
    readOnly?: boolean;
    $multiline?: boolean;
};

export const ItemLabelWithTag = (props: ItemLabelWithTagProps) => {
    return (
        <ItemLabelRow $multiline={props.$multiline}>
            <ItemLabelText htmlFor={props.htmlFor}>{props.label}</ItemLabelText>
            {props.readOnly &&
            <Tag label={<FormattedMessage defaultMessage='Read only'/>}/>
            }
        </ItemLabelRow>
    );
};

const ItemLabelText = styled.label`
	margin: 0;
	font-size: var(--font-size-100);
	font-weight: var(--font-weight-semibold);
	line-height: var(--line-height-100);
`;

export const TextFieldContainer = styled.div`
	display: flex;
	flex-direction: column;
	gap: var(--spacing-xs);
`;

export const HelpText = styled.div`
	font-size: var(--font-size-75);
	font-weight: var(--font-weight-regular);
	line-height: var(--line-height-75);
	color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const SelectFieldWrapper = styled.div<{$maxWidth?: string}>`
	width: 100%;
	max-width: ${({$maxWidth}) => $maxWidth || 'none'};
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
        <Checkbox
            data-testid={props.testId}
            checked={props.checked}
            disabled={props.disabled}
            aria-label={props.inputAriaLabel}
            onChange={(e) => props.onChange(e.target.checked)}
        >
            {props.label}
        </Checkbox>
    );
};

type BooleanItemProps = {
    label: React.ReactNode
    value: boolean
    onChange: (to: boolean) => void
    helpText?: string
    disabled?: boolean

    // When true, the "true" radio is disabled so the setting cannot be turned
    // on, but turning it off remains possible.
    disableTrue?: boolean
    extra?: React.ReactNode
};

export const BooleanItem = (props: BooleanItemProps) => {
    const name = useId();
    const labelId = useId();
    return (
        <FormRow>
            <CompactItemLabel id={labelId}>{props.label}</CompactItemLabel>
            <TextFieldContainer>
                <CompactFieldControlRow>
                    <BooleanRadioGroup
                        role='radiogroup'
                        aria-labelledby={labelId}
                    >
                        <InlineRadio
                            name={name}
                            value='true'
                            checked={props.value}
                            disabled={props.disabled || props.disableTrue}
                            onChange={() => props.onChange(true)}
                        >
                            <FormattedMessage defaultMessage='True'/>
                        </InlineRadio>
                        <InlineRadio
                            name={name}
                            value='false'
                            checked={!props.value}
                            disabled={props.disabled}
                            onChange={() => props.onChange(false)}
                        >
                            <FormattedMessage defaultMessage='False'/>
                        </InlineRadio>
                    </BooleanRadioGroup>
                    {props.extra && <FieldExtra>{props.extra}</FieldExtra>}
                </CompactFieldControlRow>
                {props.helpText &&
                <HelpText>{props.helpText}</HelpText>
                }
            </TextFieldContainer>
        </FormRow>
    );
};

// Match checkbox/radio control height instead of the taller text-field label box.
export const CompactItemLabel = styled(ItemLabel)`
	min-height: 0;
	align-items: flex-start;
	line-height: var(--line-height-100, 20px);
`;

// Drop the 40px text-input min-height so help text sits under short controls.
export const CompactFieldControlRow = styled(FieldControlRow)`
	min-height: 0;
	align-items: flex-start;
`;

// compass-ui Radio fills its row; True/False sit side by side.
const BooleanRadioGroup = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	gap: var(--spacing-xl);

	&& {
		width: auto;
	}
`;

const InlineRadio = styled(Radio)`
	&& {
		width: auto;
	}
`;
