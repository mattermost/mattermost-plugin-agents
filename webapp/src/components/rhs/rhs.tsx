// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import type {UserProfile} from '@mattermost/types/users';

import manifest from '@/manifest';
import type {RHSTab} from '@/redux';

import {archiveConversation, getAIThreads, getPostsByIds, getProfilePictureUrl, renameConversation, updateRead} from '@/client';
import AgentChatRenameDialog from '@/components/agents/agent_chat_rename_dialog';
import ConfirmationDialog from '@/components/confirmation_dialog';
import {requestAgentThreadsRefresh} from '@/websocket';

import {useBotlist} from '@/bots';

import type {AIThread} from '@/types/ai_thread';

import AgentChatDropZone from '../agent_chat/agent_chat_drop_zone';
import AgentChatThreadView from '../agent_chat/agent_chat_thread_view';

import ThreadItem from './thread_item';

import RHSNewTab from './rhs_new_tab';

const missingThreadMembershipMessage = 'User thread membership doesn\'t exist';

function isMissingThreadMembershipError(error: unknown) {
    const message = error instanceof Error ? error.message : String(error);
    return message.includes(missingThreadMembershipMessage);
}

const ThreadsList = styled.div`
    flex: 1;
    min-height: 0;
    padding: var(--spacing-xs) 0;
    overflow-y: auto;
`;

const RhsContainer = styled.div`
    height: 100%;
    display: flex;
    flex-direction: column;
    min-height: 0;
`;

type ChatPreview = {message: string; replyCount: number};

// The chats are the user's own, so the author is always the current user.
const chatAuthorName = (user?: UserProfile) => (
    `${user?.first_name ?? ''} ${user?.last_name ?? ''}`.trim() || user?.username || ''
);

// Above the plugin's other modals (2000), like the other archive confirmations.
const ARCHIVE_DIALOG_Z_INDEX = 2000;

const ErrorText = styled.p`
    margin: 0;
    color: var(--error-text);
`;

const twentyFourHoursInMS = 24 * 60 * 60 * 1000;

export default function RHS() {
    const dispatch = useDispatch();
    const selectedPostId = useSelector((state: any) => state['plugins-' + manifest.id].selectedPostId);

    // The list view (new chat or history) is shared with the header title, which is rendered by
    // the host outside this component (it shows Back and the tools button by it). An open
    // thread takes over the pane whichever list view was last chosen.
    const listTab = useSelector<any, RHSTab>((state) => state['plugins-' + manifest.id].rhsTab ?? 'new');
    const currentTab = selectedPostId ? 'thread' : listTab;
    const setCurrentTab = useCallback((tab: string) => {
        // 'thread' is implied by selecting a post.
        if (tab !== 'thread') {
            dispatch({type: 'AI_RHS_SET_TAB', tab});
        }
    }, [dispatch]);

    // The selected thread's root, once the store has it (the ThreadViewer loads it).
    const selectedRoot = useSelector<GlobalState, {channel_id: string; user_id: string} | null>((state) => (
        (selectedPostId && state.entities.posts.posts[selectedPostId]) || null
    ));
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const currentTeamId = useSelector<GlobalState, string>((state) => state.entities.teams.currentTeamId);

    const intl = useIntl();
    const [threads, setThreads] = useState<AIThread[] | null>(null);

    // The chat the history's hover actions are archiving, once confirmed.
    const [archiveTarget, setArchiveTarget] = useState<AIThread | null>(null);
    const [renameTarget, setRenameTarget] = useState<AIThread | null>(null);
    const renameThread = useCallback(async (title: string) => {
        if (!renameTarget) {
            return;
        }
        await renameConversation(renameTarget.id, title);
        const renamedId = renameTarget.id;
        setThreads((current) => (current ? current.map((t) => (t.id === renamedId ? {...t, title} : t)) : current));
        setRenameTarget(null);

        // The Agents product's sidebar lists the same chats.
        requestAgentThreadsRefresh();
    }, [renameTarget]);
    const [archiving, setArchiving] = useState(false);
    const [archiveFailed, setArchiveFailed] = useState(false);
    const confirmArchive = useCallback(async () => {
        if (!archiveTarget || archiving) {
            return;
        }
        setArchiving(true);
        setArchiveFailed(false);
        try {
            await archiveConversation(archiveTarget.id);
            const archivedId = archiveTarget.id;
            setThreads((current) => (current ? current.filter((t) => t.id !== archivedId) : current));
            setArchiveTarget(null);

            // The Agents product's sidebar lists the same chats.
            requestAgentThreadsRefresh();
        } catch (e) {
            console.error('Failed to archive agent chat:', e); // eslint-disable-line no-console
            setArchiveFailed(true);
        } finally {
            setArchiving(false);
        }
    }, [archiveTarget, archiving]);

    // The chats' first messages and reply counts, loaded after the list. Until they arrive (or if
    // they cannot be loaded) the items show the chat title.
    const [previews, setPreviews] = useState<Record<string, ChatPreview>>({});
    const currentUser = useSelector<GlobalState, UserProfile | undefined>((state) => state.entities.users.profiles[currentUserId]);

    const markSelectedThreadRead = useCallback((postId: string) => {
        updateRead(currentUserId, currentTeamId, postId, Date.now() + twentyFourHoursInMS).catch((error) => {
            if (isMissingThreadMembershipError(error)) {
                console.warn('Skipping AI thread read marker because thread membership is missing.', error); // eslint-disable-line no-console
                return;
            }

            console.error('Failed to update AI thread read marker:', error); // eslint-disable-line no-console
        });
    }, [currentTeamId, currentUserId]);

    useEffect(() => {
        // Responses of a superseded request (the tab was left, or opened again) are dropped.
        let superseded = false;
        const fetchThreads = async () => {
            const list: AIThread[] = await getAIThreads();
            if (superseded) {
                return;
            }
            setThreads(list);

            const rootIds = list.map((t) => t.root_post_id).filter((id): id is string => Boolean(id));
            if (rootIds.length === 0) {
                setPreviews({});
                return;
            }
            try {
                const posts = await getPostsByIds(rootIds);
                if (!superseded) {
                    setPreviews(Object.fromEntries(posts.map((post) => [post.id, {message: post.message, replyCount: post.reply_count}])));
                }
            } catch (error) {
                console.error('Failed to load chat history previews:', error); // eslint-disable-line no-console
            }
        };
        if (currentTab === 'threads') {
            fetchThreads();
        } else if (currentTab === 'thread' && Boolean(selectedPostId)) {
            // Update read for the thread to tomorrow. We don't really want the unreads thing to show up.
            markSelectedThreadRead(selectedPostId);
        }
        return () => {
            superseded = true;

            // Sometimes we are too fast for the server, so try again on unmount/switch.
            if (selectedPostId) {
                markSelectedThreadRead(selectedPostId);
            }
        };
    }, [currentTab, markSelectedThreadRead, selectedPostId]);

    const selectPost = useCallback((postId: string) => {
        dispatch({type: 'SELECT_AI_POST', postId});
    }, [dispatch]);

    const {bots, activeBot} = useBotlist();

    // No bots available - hide the RHS entirely
    if (bots && bots.length === 0) {
        return null;
    }

    let content = null;

    // The new chat and a thread take files dropped anywhere on the pane.
    let dropZone = false;
    if (selectedPostId) {
        dropZone = true;
        const channelId = selectedRoot?.channel_id ?? '';

        // The agent of the thread: the DM partner, or the root's author for
        // bot-started threads (e.g. channel analysis).
        const threadBot = bots?.find((b) => (channelId && b.dmChannelID === channelId) || b.id === selectedRoot?.user_id);
        content = (
            <AgentChatThreadView
                layout='panel'
                testId='rhs-thread'
                viewerTestId='rhs-thread-viewer'
                channelId={channelId}
                rootId={selectedPostId}
                agentName={threadBot?.displayName ?? ''}
                bot={threadBot ?? null}
                isThreadView={false}
            />
        );
    } else if (currentTab === 'threads') {
        if (threads && bots) {
            const navigableThreads = threads.filter((p) => p.root_post_id);
            content = (
                <ThreadsList
                    data-testid='rhs-threads-list'
                >
                    {navigableThreads.map((p) => (
                        <ThreadItem
                            key={p.id}
                            preview={previews[p.root_post_id!]?.message || p.title}
                            replyCount={previews[p.root_post_id!]?.replyCount}
                            authorName={chatAuthorName(currentUser)}
                            authorAvatarUrl={getProfilePictureUrl(currentUserId, currentUser?.last_picture_update ?? 0)}
                            agentName={bots.find((bot) => bot.id === p.bot_id)?.displayName ?? ''}
                            agentAvatarUrl={getProfilePictureUrl(p.bot_id, bots.find((bot) => bot.id === p.bot_id)?.lastIconUpdate ?? 0)}
                            lastActivityDate={p.update_at}
                            onClick={() => selectPost(p.root_post_id!)}
                            onRename={() => setRenameTarget(p)}
                            onArchive={() => {
                                setArchiveFailed(false);
                                setArchiveTarget(p);
                            }}
                        />))}
                </ThreadsList>
            );
        } else {
            content = null;
        }
    } else if (currentTab === 'new') {
        dropZone = true;
        content = (
            <RHSNewTab
                setCurrentTab={setCurrentTab}
                selectPost={selectPost}
                activeBot={activeBot}
            />
        );
    }
    return (
        <RhsContainer
            data-testid='mattermost-ai-rhs'
        >
            {dropZone ? (
                <AgentChatDropZone testId='rhs-file-drop-zone'>{content}</AgentChatDropZone>
            ) : content}
            <AgentChatRenameDialog
                show={renameTarget !== null}
                initialTitle={renameTarget?.title ?? ''}
                onSubmit={renameThread}
                onClose={() => setRenameTarget(null)}
            />
            <ConfirmationDialog
                show={archiveTarget !== null}
                titleId='rhs-archive-dialog-title'
                title={intl.formatMessage({defaultMessage: 'Archive chat'})}
                message={(
                    <>
                        <p>
                            {intl.formatMessage({defaultMessage: 'Archive this chat? It will be removed from your chats and can no longer be opened from here.'})}
                        </p>
                        {archiveFailed && (
                            <ErrorText role='alert'>
                                {intl.formatMessage({defaultMessage: 'Couldn\'t archive this chat. Try again.'})}
                            </ErrorText>
                        )}
                    </>
                )}
                confirmButtonText={intl.formatMessage({defaultMessage: 'Archive'})}
                onConfirm={confirmArchive}
                onCancel={() => {
                    if (!archiving) {
                        setArchiveTarget(null);
                    }
                }}
                isDestructive={true}
                confirmPending={archiving}
                managedAccessibility={true}
                zIndex={ARCHIVE_DIALOG_Z_INDEX}
            />
        </RhsContainer>
    );
}
