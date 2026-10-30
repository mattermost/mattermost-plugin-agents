// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback, useMemo} from 'react';
import {useDispatch, useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';
import type {RHSTab} from '@/redux';

import {getPostsByIds, getProfilePictureUrl} from '@/client';
import {ChatActionsDialogs, useChatActions} from '@/components/agents/use_chat_actions';
import {useMarkAgentThreadRead} from '@/hooks/use_agent_thread_read';
import {fetchAIThreads, selectAIThreadsState, useAgentThreads} from '@/hooks/use_agent_threads';

import {useBotlist} from '@/bots';

import type {AIThread} from '@/types/ai_thread';

import AgentChatDropZone from '../agent_chat/agent_chat_drop_zone';
import AgentChatThreadView from '../agent_chat/agent_chat_thread_view';

import ChatActionsPopover from './chat_actions_popover';
import RHSViewTransition from './rhs_view_transition';
import ThreadItem from './thread_item';

import RHSNewTab from './rhs_new_tab';

const ThreadsList = styled.div`
    flex: 1;
    min-height: 0;
    padding: var(--spacing-xs);
    overflow-y: auto;
`;

const RhsContainer = styled.div`
    height: 100%;
    display: flex;
    flex-direction: column;
    min-height: 0;
`;

type ChatPreview = {message: string; replyCount: number};

export default function RHS() {
    const dispatch = useDispatch();
    const store = useStore<GlobalState>();
    const selectedPostId: string = useSelector((state: any) => state['plugins-' + manifest.id].selectedPostId) || '';

    // The open chat is read, as in any thread view.
    useMarkAgentThreadRead(selectedPostId, Boolean(selectedPostId));

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
    const {bots, activeBot} = useBotlist();

    // Every chat of the user's, from the shared stored list; opening the history refetches it.
    const {threads} = useAgentThreads(bots);
    const chatActions = useChatActions();

    // The history item whose actions menu is open, and the button it opened from.
    const [menuFor, setMenuFor] = useState<{thread: AIThread; anchor: HTMLElement} | null>(null);

    // The chats' first messages and reply counts, loaded after the list. Until they arrive (or if
    // they cannot be loaded) the items show the chat title.
    const [previews, setPreviews] = useState<Record<string, ChatPreview>>({});

    const showingHistory = currentTab === 'threads';
    useEffect(() => {
        // A load already under way (the first one) is fresh enough.
        if (showingHistory && !selectAIThreadsState(store.getState()).loading) {
            fetchAIThreads(store.dispatch);
        }
    }, [showingHistory, store]);

    const rootIdsKey = useMemo(() => (threads ?? []).map((t) => t.root_post_id).filter(Boolean).join(','), [threads]);
    useEffect(() => {
        if (!showingHistory || !rootIdsKey) {
            return () => null;
        }

        // Responses of a superseded request (the tab was left, or the list changed) are dropped.
        let superseded = false;
        getPostsByIds(rootIdsKey.split(',')).then((posts) => {
            if (!superseded) {
                setPreviews(Object.fromEntries(posts.map((post) => [post.id, {message: post.message, replyCount: post.reply_count}])));
            }
        }).catch((error: unknown) => {
            console.error('Failed to load chat history previews:', error); // eslint-disable-line no-console
        });
        return () => {
            superseded = true;
        };
    }, [showingHistory, rootIdsKey]);

    const selectPost = useCallback((postId: string) => {
        dispatch({type: 'SELECT_AI_POST', postId});
    }, [dispatch]);

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
                            title={p.title}
                            preview={previews[p.root_post_id!]?.message ?? ''}
                            replyCount={previews[p.root_post_id!]?.replyCount}
                            agentName={bots.find((bot) => bot.id === p.bot_id)?.displayName ?? ''}
                            agentAvatarUrl={getProfilePictureUrl(p.bot_id, bots.find((bot) => bot.id === p.bot_id)?.lastIconUpdate ?? 0)}
                            lastActivityDate={p.update_at}
                            onClick={() => selectPost(p.root_post_id!)}
                            onMenuClick={(anchor) => setMenuFor({thread: p, anchor})}
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
            <RHSViewTransition
                viewKey={selectedPostId ? `thread:${selectedPostId}` : currentTab}
                rank={currentTab === 'threads' ? 0 : 1}
            >
                {dropZone ? (
                    <AgentChatDropZone testId='rhs-file-drop-zone'>{content}</AgentChatDropZone>
                ) : content}
            </RHSViewTransition>
            <ChatActionsPopover
                anchor={menuFor?.anchor ?? null}
                onClose={() => setMenuFor(null)}
                onRename={() => menuFor && chatActions.startRename({conversationId: menuFor.thread.id, title: menuFor.thread.title})}
            />
            <ChatActionsDialogs actions={chatActions}/>
        </RhsContainer>
    );
}
