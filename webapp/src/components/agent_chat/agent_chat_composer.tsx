// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useId, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import SendOutlineIcon from '@mattermost/compass-icons/components/send-outline';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {GlobalState} from '@mattermost/types/store';

import {createPost} from '@/client';

import AgentChatAttachments from './agent_chat_attachments';
import {useAttachmentDropTarget} from './agent_chat_drop_zone';
import {MAX_FILES_PER_POST, useComposerAttachments} from './use_composer_attachments';

interface AgentChatComposerProps {
    channelId: string;

    // Names the textbox; a reply composer falls back to "Reply" when unknown.
    agentName: string;

    // Reply mode: the thread root. Omitted, the composer starts a new chat (a root post).
    rootId?: string;
    onCreated?: (postId: string) => void;

    // Replaces the attach ("+") button at the start of the pill.
    leadingAction?: React.ReactNode;
}

// Clipboard files (a pasted screenshot) as uploadable files; a nameless one
// gets the host's "Image Pasted at <date>" style name from `nameFor`.
const filesFromClipboard = (data: DataTransfer, nameFor: (stamp: string) => string): File[] => {
    const files: File[] = [];
    for (const item of Array.from(data.items ?? [])) {
        if (item.kind !== 'file') {
            continue;
        }
        const file = item.getAsFile();
        if (!file) {
            continue;
        }
        if (file.name) {
            files.push(file);
            continue;
        }
        const ext = item.type.includes('/') ? `.${item.type.slice(item.type.lastIndexOf('/') + 1).toLowerCase()}` : '';
        const now = new Date();
        const stamp = `${now.getFullYear()}-${now.getMonth() + 1}-${now.getDate()} ${String(now.getHours()).padStart(2, '0')}-${String(now.getMinutes()).padStart(2, '0')}`;
        files.push(new File([file], `${nameFor(stamp)}${ext}`, {type: file.type}));
    }
    return files;
};

const hasPlainText = (data: DataTransfer) => Array.from(data.types ?? []).includes('text/plain') && data.getData('text/plain').trim().length > 0;

// Pill composer for the agent chat (Agents product view and Agents RHS), ported
// from the prototype's ComposerShell. Without `rootId` it starts a chat with a
// root post in the agent's DM; with `rootId` it replies in that thread. Either
// way it posts itself (the same Client4.createPost path the RHS uses for
// prompts), so the agent answers exactly as it does for any post in its DM.
// Files attach through the "+" picker, a paste, or a drop on the surrounding
// AgentChatDropZone; each uploads to the DM right away and its file id goes
// out with the post. Send waits until every upload has settled.
const AgentChatComposer = ({channelId, agentName, rootId = '', onCreated, leadingAction}: AgentChatComposerProps) => {
    const intl = useIntl();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const [message, setMessage] = useState('');
    const [sending, setSending] = useState(false);
    const [failed, setFailed] = useState(false);
    const {
        attachments,
        canAttach,
        maxFileSize,
        limitReached,
        fileIds,
        uploading,
        hasErrors,
        addFiles,
        remove,
        retry,
        clear,
        hasRoom,
    } = useComposerAttachments(channelId);

    // A ref, not state, so a second Enter in the same tick cannot slip past it.
    const inFlight = useRef(false);

    // Kept across a retry of the same text and files so the server can dedupe
    // a post that did land before the request failed (as the host composer does).
    const pendingPostId = useRef('');
    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => {
            mounted.current = false;
        };
    }, []);

    useEffect(() => {
        textareaRef.current?.focus();
    }, []);

    const fileIdsKey = fileIds.join(',');
    useEffect(() => {
        pendingPostId.current = '';
        setFailed(false);
    }, [fileIdsKey]);

    // Dropped files land here while uploads are possible.
    const registerDrop = useAttachmentDropTarget();
    useEffect(() => {
        if (!registerDrop || !canAttach) {
            return () => null;
        }
        return registerDrop(addFiles);
    }, [registerDrop, canAttach, addFiles]);

    // Files alone are a valid post (as in the host composer); an unfinished or
    // failed upload holds the post until it is retried or removed.
    const hasContent = message.trim().length > 0 || fileIds.length > 0;
    const canSend = hasContent && !uploading && !hasErrors && !sending;

    const send = useCallback(async () => {
        const text = message.trim();
        if (!(text || fileIds.length > 0) || uploading || hasErrors || inFlight.current) {
            return;
        }
        inFlight.current = true;
        setSending(true);
        setFailed(false);
        if (!pendingPostId.current) {
            pendingPostId.current = `${currentUserId}:${Date.now()}`;
        }
        try {
            const created = await createPost({
                channel_id: channelId,
                root_id: rootId,
                message: text,
                pending_post_id: pendingPostId.current,
                props: {},
                file_ids: fileIds,
            });
            if (!mounted.current) {
                return;
            }
            if (!created?.id) {
                throw new Error('createPost returned no post id');
            }
            pendingPostId.current = '';
            setMessage('');
            clear();
            onCreated?.(created.id);
        } catch (e) {
            console.error(rootId ? 'Failed to send agent chat reply:' : 'Failed to start agent chat:', e); // eslint-disable-line no-console
            if (mounted.current) {
                setFailed(true);
            }
        } finally {
            inFlight.current = false;
            if (mounted.current) {
                setSending(false);
                textareaRef.current?.focus();
            }
        }
    }, [channelId, rootId, currentUserId, message, fileIds, uploading, hasErrors, clear, onCreated]);

    const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
        // keyCode 229 covers browsers that end IME composition before isComposing flips.
        if (e.key !== 'Enter' || e.shiftKey || e.nativeEvent.isComposing || e.keyCode === 229) {
            return;
        }
        e.preventDefault();
        send();
    };

    // Pasted files upload; pasted text is left to the textarea.
    const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
        const data = e.clipboardData;
        if (!canAttach || !data || hasPlainText(data)) {
            return;
        }
        const files = filesFromClipboard(data, (date) => intl.formatMessage({defaultMessage: 'Image Pasted at {date}'}, {date}));
        if (files.length > 0) {
            e.preventDefault();
            addFiles(files);
        }
    };

    const openPicker = () => {
        if (hasRoom()) {
            fileInputRef.current?.click();
        }
    };

    // Unique per instance: the product view and the RHS can show this for the same DM.
    const errorId = useId();
    const limitId = useId();

    const isReply = Boolean(rootId);
    let label;
    if (!isReply) {
        label = intl.formatMessage({defaultMessage: 'Message {name}'}, {name: agentName});
    } else if (agentName) {
        label = intl.formatMessage({defaultMessage: 'Reply to {name}'}, {name: agentName});
    } else {
        label = intl.formatMessage({defaultMessage: 'Reply'});
    }
    const placeholder = isReply ? intl.formatMessage({defaultMessage: 'Reply…'}) : label;

    let leading = leadingAction;
    if (!leading && canAttach) {
        leading = (
            <AttachButton
                size='medium'
                rounded={true}
                icon={<Icon glyph={<PlusIcon/>}/>}
                aria-label={intl.formatMessage({defaultMessage: 'Attach files'})}
                disabled={sending}
                onClick={openPicker}
            />
        );
    }
    const describedBy = [failed ? errorId : '', limitReached ? limitId : ''].filter(Boolean).join(' ');

    return (
        <ComposerColumn data-testid={isReply ? 'agent-chat-reply-composer' : 'agent-chat-new-composer'}>
            <AgentChatAttachments
                attachments={attachments}
                maxFileSize={maxFileSize}
                onRemove={remove}
                onRetry={retry}
            />
            {canAttach && (
                <HiddenFileInput
                    ref={fileInputRef}
                    type='file'
                    multiple={true}
                    tabIndex={-1}
                    aria-hidden={true}
                    data-testid='agent-chat-file-input'
                    onChange={(e) => {
                        addFiles(Array.from(e.target.files ?? []));
                        e.target.value = '';
                        textareaRef.current?.focus();
                    }}
                />
            )}
            <Shell $hasLeadingAction={Boolean(leading)}>
                {leading}
                <GrowWrap data-value={message}>
                    <Textarea
                        ref={textareaRef}
                        rows={1}
                        value={message}
                        placeholder={placeholder}
                        aria-label={label}
                        aria-invalid={failed}
                        {...(describedBy ? {'aria-describedby': describedBy} : {})}
                        onChange={(e) => {
                            setMessage(e.target.value);
                            setFailed(false);
                            pendingPostId.current = '';
                        }}
                        onKeyDown={handleKeyDown}
                        onPaste={handlePaste}
                    />
                </GrowWrap>
                <SendButton
                    size='medium'
                    icon={<Icon glyph={<SendOutlineIcon/>}/>}
                    aria-label={intl.formatMessage({defaultMessage: 'Send message'})}
                    disabled={!canSend}
                    loading={sending}
                    onClick={send}
                />
            </Shell>
            {limitReached && (
                <ErrorText
                    id={limitId}
                    role='alert'
                >
                    {intl.formatMessage({defaultMessage: 'You can attach up to {count, number} files to a message.'}, {count: MAX_FILES_PER_POST})}
                </ErrorText>
            )}
            {failed && (
                <ErrorText
                    id={errorId}
                    role='alert'
                >
                    {intl.formatMessage({defaultMessage: 'Couldn\'t send your message. Try again.'})}
                </ErrorText>
            )}
        </ComposerColumn>
    );
};

// No compass token caps a text box; eight lines of body text plus the
// textarea's block padding.
const TEXTAREA_MAX_HEIGHT = 'calc(8 * var(--line-height-100) + 2 * var(--spacing-s))';

const ComposerColumn = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    width: 100%;
`;

// The prototype's ComposerShell row: "+", text, send, xs apart inside xs
// padding. Items sit on the bottom row so the buttons stay by the last line as
// the text grows. Without a leading button the text keeps the pill's l inset.
const Shell = styled.div<{$hasLeadingAction: boolean}>`
    display: flex;
    flex-direction: row;
    align-items: flex-end;
    gap: var(--spacing-xs);
    padding: var(--spacing-xs) var(--spacing-xs) var(--spacing-xs) ${({$hasLeadingAction}) => ($hasLeadingAction ? 'var(--spacing-xs)' : 'var(--spacing-l)')};
    border: var(--border-dark);
    border-radius: var(--radius-pill);
    background: var(--center-channel-bg);
    box-shadow: var(--elevation-4);
    transition: box-shadow var(--duration-quick) var(--ease-transition);

    &:focus-within {
        box-shadow: var(--elevation-5);
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

const textStyles = `
    box-sizing: border-box;
    margin: 0;

    /*
     * One line of text plus this padding is exactly a medium compass
     * IconButton (40px: line-height-100 + 2 * spacing-s), the height of the
     * "+" and send buttons. With the pill's items bottom-aligned, the first
     * line (and the placeholder) shares their center while the text is one
     * line, and the last line keeps it as the text grows.
     */
    padding: var(--spacing-s) 0;
    border: none;
    font-family: var(--font-family-body);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-100);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
`;

// Auto-grow without measuring: an invisible copy of the text (::after) sizes the
// grid cell, and the textarea stacked in the same cell fills it.
const GrowWrap = styled.div`
    display: grid;
    flex: 1 1 auto;
    min-width: 0;
    max-height: ${TEXTAREA_MAX_HEIGHT};

    &::after {
        ${textStyles}
        content: attr(data-value) ' ';
        grid-area: 1 / 1;
        visibility: hidden;
        overflow: hidden;
    }
`;

// Native textarea: compass TextArea is a labelled form field with its own border
// and frame; the pill shell supplies both here.
const Textarea = styled.textarea`
    ${textStyles}
    grid-area: 1 / 1;
    min-width: 0;
    max-height: ${TEXTAREA_MAX_HEIGHT};
    background: transparent;
    resize: none;
    overflow-y: auto;
    color: var(--center-channel-color);
    outline: none;

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.64);
    }
`;

// The prototype's ComposerShell "+": a round, faintly filled icon button.
const AttachButton = styled(IconButton)`
    && {
        flex: 0 0 auto;
    }

    &&:not(:disabled):not(:hover):not(:active) {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

// Opened by the "+" button; never focused or announced itself.
const HiddenFileInput = styled.input`
    position: absolute;
    width: 1px;
    height: 1px;
    margin: -1px;
    padding: 0;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
`;

// The prototype's send pill, colored like a compass primary Button (Button.css
// emphasis-primary: fill, 8%/16% black hover/active mixes, disabled wash).
// compass Button cannot be icon-only at the 40px pill height, so this keeps the
// medium IconButton (its size, focus ring and transitions) and recolors it.
// The extra && outranks IconButton's own :hover/:active/:disabled rules.
const SendButton = styled(IconButton)`
    && {
        flex: 0 0 auto;
        width: auto;
        padding: var(--spacing-s) var(--spacing-l);
        border-radius: var(--radius-pill);
    }

    &&&&:not(:disabled) {
        --icon-button-fg: var(--button-color);
        background-color: var(--button-bg);
        color: var(--button-color);
    }

    &&&&:hover:not(:disabled):not(:active) {
        --icon-button-fg: var(--button-color);
        background-color: color-mix(in srgb, black 8%, var(--button-bg));
        color: var(--button-color);
    }

    &&&&:active:not(:disabled) {
        --icon-button-fg: var(--button-color);
        background-color: color-mix(in srgb, black 16%, var(--button-bg));
        color: var(--button-color);
    }

    &&&&:disabled {
        --icon-button-fg: rgba(var(--center-channel-color-rgb), 0.32);
        background-color: rgba(var(--center-channel-color-rgb), 0.08);
        color: rgba(var(--center-channel-color-rgb), 0.32);
        opacity: 1;
    }
`;

const ErrorText = styled.p`
    margin: 0;
    padding: 0 var(--spacing-l);
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: var(--error-text);
`;

export default AgentChatComposer;
