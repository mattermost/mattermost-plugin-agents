// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';

import ConfirmationDialog from '@/components/confirmation_dialog';
import {archiveAIThread, renameAIThread} from '@/hooks/use_agent_threads';
import {invalidateConversation} from '@/hooks/use_conversation';

import AgentChatRenameDialog from './agent_chat_rename_dialog';
import {chatTitle} from './chat_title';

// Above the plugin's other modals (2000), like every confirmation sheet in the Agents views.
const ARCHIVE_DIALOG_Z_INDEX = 2000;

export interface ChatActionTarget {
    conversationId: string;

    // The chat's current title, the dialog's starting value.
    title: string;
}

interface ArchiveRequest {
    target: ChatActionTarget;

    // Runs once the chat is archived, e.g. to leave it when it is the open one.
    onArchived?: () => void;
}

export interface ChatActions {
    startRename: (target: ChatActionTarget) => void;
    renameTarget: ChatActionTarget | null;
    submitRename: (title: string) => Promise<void>;
    closeRename: () => void;

    startArchive: (target: ChatActionTarget, onArchived?: () => void) => void;
    archiveTarget: ChatActionTarget | null;
    archiving: boolean;
    archiveFailed: boolean;
    confirmArchive: () => Promise<void>;
    closeArchive: () => void;
}

// The chat actions every chat list offers (the Agents sidebar, the chat name menu, the RHS history),
// with their dialog state. Render <ChatActionsDialogs> with the result.
export function useChatActions(): ChatActions {
    const store = useStore<GlobalState>();
    const [renameTarget, setRenameTarget] = useState<ChatActionTarget | null>(null);
    const [archiveRequest, setArchiveRequest] = useState<ArchiveRequest | null>(null);
    const [archiving, setArchiving] = useState(false);
    const [archiveFailed, setArchiveFailed] = useState(false);
    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => {
            mounted.current = false;
        };
    }, []);

    const startRename = useCallback((target: ChatActionTarget) => setRenameTarget(target), []);
    const closeRename = useCallback(() => setRenameTarget(null), []);

    // The stored thread list shows the new title at once (and restores the old one if the rename
    // fails); the conversation itself is reloaded for views that read it. A failure rejects, so the
    // dialog stays open with its error.
    const submitRename = useCallback(async (title: string) => {
        if (!renameTarget) {
            return;
        }
        await renameAIThread(store, renameTarget.conversationId, title);
        invalidateConversation(renameTarget.conversationId);
        if (mounted.current) {
            setRenameTarget(null);
        }
    }, [renameTarget, store]);

    const startArchive = useCallback((target: ChatActionTarget, onArchived?: () => void) => {
        setArchiveFailed(false);
        setArchiveRequest({target, onArchived});
    }, []);
    const closeArchive = useCallback(() => {
        if (!archiving) {
            setArchiveRequest(null);
        }
    }, [archiving]);

    // The stored thread list drops the chat at once (and puts it back if the archive fails). A
    // failure keeps the dialog open with its error.
    const confirmArchive = useCallback(async () => {
        if (!archiveRequest || archiving) {
            return;
        }
        setArchiving(true);
        setArchiveFailed(false);
        try {
            await archiveAIThread(store, archiveRequest.target.conversationId);
        } catch (e) {
            console.error('Failed to archive agent chat:', e); // eslint-disable-line no-console
            if (mounted.current) {
                setArchiveFailed(true);
                setArchiving(false);
            }
            return;
        }
        if (mounted.current) {
            setArchiving(false);
            setArchiveRequest(null);
        }
        archiveRequest.onArchived?.();
    }, [archiveRequest, archiving, store]);

    return {
        startRename,
        renameTarget,
        submitRename,
        closeRename,
        startArchive,
        archiveTarget: archiveRequest?.target ?? null,
        archiving,
        archiveFailed,
        confirmArchive,
        closeArchive,
    };
}

export const ChatActionsDialogs = ({actions}: {actions: ChatActions}) => {
    const intl = useIntl();

    // Keeps the title in the dialog while its exit animation runs.
    const lastArchiveTitle = useRef('');
    if (actions.archiveTarget) {
        lastArchiveTitle.current = chatTitle(intl, actions.archiveTarget.title);
    }

    return (
        <>
            <AgentChatRenameDialog
                show={actions.renameTarget !== null}
                initialTitle={actions.renameTarget?.title ?? ''}
                onSubmit={actions.submitRename}
                onClose={actions.closeRename}
            />
            <ConfirmationDialog
                show={actions.archiveTarget !== null}
                titleId='agent-chat-archive-dialog-title'
                title={intl.formatMessage({defaultMessage: 'Archive chat'})}
                message={(
                    <>
                        <p>
                            {intl.formatMessage(
                                {defaultMessage: 'Archive "{title}"? It will be removed from your chats and can no longer be opened from here.'},
                                {title: lastArchiveTitle.current},
                            )}
                        </p>
                        {actions.archiveFailed && (
                            <ArchiveError role='alert'>
                                {intl.formatMessage({defaultMessage: 'Couldn\'t archive this chat. Try again.'})}
                            </ArchiveError>
                        )}
                    </>
                )}
                confirmButtonText={intl.formatMessage({defaultMessage: 'Archive'})}
                cancelButtonText={intl.formatMessage({defaultMessage: 'Cancel'})}
                onConfirm={actions.confirmArchive}
                onCancel={actions.closeArchive}
                isDestructive={true}
                confirmPending={actions.archiving}
                managedAccessibility={true}
                zIndex={ARCHIVE_DIALOG_Z_INDEX}
            />
        </>
    );
};

const ArchiveError = styled.p`
    margin: var(--spacing-xs) 0 0;
    color: var(--error-text);
`;
