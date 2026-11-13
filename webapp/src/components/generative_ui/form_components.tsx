// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useId, useMemo, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';
import {
    FormNameContext,
    useFormName,
    useGetFieldValue,
    useIsStreaming,
    useSetDefaultValue,
    useSetFieldValue,
} from '@openuidev/react-lang';
import {CheckIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Checkbox} from '@mattermost/compass-ui/components/checkbox';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {Radio} from '@mattermost/compass-ui/components/radio';
import {Select} from '@mattermost/compass-ui/components/select';
import {TextArea} from '@mattermost/compass-ui/components/text-area';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {
    FormFields,
    FormFieldsContext,
    RegisteredField,
    SubmissionField,
    SubmissionValue,
    useBlockInteraction,
    useFormFields,
} from './actions';

// The server refuses longer values.
const MAX_VALUE_LENGTH = 2000;

const isEmpty = (value: SubmissionValue) => value === '' || value === false;

const requiredMessage = (
    <FormattedMessage
        id='ai.generative_ui.field_required'
        defaultMessage='Required'
    />
);

/**
 * Binds one field to its form's state: the agent's default once streaming
 * ends, the user's edits after that, and its registration with the Form so a
 * button can submit it.
 */
function useFormField<T extends SubmissionValue>(componentType: string, name: string, label: string, required: boolean, defaultValue: T) {
    const formName = useFormName();
    const getFieldValue = useGetFieldValue();
    const setFieldValue = useSetFieldValue();
    const stored = getFieldValue(formName, name) as T | undefined;
    useSetDefaultValue({formName, componentType, name, existingValue: stored, defaultValue});
    const value = stored ?? defaultValue;

    const valueRef = useRef<T>(value);
    valueRef.current = value;
    const fields = useFormFields();
    useEffect(
        () => fields?.register(name, {label, required, getValue: () => valueRef.current}),
        [fields, name, label, required],
    );

    const interaction = useBlockInteraction();
    const isStreaming = useIsStreaming();
    const onChange = (next: T) => {
        fields?.clearError(name);
        setFieldValue(formName, componentType, name, next, false);
    };

    return {
        value,
        onChange,
        disabled: interaction.disabled || isStreaming,
        invalid: fields?.errors.has(name) ?? false,
    };
}

interface FormProps {
    name: string;
    fields: React.ReactNode;
    buttons: React.ReactNode;
}

export const FormView = ({name, fields, buttons}: FormProps) => {
    const registered = useRef(new Map<string, RegisteredField>());
    const [errors, setErrors] = useState<ReadonlySet<string>>(new Set());

    const register = useCallback((fieldName: string, field: RegisteredField) => {
        registered.current.set(fieldName, field);
        return () => {
            if (registered.current.get(fieldName) === field) {
                registered.current.delete(fieldName);
            }
        };
    }, []);

    const clearError = useCallback((fieldName: string) => {
        setErrors((prev) => {
            if (!prev.has(fieldName)) {
                return prev;
            }
            const next = new Set(prev);
            next.delete(fieldName);
            return next;
        });
    }, []);

    const collect = useCallback(() => {
        const missing = new Set<string>();
        const values: SubmissionField[] = [];
        for (const [fieldName, field] of registered.current) {
            const value = field.getValue() ?? '';
            if (field.required && isEmpty(value)) {
                missing.add(fieldName);
            }
            values.push({name: fieldName, label: field.label, value});
        }
        setErrors(missing);
        return missing.size > 0 ? null : values;
    }, []);

    const context = useMemo<FormFields>(() => ({register, clearError, errors, collect}), [register, clearError, errors, collect]);

    return (
        <FormNameContext.Provider value={name}>
            <FormFieldsContext.Provider value={context}>
                <FormElement
                    data-testid='generative-ui-form'
                    noValidate={true}
                    onSubmit={(event) => event.preventDefault()}
                >
                    <FieldStack>{fields}</FieldStack>
                    <ButtonRow>{buttons}</ButtonRow>
                    <FormNotice/>
                </FormElement>
            </FormFieldsContext.Provider>
        </FormNameContext.Provider>
    );
};

const FormNotice = () => {
    const {notice, requesterUsername} = useBlockInteraction();
    switch (notice) {
    case 'submitted':
        return (
            <NoticeText>
                <FormattedMessage
                    id='ai.generative_ui.form_submitted'
                    defaultMessage='Submitted'
                />
            </NoticeText>
        );
    case 'stale':
        return (
            <NoticeText>
                <FormattedMessage
                    id='ai.generative_ui.form_inactive'
                    defaultMessage='This form is no longer active'
                />
            </NoticeText>
        );
    case 'onlooker':
        return (
            <NoticeText>
                {requesterUsername ? (
                    <FormattedMessage
                        id='ai.generative_ui.form_requester_only'
                        defaultMessage='Only @{username} can respond'
                        values={{username: requesterUsername}}
                    />
                ) : (
                    <FormattedMessage
                        id='ai.generative_ui.form_requester_only_unknown'
                        defaultMessage='Only the requester can respond'
                    />
                )}
            </NoticeText>
        );
    case null:
        return null;
    default: {
        const unhandled: never = notice;
        return unhandled;
    }
    }
};

interface TextFieldProps {
    name: string;
    label: string;
    placeholder?: string;
    defaultValue?: string;
    required?: boolean;
}

export const TextInputView = ({name, label, placeholder, defaultValue, required}: TextFieldProps) => {
    const field = useFormField('TextInput', name, label, Boolean(required), defaultValue ?? '');
    return (
        <TextInput
            name={name}
            label={label}
            placeholder={placeholder}
            value={field.value}
            maxLength={MAX_VALUE_LENGTH}
            required={required}
            disabled={field.disabled}
            invalid={field.invalid}
            message={field.invalid ? requiredMessage : undefined} // eslint-disable-line no-undefined
            onChange={(event) => field.onChange(event.target.value)}
        />
    );
};

export const TextAreaView = ({name, label, placeholder, defaultValue, required}: TextFieldProps) => {
    const field = useFormField('TextArea', name, label, Boolean(required), defaultValue ?? '');
    return (
        <TextArea
            name={name}
            label={label}
            placeholder={placeholder}
            value={field.value}
            maxLength={MAX_VALUE_LENGTH}
            required={required}
            disabled={field.disabled}
            invalid={field.invalid}
            message={field.invalid ? requiredMessage : undefined} // eslint-disable-line no-undefined
            onChange={(event) => field.onChange(event.target.value)}
        />
    );
};

interface ChoiceProps {
    name: string;
    label: string;
    options: string[];
    defaultValue?: string;
    required?: boolean;
}

// A default that is not one of the options would be submitted without being
// shown as selected, so a choice starts unselected instead.
const shownDefault = (options: string[], defaultValue = '') => (options.includes(defaultValue) ? defaultValue : '');

export const SelectView = ({name, label, options, defaultValue, required}: ChoiceProps) => {
    const field = useFormField('Select', name, label, Boolean(required), shownDefault(options, defaultValue));
    return (
        <FieldWithMessage>
            <Select
                name={name}
                label={label}
                options={options.map((option) => ({value: option, label: option}))}
                value={field.value}
                disabled={field.disabled}
                invalid={field.invalid}
                onChange={field.onChange}
            />
            {field.invalid && <ErrorText>{requiredMessage}</ErrorText>}
        </FieldWithMessage>
    );
};

export const RadioGroupView = ({name, label, options, defaultValue, required}: ChoiceProps) => {
    const field = useFormField('RadioGroup', name, label, Boolean(required), shownDefault(options, defaultValue));
    const groupName = `${useId()}-${name}`;
    return (
        <Fieldset disabled={field.disabled}>
            <Legend>{label}</Legend>
            {options.map((option) => (
                <Radio
                    key={option}
                    name={groupName}
                    value={option}
                    checked={field.value === option}
                    invalid={field.invalid}
                    onChange={() => field.onChange(option)}
                >
                    {option}
                </Radio>
            ))}
            {field.invalid && <ErrorText>{requiredMessage}</ErrorText>}
        </Fieldset>
    );
};

interface CheckboxProps {
    name: string;
    label: string;
    defaultChecked?: boolean;
    required?: boolean;
}

export const CheckboxView = ({name, label, defaultChecked, required}: CheckboxProps) => {
    const field = useFormField('Checkbox', name, label, Boolean(required), Boolean(defaultChecked));
    return (
        <FieldWithMessage>
            <Checkbox
                name={name}
                checked={field.value}
                disabled={field.disabled}
                invalid={field.invalid}
                onChange={(event) => field.onChange(event.target.checked)}
            >
                {label}
            </Checkbox>
            {field.invalid && <ErrorText>{requiredMessage}</ErrorText>}
        </FieldWithMessage>
    );
};

export type ButtonVariant = 'primary' | 'secondary' | 'danger';

interface ButtonProps {
    label: string;
    variant?: ButtonVariant;
}

// The server stores a submitted label without control characters or
// surrounding whitespace (genui.Submission.Normalize), while a click not yet
// echoed back still carries the rendered label; compare both that way.
const storedLabel = (label: string) => label.replace(/\p{Cc}/gu, '').trim();

/**
 * Sends its own label, plus the values of its form when it is in one. A
 * button carries no other message or action, so what is submitted is always
 * what the user saw.
 */
export const ButtonView = ({label, variant}: ButtonProps) => {
    const formName = useFormName() ?? '';
    const fields = useFormFields();
    const interaction = useBlockInteraction();
    const isStreaming = useIsStreaming();
    const chosen = interaction.submitted !== null && interaction.submitted.form === formName && storedLabel(interaction.submitted.label) === storedLabel(label);

    const onClick = () => {
        const values = fields ? fields.collect() : [];
        if (values) {
            interaction.submit(formName, label, values);
        }
    };

    return (
        <Button
            type='button'
            size='small'
            emphasis={variant === 'secondary' ? 'tertiary' : 'primary'}
            destructive={variant === 'danger'}
            disabled={interaction.disabled || isStreaming}
            loading={interaction.submittingLabel === label}
            leadingIcon={chosen ? <Icon glyph={<CheckIcon/>}/> : undefined} // eslint-disable-line no-undefined
            aria-pressed={interaction.submitted ? chosen : undefined} // eslint-disable-line no-undefined
            onClick={onClick}
        >
            {label}
        </Button>
    );
};

const FormElement = styled.form`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
    margin: 0;
`;

const FieldStack = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-s);
`;

export const ButtonRow = styled.div`
    display: flex;
    flex-wrap: wrap;
    gap: var(--spacing-xs);
`;

const FieldWithMessage = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxs);
`;

const Fieldset = styled.fieldset`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxs);
    margin: 0;
    padding: 0;
    border: none;
    min-width: 0;
`;

const Legend = styled.legend`
    margin-bottom: var(--spacing-xxs);
    padding: 0;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const ErrorText = styled.span`
    font-size: 12px;
    line-height: 16px;
    color: var(--error-text);
`;

const NoticeText = styled.span`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;
