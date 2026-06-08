// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useRef, useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {CustomPrompt} from '@/types';
import Dropdown from '../dropdown';

import ContextVariablesDropdown from './context_variables_dropdown';

const FormLayout = styled.div<{$stickyFooter?: boolean}>`
    display: flex;
    flex-direction: column;
    background-color: var(--center-channel-bg);

    ${({$stickyFooter}) =>
        $stickyFooter &&
        `
        flex: 1;
        min-height: 0;
    `}
`;

const FormBody = styled.div<{$stickyFooter?: boolean}>`
    display: flex;
    flex-direction: column;
    gap: 24px;
    padding: 20px 32px 0;

    ${({$stickyFooter}) =>
        $stickyFooter && `
        flex: 1;
        min-height: 0;
        overflow-y: auto;
    `}
`;

const FormFooter = styled.div`
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
    padding: 16px 32px 24px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

/** Read-only and legacy single-block layout */
const FormContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 24px;
    padding: 20px 32px;
    background-color: var(--center-channel-bg);
`;

const FieldGroup = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
    position: relative;
`;

const FieldLabel = styled.label`
    position: absolute;
    top: -8px;
    left: 12px;
    background-color: var(--center-channel-bg);
    padding: 0 4px;
    font-size: 10px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    z-index: 1;
`;

const TextInput = styled.input`
    width: 100%;
    padding: 10px 16px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background-color: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    outline: none;

    &:focus {
        border-color: var(--button-bg);
        box-shadow: 0 0 0 1px var(--button-bg);
    }
`;

const TextArea = styled.textarea`
    width: 100%;
    padding: 10px 16px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background-color: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    outline: none;
    resize: vertical;
    min-height: 60px;

    &:focus {
        border-color: var(--button-bg);
        box-shadow: 0 0 0 1px var(--button-bg);
    }
`;

const SystemPromptTextArea = styled(TextArea)`
    min-height: 120px;
`;

const RadioGroup = styled.div`
    display: flex;
    align-items: center;
    gap: 16px;
`;

const RadioLabel = styled.label`
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);
    cursor: pointer;
`;

const RadioInput = styled.input`
    cursor: pointer;
`;

const PrivateNote = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 12px;
`;

const VisibilityLabel = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    margin-bottom: 4px;
`;

const SystemPromptHeader = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
`;

const SystemPromptLabel = styled.label`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ContextVariablesButton = styled.button`
    background: rgba(var(--center-channel-color-rgb), 0.08);
    color: var(--center-channel-color);
    border: none;
    border-radius: 4px;
    padding: 4px 10px;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.16);
    }
`;

const SaveButton = styled.button`
    background: var(--button-bg);
    color: var(--button-color);
    border: none;
    border-radius: 4px;
    padding: 10px 20px;
    font-weight: 600;
    font-size: 14px;
    cursor: pointer;
    font-family: 'Open Sans', sans-serif;

    &:hover {
        background: rgba(var(--button-bg-rgb), 0.88);
    }

    &:disabled {
        opacity: 0.5;
        cursor: not-allowed;
    }
`;

const DiscardButton = styled.button`
    background: none;
    color: var(--button-bg);
    border: none;
    border-radius: 4px;
    padding: 10px 20px;
    font-weight: 600;
    font-size: 14px;
    cursor: pointer;
    font-family: 'Open Sans', sans-serif;

    &:hover {
        background: rgba(var(--button-bg-rgb), 0.08);
    }

    &:disabled {
        opacity: 0.5;
        cursor: not-allowed;
    }
`;

const DeleteButton = styled.button`
    background: none;
    color: var(--error-text);
    border: none;
    border-radius: 4px;
    padding: 10px 20px;
    font-weight: 600;
    font-size: 14px;
    cursor: pointer;
    font-family: 'Open Sans', sans-serif;
    margin-right: auto;

    &:hover {
        background: rgba(var(--error-text-color-rgb), 0.08);
    }

    &:disabled {
        opacity: 0.5;
        cursor: not-allowed;
    }
`;

const ValidationError = styled.div`
    color: var(--error-text);
    font-size: 12px;
    line-height: 16px;
    margin-top: 2px;
`;

const ReadOnlyText = styled.div`
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);
    padding: 4px 0;
    white-space: pre-wrap;
`;

const ReadOnlyMuted = styled(ReadOnlyText)`
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

interface CustomPromptFormProps {
    prompt?: CustomPrompt;
    onSave: (data: {name: string; description: string; template: string; is_shared: boolean}) => void | Promise<void>;
    onDiscard: () => void;
    onDelete?: () => void;
    readOnly?: boolean;

    /** When true, actions sit in a footer bar and the body scrolls (new prompt modal). */
    stickyFooter?: boolean;
}

const CustomPromptForm = ({prompt, onSave, onDiscard, onDelete, readOnly, stickyFooter}: CustomPromptFormProps) => {
    const intl = useIntl();
    const [name, setName] = useState(prompt?.name ?? '');
    const [description, setDescription] = useState(prompt?.description ?? '');
    const [template, setTemplate] = useState(prompt?.template ?? '');
    const [isShared, setIsShared] = useState(prompt?.is_shared ?? false);
    const [showContextVars, setShowContextVars] = useState(false);
    const [errors, setErrors] = useState<{name?: boolean; template?: boolean}>({});
    const [isSaving, setIsSaving] = useState(false);
    const templateRef = useRef<HTMLTextAreaElement>(null);

    const handleSave = useCallback(async () => {
        if (isSaving) {
            return;
        }
        const newErrors: {name?: boolean; template?: boolean} = {};
        if (!name.trim()) {
            newErrors.name = true;
        }
        if (!template.trim()) {
            newErrors.template = true;
        }
        if (newErrors.name || newErrors.template) {
            setErrors(newErrors);
            return;
        }
        setErrors({});
        setIsSaving(true);
        try {
            await onSave({name: name.trim(), description: description.trim(), template: template.trim(), is_shared: isShared});
        } finally {
            setIsSaving(false);
        }
    }, [name, description, template, isShared, onSave, isSaving]);

    const handleInsertVariable = useCallback((variable: string) => {
        const textarea = templateRef.current;
        if (textarea) {
            const start = textarea.selectionStart;
            const end = textarea.selectionEnd;
            const newValue = template.substring(0, start) + variable + template.substring(end);
            setTemplate(newValue);

            // Set cursor position after inserted variable
            requestAnimationFrame(() => {
                textarea.focus();
                const newPos = start + variable.length;
                textarea.setSelectionRange(newPos, newPos);
            });
        } else {
            setTemplate(template + variable);
        }
        setShowContextVars(false);
    }, [template]);

    if (readOnly) {
        return (
            <FormContainer>
                <FieldGroup>
                    <VisibilityLabel>
                        <FormattedMessage defaultMessage='Visibility'/>
                    </VisibilityLabel>
                    <ReadOnlyText>
                        {prompt?.is_shared ? (
                            <FormattedMessage defaultMessage='Public'/>
                        ) : (
                            <FormattedMessage defaultMessage='Private'/>
                        )}
                    </ReadOnlyText>
                </FieldGroup>
                <FieldGroup>
                    <VisibilityLabel>
                        <FormattedMessage defaultMessage='Action Title'/>
                    </VisibilityLabel>
                    <ReadOnlyText>{prompt?.name}</ReadOnlyText>
                </FieldGroup>
                {prompt?.description && (
                    <FieldGroup>
                        <VisibilityLabel>
                            <FormattedMessage defaultMessage='Brief Description'/>
                        </VisibilityLabel>
                        <ReadOnlyMuted>{prompt.description}</ReadOnlyMuted>
                    </FieldGroup>
                )}
                <FieldGroup>
                    <VisibilityLabel>
                        <FormattedMessage defaultMessage='System Prompt'/>
                    </VisibilityLabel>
                    <ReadOnlyText>{prompt?.template}</ReadOnlyText>
                </FieldGroup>
            </FormContainer>
        );
    }

    const actions = (
        <>
            {onDelete && (
                <DeleteButton
                    type='button'
                    onClick={onDelete}
                    disabled={isSaving}
                >
                    <FormattedMessage defaultMessage='Delete'/>
                </DeleteButton>
            )}
            <DiscardButton
                type='button'
                onClick={onDiscard}
                disabled={isSaving}
            >
                <FormattedMessage defaultMessage='Discard'/>
            </DiscardButton>
            <SaveButton
                type='button'
                onClick={handleSave}
                disabled={isSaving}
            >
                <FormattedMessage defaultMessage='Save'/>
            </SaveButton>
        </>
    );

    return (
        <FormLayout $stickyFooter={stickyFooter}>
            <FormBody $stickyFooter={stickyFooter}>
                <FieldGroup>
                    <VisibilityLabel>
                        <FormattedMessage defaultMessage='Visibility'/>
                    </VisibilityLabel>
                    <RadioGroup>
                        <RadioLabel>
                            <RadioInput
                                type='radio'
                                name={`visibility-${prompt?.id ?? 'new'}`}
                                checked={isShared}
                                onChange={() => setIsShared(true)}
                            />
                            <FormattedMessage defaultMessage='Public'/>
                        </RadioLabel>
                        <RadioLabel>
                            <RadioInput
                                type='radio'
                                name={`visibility-${prompt?.id ?? 'new'}`}
                                checked={!isShared}
                                onChange={() => setIsShared(false)}
                            />
                            <FormattedMessage defaultMessage='Private'/>
                            <PrivateNote>
                                <FormattedMessage defaultMessage='(only you)'/>
                            </PrivateNote>
                        </RadioLabel>
                    </RadioGroup>
                </FieldGroup>
                <FieldGroup>
                    <FieldLabel htmlFor={`prompt-name-${prompt?.id ?? 'new'}`}>
                        <FormattedMessage defaultMessage='Action Title'/>
                    </FieldLabel>
                    <TextInput
                        id={`prompt-name-${prompt?.id ?? 'new'}`}
                        value={name}
                        maxLength={64}
                        onChange={(e) => {
                            setName(e.target.value);
                            if (errors.name) {
                                setErrors((prev) => ({...prev, name: false}));
                            }
                        }}
                        placeholder={intl.formatMessage({defaultMessage: 'Enter a title for your prompt'})}
                    />
                    {errors.name && (
                        <ValidationError>
                            <FormattedMessage defaultMessage='Action title is required'/>
                        </ValidationError>
                    )}
                </FieldGroup>
                <FieldGroup>
                    <FieldLabel htmlFor={`prompt-description-${prompt?.id ?? 'new'}`}>
                        <FormattedMessage defaultMessage='Brief Description'/>
                    </FieldLabel>
                    <TextArea
                        id={`prompt-description-${prompt?.id ?? 'new'}`}
                        value={description}
                        onChange={(e) => setDescription(e.target.value)}
                        placeholder={intl.formatMessage({defaultMessage: 'Enter a brief description'})}
                    />
                </FieldGroup>
                <FieldGroup>
                    <SystemPromptHeader>
                        <SystemPromptLabel htmlFor={`prompt-template-${prompt?.id ?? 'new'}`}>
                            <FormattedMessage defaultMessage='System Prompt'/>
                        </SystemPromptLabel>
                        <Dropdown
                            target={
                                <ContextVariablesButton
                                    type='button'
                                    onClick={() => setShowContextVars(!showContextVars)}
                                    aria-label={intl.formatMessage({defaultMessage: 'Insert context variable'})}
                                >
                                    <FormattedMessage defaultMessage='Context Variables'/>
                                </ContextVariablesButton>
                            }
                            isOpen={showContextVars}
                            onOpenChange={setShowContextVars}
                            placement='bottom-end'
                        >
                            <ContextVariablesDropdown
                                onSelect={handleInsertVariable}
                            />
                        </Dropdown>
                    </SystemPromptHeader>
                    <SystemPromptTextArea
                        id={`prompt-template-${prompt?.id ?? 'new'}`}
                        ref={templateRef}
                        value={template}
                        onChange={(e) => {
                            setTemplate(e.target.value);
                            if (errors.template) {
                                setErrors((prev) => ({...prev, template: false}));
                            }
                        }}
                        placeholder={intl.formatMessage({defaultMessage: 'Enter the system prompt template'})}
                    />
                    {errors.template && (
                        <ValidationError>
                            <FormattedMessage defaultMessage='System prompt is required'/>
                        </ValidationError>
                    )}
                </FieldGroup>
            </FormBody>
            <FormFooter>{actions}</FormFooter>
        </FormLayout>
    );
};

export default CustomPromptForm;
