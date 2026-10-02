// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useRef, useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';
import {Radio} from '@mattermost/compass-ui/components/radio';
import {TextArea} from '@mattermost/compass-ui/components/text-area';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {useIsLicensedFor} from '@/license';

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

const RadioGroup = styled.div`
    display: flex;
    align-items: center;
    gap: 16px;
`;

const InlineRadio = styled(Radio)`
    && {
        width: auto;
    }
`;

const PrivateNote = styled.span`
    margin-left: 6px;
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

const DeleteButton = styled(Button)`
    margin-right: auto;
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
    const sharedPromptsLicensed = useIsLicensedFor('shared_prompts');
    const canShare = sharedPromptsLicensed || isShared;
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
                    emphasis='tertiary'
                    destructive={true}
                    onClick={onDelete}
                    disabled={isSaving}
                >
                    <FormattedMessage defaultMessage='Delete'/>
                </DeleteButton>
            )}
            <Button
                emphasis='tertiary'
                onClick={onDiscard}
                disabled={isSaving}
            >
                <FormattedMessage defaultMessage='Discard'/>
            </Button>
            <Button
                emphasis='primary'
                onClick={handleSave}
                loading={isSaving}
            >
                <FormattedMessage defaultMessage='Save'/>
            </Button>
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
                        {canShare && (
                            <InlineRadio
                                name={`visibility-${prompt?.id ?? 'new'}`}
                                checked={isShared}
                                disabled={!sharedPromptsLicensed}
                                onChange={() => setIsShared(true)}
                            >
                                <FormattedMessage defaultMessage='Public'/>
                            </InlineRadio>
                        )}
                        <InlineRadio
                            name={`visibility-${prompt?.id ?? 'new'}`}
                            checked={!isShared}
                            onChange={() => setIsShared(false)}
                        >
                            <FormattedMessage defaultMessage='Private'/>
                            <PrivateNote>
                                <FormattedMessage defaultMessage='(only you)'/>
                            </PrivateNote>
                        </InlineRadio>
                    </RadioGroup>
                </FieldGroup>
                <FieldGroup>
                    <TextInput
                        id={`prompt-name-${prompt?.id ?? 'new'}`}
                        label={<FormattedMessage defaultMessage='Action Title'/>}
                        value={name}
                        maxLength={64}
                        invalid={errors.name}
                        onChange={(e) => {
                            setName(e.target.value);
                            if (errors.name) {
                                setErrors((prev) => ({...prev, name: false}));
                            }
                        }}
                        placeholder={intl.formatMessage({defaultMessage: 'Enter a title for your prompt'})}
                    />
                    {errors.name && (
                        <ErrorMessage message={<FormattedMessage defaultMessage='Action title is required'/>}/>
                    )}
                </FieldGroup>
                <FieldGroup>
                    <TextArea
                        id={`prompt-description-${prompt?.id ?? 'new'}`}
                        label={<FormattedMessage defaultMessage='Brief Description'/>}
                        rows={2}
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
                                <Button
                                    emphasis='quaternary'
                                    size='x-small'
                                    onClick={() => setShowContextVars(!showContextVars)}
                                    aria-label={intl.formatMessage({defaultMessage: 'Insert context variable'})}
                                >
                                    <FormattedMessage defaultMessage='Context Variables'/>
                                </Button>
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
                    <TextArea
                        id={`prompt-template-${prompt?.id ?? 'new'}`}
                        ref={templateRef}
                        rows={6}
                        value={template}
                        invalid={errors.template}
                        onChange={(e) => {
                            setTemplate(e.target.value);
                            if (errors.template) {
                                setErrors((prev) => ({...prev, template: false}));
                            }
                        }}
                        placeholder={intl.formatMessage({defaultMessage: 'Enter the system prompt template'})}
                    />
                    {errors.template && (
                        <ErrorMessage message={<FormattedMessage defaultMessage='System prompt is required'/>}/>
                    )}
                </FieldGroup>
            </FormBody>
            <FormFooter>{actions}</FormFooter>
        </FormLayout>
    );
};

export default CustomPromptForm;
