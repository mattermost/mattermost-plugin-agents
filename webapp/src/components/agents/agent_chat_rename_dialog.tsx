// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import {AnimatedModalShell, MODAL_SHEET_CLASS} from '@/components/animated_modal_shell';

// Mirrors api.MaxConversationTitleLength (the server rejects longer titles).
export const MAX_CHAT_TITLE_LENGTH = 256;

interface AgentChatRenameDialogProps {
    show: boolean;
    initialTitle: string;

    // Rejects when the rename fails; the dialog then stays open with an error.
    onSubmit: (title: string) => Promise<void>;
    onClose: () => void;
}

// The prototype uses window.prompt; this is the same single field as a compass modal.
const AgentChatRenameDialog = ({show, initialTitle, onSubmit, onClose}: AgentChatRenameDialogProps) => {
    const intl = useIntl();
    const inputRef = useRef<HTMLInputElement>(null);
    const [title, setTitle] = useState(initialTitle);
    const [saving, setSaving] = useState(false);
    const [failed, setFailed] = useState(false);
    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => {
            mounted.current = false;
        };
    }, []);

    useEffect(() => {
        let frame = 0;
        if (show) {
            setTitle(initialTitle);
            setFailed(false);
            frame = window.requestAnimationFrame(() => inputRef.current?.select());
        }
        return () => window.cancelAnimationFrame(frame);
    }, [show, initialTitle]);

    const trimmed = title.trim();
    const canSave = trimmed.length > 0 && trimmed !== initialTitle.trim() && !saving;

    const close = () => {
        if (!saving) {
            onClose();
        }
    };

    const save = async () => {
        if (!canSave) {
            return;
        }
        setSaving(true);
        setFailed(false);
        try {
            await onSubmit(trimmed);
        } catch {
            if (mounted.current) {
                setFailed(true);
            }
        } finally {
            if (mounted.current) {
                setSaving(false);
            }
        }
    };

    return (
        <AnimatedModalShell
            show={show}
            onBackdropClick={close}
        >
            {/* eslint-disable-next-line agents/click-events-have-key-events, agents/no-static-element-interactions -- only stops clicks inside the sheet from reaching the backdrop */}
            <Sheet
                className={MODAL_SHEET_CLASS}
                onClick={(e) => e.stopPropagation()}
            >
                <Modal
                    size='small'
                    closeOnEscape={false}
                    title={intl.formatMessage({defaultMessage: 'Rename chat'})}
                    onClose={close}
                    closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                    headerDivider={false}
                    footerDivider={false}
                    scrollable={false}
                    footer={(
                        <>
                            <Button
                                emphasis='tertiary'
                                disabled={saving}
                                onClick={close}
                            >
                                {intl.formatMessage({defaultMessage: 'Cancel'})}
                            </Button>
                            <Button
                                emphasis='primary'
                                disabled={!canSave}
                                loading={saving}
                                onClick={save}
                            >
                                {intl.formatMessage({defaultMessage: 'Save'})}
                            </Button>
                        </>
                    )}
                >
                    <Form
                        onSubmit={(e) => {
                            e.preventDefault();
                            save();
                        }}
                    >
                        <TextInput
                            ref={inputRef}
                            label={intl.formatMessage({defaultMessage: 'Chat name'})}
                            value={title}
                            maxLength={MAX_CHAT_TITLE_LENGTH}
                            invalid={failed}
                            onChange={(e) => setTitle(e.target.value)}
                        />
                        {failed && (
                            <ErrorText role='alert'>
                                {intl.formatMessage({defaultMessage: 'Couldn\'t rename this chat. Try again.'})}
                            </ErrorText>
                        )}
                    </Form>
                </Modal>
            </Sheet>
        </AnimatedModalShell>
    );
};

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - (2 * var(--spacing-l)));
`;

const Form = styled.form`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

const ErrorText = styled.p`
    margin: 0;
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: var(--error-text);
`;

export default AgentChatRenameDialog;
