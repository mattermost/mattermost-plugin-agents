// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useId, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';
import SendOutlineIcon from '@mattermost/compass-icons/components/send-outline';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {GlobalState} from '@mattermost/types/store';

import {createPost} from '@/client';

interface AgentChatComposerProps {
    channelId: string;

    // Names the textbox; a reply composer falls back to "Reply" when unknown.
    agentName: string;

    // Reply mode: the thread root. Omitted, the composer starts a new chat (a root post).
    rootId?: string;
    onCreated?: (postId: string) => void;

    // Optional control at the start of the pill (e.g. a future attach button).
    leadingAction?: React.ReactNode;
}

// Pill composer for the agent chat (Agents product view and Agents RHS), ported
// from the prototype's ComposerShell. Without `rootId` it starts a chat with a
// root post in the agent's DM; with `rootId` it replies in that thread. Either
// way it posts itself (the same Client4.createPost path the RHS uses for
// prompts), so the agent answers exactly as it does for any post in its DM.
// It does not upload files.
const AgentChatComposer = ({channelId, agentName, rootId = '', onCreated, leadingAction}: AgentChatComposerProps) => {
    const intl = useIntl();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const [message, setMessage] = useState('');
    const [sending, setSending] = useState(false);
    const [failed, setFailed] = useState(false);

    // A ref, not state, so a second Enter in the same tick cannot slip past it.
    const inFlight = useRef(false);

    // Kept across a retry of the same text so the server can dedupe a post
    // that did land before the request failed (as the host composer does).
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

    const canSend = message.trim().length > 0 && !sending;

    const send = useCallback(async () => {
        const text = message.trim();
        if (!text || inFlight.current) {
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
                file_ids: [],
            });
            if (!mounted.current) {
                return;
            }
            if (!created?.id) {
                throw new Error('createPost returned no post id');
            }
            pendingPostId.current = '';
            setMessage('');
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
    }, [channelId, rootId, currentUserId, message, onCreated]);

    const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
        // keyCode 229 covers browsers that end IME composition before isComposing flips.
        if (e.key !== 'Enter' || e.shiftKey || e.nativeEvent.isComposing || e.keyCode === 229) {
            return;
        }
        e.preventDefault();
        send();
    };

    // Unique per instance: the product view and the RHS can show this for the same DM.
    const errorId = useId();

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

    return (
        <ComposerColumn data-testid={isReply ? 'agent-chat-reply-composer' : 'agent-chat-new-composer'}>
            <Shell $hasLeadingAction={Boolean(leadingAction)}>
                {leadingAction}
                <GrowWrap data-value={message}>
                    <Textarea
                        ref={textareaRef}
                        rows={1}
                        value={message}
                        placeholder={placeholder}
                        aria-label={label}
                        aria-invalid={failed}
                        {...(failed ? {'aria-describedby': errorId} : {})}
                        onChange={(e) => {
                            setMessage(e.target.value);
                            setFailed(false);
                            pendingPostId.current = '';
                        }}
                        onKeyDown={handleKeyDown}
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

// No compass token caps a text box; about eight lines of body text.
const TEXTAREA_MAX_HEIGHT = 'calc(8 * var(--line-height-100))';

const ComposerColumn = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    width: 100%;
`;

// A leading action sits in the pill's end padding, like the send button.
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
    margin: 0;

    /* Vertically centers one line against the medium send button. */
    padding: var(--spacing-xs) 0;
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

// The prototype's send pill: primary colors once there is something to send.
const SendButton = styled(IconButton)`
    && {
        flex: 0 0 auto;
        width: auto;
        padding: var(--spacing-s) var(--spacing-l);
        border-radius: var(--radius-pill);
    }

    &&:not(:disabled),
    &&:hover:not(:disabled):not(:active) {
        background: var(--button-bg);
        color: var(--button-color);
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
