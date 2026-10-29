// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useId, useLayoutEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import styled, {css} from 'styled-components';
import SendOutlineIcon from '@mattermost/compass-icons/components/send-outline';
import RecordSquareOutlineIcon from '@mattermost/compass-icons/components/record-square-outline';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {GlobalState} from '@mattermost/types/store';

import type {LLMBot} from '@/bots';
import {Client4, createPost, doStopGenerating} from '@/client';
import {Textbox as HostTextbox} from '@/mm_webapp';

import AgentChatAttachments from './agent_chat_attachments';
import {
    TEXTAREA_EXPANDED_MAX_HEIGHT,
    TEXTAREA_EXPANDED_ONE_LINE_HEIGHT,
    TEXTAREA_EXPANDED_PADDING,
    TEXTAREA_MAX_HEIGHT,
    TEXTAREA_ONE_LINE_HEIGHT,
    composerTextStyles,
    hostTextboxPillStyles,
} from './agent_chat_composer_host_styles';
import {useAttachmentDropTarget} from './agent_chat_drop_zone';
import {
    DEFAULT_MAX_POST_SIZE,
    INVALID_SLASH_COMMAND_ERROR_ID,
    PREF_CODE_BLOCK_CTRL_ENTER,
    PREF_SEND_ON_CTRL_ENTER,
    decideEnter,
    draftKey,
    gotoTarget,
    isSlashCommand,
    normalizeCommand,
    readDraft,
    writeDraft,
} from './composer_input';
import ComposerPlusMenu from './composer_plus_menu';
import {MAX_FILES_PER_POST, useComposerAttachments} from './use_composer_attachments';
import {useThreadProfiles} from './use_thread_profiles';
import {markGenerationStopped, useThreadGenerating} from './use_thread_generating';

interface AgentChatComposerProps {

    // The channel posts and commands go to: the agent's DM, or the channel of
    // a thread the agent was mentioned in.
    channelId: string;

    // Names the textbox; a reply composer falls back to "Reply" when unknown.
    agentName: string;

    // Reply mode: the thread root. Omitted, the composer starts a new chat (a root post).
    rootId?: string;
    onCreated?: (postId: string) => void;

    // The chat's agent, for the "+" menu's Tools and AI Rewrite; null or
    // omitted for a thread without one.
    bot?: LLMBot | null;

    // Replaces the "+" menu at the start of the pill.
    leadingAction?: React.ReactNode;
}

// The parts of the host Textbox instance (components/textbox/textbox.tsx) used here.
interface HostTextboxHandle {
    focus: () => void;
    getInputBox: () => HTMLTextAreaElement | HTMLInputElement | null | undefined;
}

type SendError = {kind: 'post'} | {kind: 'stop'} | {kind: 'command'; message: string; invalidCommand: boolean} | {kind: 'menu'; message: string};

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

const EMPTY_CONFIG: Partial<Record<string, string>> = {};

const prefValue = (state: GlobalState, key: string) => (state.entities.preferences?.myPreferences as Record<string, {value?: string}> | undefined)?.[key]?.value;

const splitRoles = (roles?: string) => (roles ?? '').split(' ').filter(Boolean);

// Whether @here/@channel/@all are offered, from the user's system, team and
// channel roles (the host's USE_CHANNEL_MENTIONS check, without channel
// scheme roles). It only shapes the suggestions; the server decides whether
// such a mention notifies. With no roles loaded they are offered.
const canUseChannelMentions = (state: GlobalState, channelId: string) => {
    const channel = state.entities.channels?.channels?.[channelId];
    const teamId = channel?.team_id || state.entities.teams?.currentTeamId || '';
    const names = [
        ...splitRoles(state.entities.users?.profiles?.[state.entities.users.currentUserId]?.roles),
        ...splitRoles(state.entities.teams?.myMembers?.[teamId]?.roles),
        ...splitRoles(state.entities.channels?.myMembers?.[channelId]?.roles),
    ];
    const roles = names.map((name) => state.entities.roles?.roles?.[name]).filter(Boolean);
    if (roles.length === 0) {
        return true;
    }
    return roles.some((role) => role.permissions?.includes('use_channel_mentions'));
};

// A DOM id from React's useId (":r1:"), safe for host code that builds selectors.
const domId = (prefix: string, reactId: string) => `${prefix}-${reactId.replace(/[^a-zA-Z0-9_-]/g, '')}`;

// Pill composer for the agent chat (Agents product view and Agents RHS), ported
// from the prototype's ComposerShell. Without `rootId` it starts a chat with a
// root post; with `rootId` it replies in that thread, which may be the agent's
// DM or a channel thread the agent was mentioned in. Either way it posts itself
// (Client4.createPost, as the RHS does for prompts), so the agent answers
// exactly as it does for any other post.
//
// Where the host publishes its Textbox, the text box is the host's suggestion
// box: @mention, ~channel, :emoji: and slash command autocomplete, scoped to
// `channelId`/`rootId`. A message starting with "/" runs as a slash command
// through the commands API, as in the host composer. Elsewhere a plain
// textarea stands in. In-progress text is kept per channel and thread for the
// browser session.
//
// Files attach through the "+" menu's Upload files, a paste, or a drop on the surrounding
// AgentChatDropZone; each uploads to the channel right away and its file id
// goes out with the post. Send waits until every upload has settled.
const AgentChatComposer = ({channelId, agentName, rootId = '', onCreated, bot, leadingAction}: AgentChatComposerProps) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const currentTeamId = useSelector<GlobalState, string>((state) => state.entities.teams?.currentTeamId ?? '');
    const channelTeamId = useSelector<GlobalState, string>((state) => state.entities.channels?.channels?.[channelId]?.team_id ?? '');
    const config = useSelector<GlobalState, Partial<Record<string, string>>>((state) => state.entities.general?.config ?? EMPTY_CONFIG);
    const sendOnCtrlEnter = useSelector<GlobalState, boolean>((state) => prefValue(state, PREF_SEND_ON_CTRL_ENTER) === 'true');
    const codeBlockOnCtrlEnter = useSelector<GlobalState, boolean>((state) => prefValue(state, PREF_CODE_BLOCK_CTRL_ENTER) !== 'false');
    const useChannelMentions = useSelector<GlobalState, boolean>((state) => canUseChannelMentions(state, channelId));
    const maxPostSize = parseInt(config.MaxPostSize ?? '', 10) || DEFAULT_MAX_POST_SIZE;

    useThreadProfiles(rootId, Boolean(HostTextbox));

    // While the agent answers in this thread, Send becomes Stop.
    const generatingPostId = useThreadGenerating(channelId, rootId);
    const [stopping, setStopping] = useState(false);
    const [justStopped, setJustStopped] = useState(false);

    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const hostRef = useRef<HostTextboxHandle>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);

    const key = draftKey(channelId, rootId);
    const [message, setMessage] = useState(() => readDraft(key));
    const [sending, setSending] = useState(false);

    // The send-key hint shows while the composer is focused and empty, then fades after a few seconds.
    const [focused, setFocused] = useState(false);
    const [hintExpired, setHintExpired] = useState(false);
    useEffect(() => {
        setHintExpired(false);
        const timer = focused ? setTimeout(() => setHintExpired(true), SEND_HINT_MS) : null;
        return () => {
            if (timer) {
                clearTimeout(timer);
            }
        };
    }, [focused]);
    const [sendError, setSendError] = useState<SendError | null>(null);

    // While an AI Rewrite runs the box is read-only and Send waits, so the
    // rewritten text never lands over newer edits (as the host holds them).
    const [rewriting, setRewriting] = useState(false);
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

    // A command the server did not recognize: sending the same text again posts
    // it as a message (the host's "send as message" fallback).
    const invalidCommand = useRef('');

    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => {
            mounted.current = false;
        };
    }, []);

    const focusInput = useCallback(() => {
        if (hostRef.current) {
            hostRef.current.focus();
        } else {
            textareaRef.current?.focus();
        }
    }, []);

    useEffect(() => {
        focusInput();
    }, [focusInput]);

    // The draft follows the text; another channel or thread brings back its own.
    const loadedKey = useRef(key);
    useEffect(() => {
        if (loadedKey.current !== key) {
            loadedKey.current = key;
            setMessage(readDraft(key));
            return;
        }
        writeDraft(key, message);
    }, [key, message]);

    const fileIdsKey = fileIds.join(',');
    useEffect(() => {
        pendingPostId.current = '';
        setSendError(null);
    }, [fileIdsKey]);

    // Dropped files land here while uploads are possible.
    const registerDrop = useAttachmentDropTarget();
    useEffect(() => {
        if (!registerDrop || !canAttach) {
            return () => null;
        }
        return registerDrop(addFiles);
    }, [registerDrop, canAttach, addFiles]);

    // Over the server's MaxPostSize (counted untrimmed, as the host does) the
    // message cannot go out.
    const tooLong = message.length > maxPostSize;

    // Files alone are a valid post (as in the host composer); an unfinished or
    // failed upload holds the post until it is retried or removed.
    const hasContent = message.trim().length > 0 || fileIds.length > 0;

    // Which key sends follows the user's Advanced setting (Ctrl+Enter, or Enter).
    let sendHint = intl.formatMessage({defaultMessage: 'Enter to send'});
    if (sendOnCtrlEnter) {
        sendHint = isMacPlatform() ? intl.formatMessage({defaultMessage: '⌘ Enter to send'}) : intl.formatMessage({defaultMessage: 'Ctrl+Enter to send'});
    }

    const hintVisible = focused && !hintExpired && !hasContent && !generatingPostId;

    const canSend = hasContent && !uploading && !hasErrors && !sending && !tooLong && !rewriting;

    // Runs a slash command as actions/command.ts does. Its ephemeral response
    // arrives as a post over the websocket; only errors surface here.
    const runCommand = useCallback(async (text: string) => {
        const response = await Client4.executeCommand(normalizeCommand(text), {
            channel_id: channelId,

            // As the host: the current team, which DMs and GMs (no team of
            // their own) also run under; the channel's team when there is none.
            team_id: currentTeamId || channelTeamId,
            root_id: rootId,
        });
        if (!mounted.current) {
            return;
        }

        // The host opens an interactive dialog from its websocket event once
        // the trigger id is in the store.
        if (response?.trigger_id) {
            dispatch({type: 'RECEIVED_DIALOG_TRIGGER_ID', data: response.trigger_id});
        }
        const target = gotoTarget(response?.goto_location, config.SiteURL ?? '');
        if (target && 'path' in target) {
            window.WebappUtils?.browserHistory?.push(target.path);
        } else if (target) {
            window.open(target.url, '_blank', 'noopener,noreferrer');
        }
        invalidCommand.current = '';
        writeDraft(key, '');
        setMessage('');
    }, [channelId, currentTeamId, channelTeamId, rootId, dispatch, config.SiteURL, key]);

    const postMessage = useCallback(async (text: string) => {
        if (!pendingPostId.current) {
            pendingPostId.current = `${currentUserId}:${Date.now()}`;
        }
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
        invalidCommand.current = '';

        // Now, not from the draft effect: onCreated may unmount this composer
        // (a new chat becomes its thread) before that effect would run.
        writeDraft(key, '');
        setMessage('');
        clear();
        onCreated?.(created.id);
    }, [channelId, rootId, currentUserId, fileIds, clear, onCreated, key]);

    // `override`: the text to send instead of the box's (a closed code block).
    const send = useCallback(async (override?: string) => {
        const text = (override ?? message).trim();
        if (!(text || fileIds.length > 0) || uploading || hasErrors || tooLong || rewriting || inFlight.current) {
            return;
        }
        const asCommand = isSlashCommand(text) && invalidCommand.current !== text;
        inFlight.current = true;
        setSending(true);
        setSendError(null);
        try {
            if (asCommand) {
                await runCommand(text);
            } else {
                await postMessage(text);
            }
        } catch (e) {
            console.error(rootId ? 'Failed to send agent chat reply:' : 'Failed to start agent chat:', e); // eslint-disable-line no-console
            if (mounted.current) {
                if (asCommand) {
                    const err = e as {message?: string; server_error_id?: string};
                    const invalid = err.server_error_id === INVALID_SLASH_COMMAND_ERROR_ID;
                    invalidCommand.current = invalid ? text : '';
                    setSendError({kind: 'command', message: err.message ?? '', invalidCommand: invalid});
                } else {
                    setSendError({kind: 'post'});
                }
            }
        } finally {
            inFlight.current = false;
            if (mounted.current) {
                setSending(false);
                focusInput();
            }
        }
    }, [message, fileIds.length, uploading, hasErrors, tooLong, rewriting, rootId, runCommand, postMessage, focusInput]);

    // The same call as the post's own Stop control; the server then ends the
    // stream (cancel, end), which LLMBotPost renders as stopped.
    const stopGeneration = async () => {
        if (!generatingPostId || stopping) {
            return;
        }
        setStopping(true);
        setSendError(null);
        try {
            await doStopGenerating(generatingPostId);
            markGenerationStopped(generatingPostId);
            if (mounted.current) {
                setJustStopped(true);
            }
        } catch (e) {
            console.error('Failed to stop generating:', e); // eslint-disable-line no-console
            if (mounted.current) {
                setSendError({kind: 'stop'});
            }
        } finally {
            if (mounted.current) {
                setStopping(false);
            }
        }
    };

    useEffect(() => {
        if (generatingPostId) {
            setJustStopped(false);
        }
    }, [generatingPostId]);

    const handleChange = (e: {target: {value: string}}) => {
        setMessage(e.target.value);
        setSendError(null);
        pendingPostId.current = '';
    };

    // The "+" menu reads and replaces the text through these. A replacement is
    // an edit like typing (draft, wrap measuring and expanded layout follow
    // `message`), then focus returns to the box with the caret at the end.
    const getText = useCallback(() => message, [message]);
    const setText = useCallback((text: string) => {
        setMessage(text);
        setSendError(null);
        pendingPostId.current = '';
        focusInput();
    }, [focusInput]);
    const showMenuError = useCallback((errorMessage: string) => {
        setSendError({kind: 'menu', message: errorMessage});
    }, []);

    const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement | HTMLInputElement>) => {
        // The host suggestion list stops the Enter (or Tab) that picks a
        // suggestion before passing it on, if it does at all.
        if (e.isPropagationStopped() || e.defaultPrevented) {
            return;
        }
        const target = e.target as HTMLTextAreaElement;
        const decision = decideEnter({
            key: e.key,
            shiftKey: e.shiftKey,
            altKey: e.altKey,
            ctrlKey: e.ctrlKey,
            metaKey: e.metaKey,
            isComposing: e.nativeEvent.isComposing,
            keyCode: e.keyCode,
        }, {
            message,
            caret: target.selectionStart ?? message.length,
            sendOnCtrlEnter,
            codeBlockOnCtrlEnter,
        });
        if (!decision.send) {
            return;
        }
        e.preventDefault();
        send(decision.message);
    };

    // Pasted files upload; pasted text is left to the textarea.
    const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement | HTMLInputElement>) => {
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
    const reactId = useId();
    const inputId = domId('agent-chat-textbox', reactId);
    const errorId = domId('agent-chat-composer-error', reactId);
    const limitId = domId('agent-chat-composer-limit', reactId);
    const lengthId = domId('agent-chat-composer-length', reactId);

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

    const leading = (
        <LeadingSlot>
            {leadingAction ?? (
                <ComposerPlusMenu
                    bot={bot}
                    channelId={channelId}
                    rootId={rootId}
                    uploadEnabled={canAttach}
                    onUploadFiles={openPicker}
                    getText={getText}
                    setText={setText}
                    disabled={sending}
                    onError={showMenuError}
                    onRewritingChange={setRewriting}
                />
            )}
        </LeadingSlot>
    );
    const invalid = Boolean(sendError) || tooLong;

    // Expanded (text on its own full-width row, "+" and send below) once the
    // text has a newline or wraps at the INLINE width. The mirrors always lay
    // the text out at that width, whatever the current arrangement, so the
    // wider expanded box never flips it back (no flip-flop): it collapses only
    // when the text fits one inline line again (or is empty).
    const hasLeading = Boolean(leading);
    const wrapMirrorRef = useRef<HTMLDivElement>(null);
    const lineMirrorRef = useRef<HTMLDivElement>(null);
    const shellRef = useRef<HTMLDivElement>(null);
    const [wraps, setWraps] = useState(false);
    const measureWrap = useCallback(() => {
        const text = wrapMirrorRef.current;
        const line = lineMirrorRef.current;
        if (text && line) {
            setWraps(text.scrollHeight > line.scrollHeight);
        }
    }, []);
    useLayoutEffect(measureWrap, [message, hasLeading, measureWrap]);
    useEffect(() => {
        const shell = shellRef.current;
        if (!shell || typeof ResizeObserver === 'undefined') {
            return () => null;
        }
        const observer = new ResizeObserver(measureWrap);
        observer.observe(shell);
        return () => observer.disconnect();
    }, [measureWrap]);
    const expanded = message.includes('\n') || wraps;

    const describedBy = [sendError ? errorId : '', limitReached ? limitId : '', tooLong ? lengthId : ''].filter(Boolean).join(' ');

    // The host Textbox takes no aria props; set them on its textarea.
    useEffect(() => {
        const input = hostRef.current?.getInputBox();
        if (!input) {
            return;
        }
        input.setAttribute('aria-invalid', String(invalid));
        input.readOnly = rewriting;
        if (describedBy) {
            input.setAttribute('aria-describedby', describedBy);
        } else {
            input.removeAttribute('aria-describedby');
        }
    });

    let errorText = '';
    if (sendError?.kind === 'post') {
        errorText = intl.formatMessage({defaultMessage: 'Couldn\'t send your message. Try again.'});
    } else if (sendError?.kind === 'stop') {
        errorText = intl.formatMessage({defaultMessage: 'Couldn\'t stop generating. Try again.'});
    } else if (sendError?.kind === 'menu') {
        errorText = sendError.message;
    } else if (sendError?.kind === 'command') {
        errorText = sendError.message || intl.formatMessage({defaultMessage: 'Couldn\'t run that command. Try again.'});
        if (sendError.invalidCommand) {
            errorText = `${errorText} ${intl.formatMessage({defaultMessage: 'Send it again to post it as a message.'})}`;
        }
    }

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
                        focusInput();
                    }}
                />
            )}
            <Shell
                ref={shellRef}
                $hasLeadingAction={hasLeading}
                $hostTextbox={Boolean(HostTextbox)}
                data-expanded={expanded}
                aria-busy={rewriting}
                onFocus={() => setFocused(true)}
                onBlur={() => setFocused(false)}
                data-testid='agent-chat-composer-pill'
            >
                <WrapMirror
                    ref={wrapMirrorRef}
                    $hasLeadingAction={hasLeading}
                    aria-hidden={true}
                    data-testid='agent-chat-composer-wrap-mirror'
                >
                    {`${message} `}
                </WrapMirror>
                <WrapMirror
                    ref={lineMirrorRef}
                    $hasLeadingAction={hasLeading}
                    aria-hidden={true}
                    data-testid='agent-chat-composer-line-mirror'
                >
                    {' '}
                </WrapMirror>
                {leading}
                {HostTextbox ? (
                    <HostTextboxSlot>
                        <HiddenLabel htmlFor={inputId}>{label}</HiddenLabel>
                        <HostTextbox
                            ref={hostRef}
                            id={inputId}
                            channelId={channelId}
                            rootId={rootId}
                            value={message}
                            createMessage={placeholder}
                            onChange={handleChange}
                            onKeyDown={handleKeyDown}
                            onKeyPress={noop}
                            onPaste={handlePaste}
                            supportsCommands={true}
                            useChannelMentions={useChannelMentions}
                            emojiEnabled={false}
                            characterLimit={maxPostSize}
                            preview={false}
                            suggestionListPosition='top'
                            alignWithTextbox={true}
                        />
                    </HostTextboxSlot>
                ) : (
                    <GrowWrap data-value={message}>
                        <Textarea
                            ref={textareaRef}
                            id={inputId}
                            rows={1}
                            value={message}
                            placeholder={placeholder}
                            aria-label={label}
                            aria-invalid={invalid}
                            readOnly={rewriting}
                            {...(describedBy ? {'aria-describedby': describedBy} : {})}
                            onChange={handleChange}
                            onKeyDown={handleKeyDown}
                            onPaste={handlePaste}
                        />
                    </GrowWrap>
                )}
                {generatingPostId ? (
                    <StopButton
                        size='small'
                        rounded={true}
                        icon={<Icon glyph={<RecordSquareOutlineIcon/>}/>}
                        aria-label={intl.formatMessage({defaultMessage: 'Stop generating'})}
                        data-testid='stop-generating-button'
                        loading={stopping}
                        onClick={stopGeneration}
                    />
                ) : (
                    <SendButton
                        size='small'
                        rounded={true}
                        data-emphasis='primary'
                        icon={<SendIcon glyph={<SendOutlineIcon/>}/>}
                        aria-label={intl.formatMessage({defaultMessage: 'Send message'})}
                        disabled={!canSend}
                        loading={sending}
                        onClick={() => send()}
                    />
                )}
                <SendHint
                    aria-hidden={true}
                    data-visible={hintVisible}
                    data-testid='agent-chat-send-hint'
                >
                    {sendHint}
                </SendHint>
                <LiveStatus
                    role='status'
                    aria-live='polite'
                >
                    {generatingPostId ? intl.formatMessage({defaultMessage: 'Generating…'}) : ''}
                    {!generatingPostId && justStopped ? intl.formatMessage({defaultMessage: 'Stopped'}) : ''}
                    {rewriting ? intl.formatMessage({defaultMessage: 'Rewriting…'}) : ''}
                </LiveStatus>
            </Shell>
            {limitReached && (
                <ErrorText
                    id={limitId}
                    role='alert'
                >
                    {intl.formatMessage({defaultMessage: 'You can attach up to {count, number} files to a message.'}, {count: MAX_FILES_PER_POST})}
                </ErrorText>
            )}
            {tooLong && (
                <ErrorText
                    id={lengthId}
                    role='alert'
                >
                    {intl.formatMessage({defaultMessage: 'Your message is too long. Character count: {length, number}/{limit, number}'}, {length: message.length, limit: maxPostSize})}
                </ErrorText>
            )}
            {sendError && (
                <ErrorText
                    id={errorId}
                    role='alert'
                >
                    {errorText}
                </ErrorText>
            )}
        </ComposerColumn>
    );
};

// How long the hint stays once the composer is focused.
const SEND_HINT_MS = 4000;

const isMacPlatform = () => (/Mac|iPhone|iPad|iPod/i).test(navigator.platform || navigator.userAgent || '');

// The host Textbox requires onKeyPress; sending is decided on keydown.
const noop = () => null;

const visuallyHidden = css`
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

const ComposerColumn = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    width: 100%;
`;

// No compass or host token for the border washes: the host's message box draws a 2px border at 16% of
// the text color, 24% on hover and 32% while focused (advanced_text_editor.scss in the host).
const BORDER_REST = 'rgba(var(--center-channel-color-rgb), 0.16)';
const BORDER_HOVER = 'rgba(var(--center-channel-color-rgb), 0.24)';
const BORDER_FOCUS = 'rgba(var(--center-channel-color-rgb), 0.32)';

// The prototype's ComposerShell row: "+", text, send, xs apart inside xs
// padding. Items sit on the bottom row so the buttons stay by the last line as
// the text grows. Without a leading button the text keeps the pill's l inset.
// Positioned so the host suggestion list can float above the whole pill.
// The small compass IconButton ("+", send, stop) is 32px, exactly one text
// line (TEXTAREA_ONE_LINE_HEIGHT), so the pill's geometry uses one value.
const PILL_BUTTON_SIZE = TEXTAREA_ONE_LINE_HEIGHT;

// Inline: [+] [text] [send] on one row. Expanded (data-expanded): the text
// spans the whole row above, "+" bottom left, send bottom right. Grid areas
// only, so the DOM (and tab) order is "+", text, send in both. No transition:
// the switch happens once when the text wraps, not per keystroke.
const Shell = styled.div<{$hasLeadingAction: boolean; $hostTextbox: boolean}>`
    position: relative;
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    grid-template-areas: ${({$hasLeadingAction}) => ($hasLeadingAction ? "'plus input send'" : "'input input send'")};
    align-items: end;
    gap: var(--spacing-xs);

    /* No gap under the text: its own xs inset is the space above the buttons. */
    &[data-expanded='true'] {
        row-gap: 0;
        grid-template-areas:
            'input input input'
            ${({$hasLeadingAction}) => ($hasLeadingAction ? "'plus . send'" : "'. . send'")};
    }
    padding: var(--spacing-xs) var(--spacing-xs) var(--spacing-xs) ${({$hasLeadingAction}) => ($hasLeadingAction ? 'var(--spacing-xs)' : 'var(--spacing-l)')};
    border: var(--spacing-xxxxs) solid ${BORDER_REST};
    border-radius: var(--radius-s);
    background: var(--center-channel-bg);
    transition: border-color var(--duration-quick) var(--ease-transition);

    /* Hover and focus raise the border's opacity. */
    &:hover {
        border-color: ${BORDER_HOVER};
    }

    &:focus-within,
    &:focus-within:hover {
        border-color: ${BORDER_FOCUS};
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }

    ${({$hostTextbox}) => $hostTextbox && hostTextboxPillStyles}
`;

// The host Textbox sizes itself (components/autosize_textarea.tsx), up to the
// max-height set in agent_chat_composer_host_styles.ts.
const HostTextboxSlot = styled.div`
    grid-area: input;
    min-width: 0;
`;

// Lays the text out at the inline text width (between "+" and send) to tell
// whether it wraps; never shown. The pill's padding box is the containing
// block, so the insets are the pill padding plus the buttons and gaps.
const WrapMirror = styled.div<{$hasLeadingAction: boolean}>`
    ${composerTextStyles}
    position: absolute;
    top: 0;
    left: ${({$hasLeadingAction}) => ($hasLeadingAction ? `calc(2 * var(--spacing-xs) + ${PILL_BUTTON_SIZE})` : 'var(--spacing-l)')};
    right: calc(2 * var(--spacing-xs) + ${PILL_BUTTON_SIZE});
    visibility: hidden;
    pointer-events: none;
`;

// A custom leading control in the "+" slot.
const LeadingSlot = styled.div`
    grid-area: plus;
    display: flex;
`;

// Announces Generating… / Stopped as the send button turns into Stop and back.
const LiveStatus = styled.span`
    ${visuallyHidden}
`;

// Names the host textarea, which takes no aria-label of its own.
const HiddenLabel = styled.label`
    ${visuallyHidden}
`;

// Auto-grow without measuring: an invisible copy of the text (::after) sizes the
// grid cell, and the textarea stacked in the same cell fills it.
const GrowWrap = styled.div`
    display: grid;
    grid-area: input;
    min-width: 0;
    max-height: ${TEXTAREA_MAX_HEIGHT};

    &::after {
        ${composerTextStyles}
        content: attr(data-value) ' ';
        grid-area: 1 / 1;
        visibility: hidden;
        overflow: hidden;
    }

    [data-expanded='true'] & {
        max-height: ${TEXTAREA_EXPANDED_MAX_HEIGHT};
    }

    [data-expanded='true'] &::after {
        padding: ${TEXTAREA_EXPANDED_PADDING};
    }
`;

// Native textarea: compass TextArea is a labelled form field with its own border
// and frame; the pill shell supplies both here.
const Textarea = styled.textarea`
    ${composerTextStyles}
    grid-area: 1 / 1;
    min-width: 0;
    min-height: ${TEXTAREA_ONE_LINE_HEIGHT};
    max-height: ${TEXTAREA_MAX_HEIGHT};
    background: transparent;
    resize: none;
    overflow-y: auto;
    color: var(--center-channel-color);
    outline: none;

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.64);
    }

    [data-expanded='true'] & {
        padding: ${TEXTAREA_EXPANDED_PADDING};
        min-height: ${TEXTAREA_EXPANDED_ONE_LINE_HEIGHT};
        max-height: ${TEXTAREA_EXPANDED_MAX_HEIGHT};
    }
`;

// Optical correction for the send glyph's shape, not a layout value: the
// paper plane's path spans x 2-22.2 of its 24-unit viewBox (box centered), but
// its mass is the triangle from the left edge to the right tip, whose centroid
// sits at x ~8.7, ~2.4px left of center at the small button's 16px glyph (~3px
// at the old 20px one). Nudging it right by --spacing-xxxxs (2px; 1px leaves
// it ~1.4px left-heavy) makes it look centered. A transform, so the button's
// size and hit area stay.
const SEND_ICON_OPTICAL_OFFSET = 'var(--spacing-xxxxs)';

const SendIcon = styled(Icon)`
    transform: translateX(${SEND_ICON_OPTICAL_OFFSET});
`;

// Stop: the standard (neutral) compass IconButton, round and 32px like Send so
// swapping them does not move anything; quieter than the primary Send.
const StopButton = styled(IconButton)`
    && {
        grid-area: send;
    }
`;

// Opened by the "+" menu's Upload files; never focused or announced itself.
const HiddenFileInput = styled.input`
    ${visuallyHidden}
`;

// Send (and Stop): a round small compass IconButton (32x32, the "+" size,
// circular through `rounded`) colored like a compass primary Button
// (Button.css emphasis-primary: fill, 8%/16% black hover/active mixes,
// disabled wash). The extra && outranks IconButton's own
// :hover/:active/:disabled rules.
const SendButton = styled(IconButton)`
    && {
        grid-area: send;
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
        background-color: transparent;
        color: rgba(var(--center-channel-color-rgb), 0.32);
        opacity: 1;
    }
`;

// Inside the pill, on the right, just left of the send button: out of the grid flow, so showing or
// hiding it never moves anything. The right inset is the pill's padding plus the 32px button and the gap.
const SendHint = styled.p`
    position: absolute;
    right: calc(2 * var(--spacing-xs) + var(--spacing-xxxl));
    bottom: var(--spacing-xs);
    display: flex;
    height: var(--spacing-xxxl);
    align-items: center;
    margin: 0;
    pointer-events: none;
    opacity: 0;

    /* No compass token for a slow fade: twice the moderate duration. */
    transition: opacity calc(2 * var(--duration-moderate)) var(--ease-transition);

    &[data-visible='true'] {
        opacity: 1;
        transition-duration: var(--duration-quick);
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
    font-family: var(--font-family-body);
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    text-align: end;
    color: rgba(var(--center-channel-color-rgb), 0.64);
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
