// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useStore} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {renameAIThread} from '@/hooks/use_agent_threads';
import {invalidateConversation} from '@/hooks/use_conversation';

import AgentChatRenameDialog from './agent_chat_rename_dialog';

export interface ChatActionTarget {
    conversationId: string;

    // The chat's current title, the dialog's starting value.
    title: string;
}

export interface ChatActions {
    startRename: (target: ChatActionTarget) => void;
    renameTarget: ChatActionTarget | null;
    submitRename: (title: string) => Promise<void>;
    closeRename: () => void;
}

// The chat actions every chat list offers (the Agents sidebar, the chat name menu, the RHS history),
// with their dialog state. Render <ChatActionsDialogs> with the result.
export function useChatActions(): ChatActions {
    const store = useStore<GlobalState>();
    const [renameTarget, setRenameTarget] = useState<ChatActionTarget | null>(null);
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

    return {startRename, renameTarget, submitRename, closeRename};
}

export const ChatActionsDialogs = ({actions}: {actions: ChatActions}) => (
    <AgentChatRenameDialog
        show={actions.renameTarget !== null}
        initialTitle={actions.renameTarget?.title ?? ''}
        onSubmit={actions.submitRename}
        onClose={actions.closeRename}
    />
);
