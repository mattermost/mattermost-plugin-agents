// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';

import {getAIThreads, getUserMCPTools, getUserToolPreferences, updateRead} from '@/client';

import {useBotlist} from '@/bots';

import type {AIThread} from '@/types/ai_thread';

import {useConversationIdForThread} from '@/hooks/use_conversation_id_for_thread';

import AgentChatThreadView from '../agent_chat/agent_chat_thread_view';

import type {UserMCPServerInfo} from './tool_provider_popover';
import ThreadItem from './thread_item';
import RHSHeader from './rhs_header';

import RHSNewTab from './rhs_new_tab';

const missingThreadMembershipMessage = 'User thread membership doesn\'t exist';

function isMissingThreadMembershipError(error: unknown) {
    const message = error instanceof Error ? error.message : String(error);
    return message.includes(missingThreadMembershipMessage);
}

const ThreadsList = styled.div`
    flex: 1;
    min-height: 0;
    overflow-y: auto;
`;

const RhsContainer = styled.div`
    height: 100%;
    display: flex;
    flex-direction: column;
    min-height: 0;
`;

const twentyFourHoursInMS = 24 * 60 * 60 * 1000;

export default function RHS() {
    const dispatch = useDispatch();
    const [currentTab, setCurrentTab] = useState('new');
    const selectedPostId = useSelector((state: any) => state['plugins-' + manifest.id].selectedPostId);

    // The selected thread's root, once the store has it (the ThreadViewer loads it).
    const selectedRoot = useSelector<GlobalState, {channel_id: string; user_id: string} | null>((state) => (
        (selectedPostId && state.entities.posts.posts[selectedPostId]) || null
    ));
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const currentTeamId = useSelector<GlobalState, string>((state) => state.entities.teams.currentTeamId);

    const [threads, setThreads] = useState<AIThread[] | null>(null);
    const [disabledServers, setDisabledServers] = useState<string[]>([]);
    const [preloadedServers, setPreloadedServers] = useState<UserMCPServerInfo[]>([]);

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
        const fetchPreferences = async () => {
            try {
                const prefs = await getUserToolPreferences();
                setDisabledServers(prefs.disabled_servers || []);
            } catch {
                // Preferences unavailable, default to all enabled
            }
        };
        fetchPreferences();

        const fetchServers = async () => {
            try {
                const response = await getUserMCPTools();
                setPreloadedServers(response.servers);
            } catch {
                // Silently fail - servers will load when popover opens
            }
        };
        fetchServers();
    }, []);

    useEffect(() => {
        const fetchThreads = async () => {
            setThreads(await getAIThreads());
        };
        if (currentTab === 'threads') {
            fetchThreads();
        } else if (currentTab === 'thread' && Boolean(selectedPostId)) {
            // Update read for the thread to tomorrow. We don't really want the unreads thing to show up.
            markSelectedThreadRead(selectedPostId);
        }
        return () => {
            // Sometimes we are too fast for the server, so try again on unmount/switch.
            if (selectedPostId) {
                markSelectedThreadRead(selectedPostId);
            }
        };
    }, [currentTab, markSelectedThreadRead, selectedPostId]);

    const selectPost = useCallback((postId: string) => {
        dispatch({type: 'SELECT_AI_POST', postId});
    }, [dispatch]);

    const {bots, activeBot, setActiveBot} = useBotlist();
    const activeConversationId = useConversationIdForThread(selectedPostId);

    // No bots available - hide the RHS entirely
    if (bots && bots.length === 0) {
        return null;
    }

    let content = null;
    if (selectedPostId) {
        if (currentTab !== 'thread') {
            setCurrentTab('thread');
        }
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
                            postTitle={p.title}
                            turnCount={p.turn_count}
                            lastActivityDate={p.update_at}
                            label={bots.find((bot) => bot.id === p.bot_id)?.displayName ?? ''}
                            onClick={() => {
                                setCurrentTab('thread');
                                selectPost(p.root_post_id!);
                            }}
                        />))}
                </ThreadsList>
            );
        } else {
            content = null;
        }
    } else if (currentTab === 'new') {
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
            <RHSHeader
                currentTab={currentTab}
                setCurrentTab={setCurrentTab}
                selectPost={selectPost}
                bots={bots}
                activeBot={activeBot}
                setActiveBot={setActiveBot}
                disabledServers={disabledServers}
                onDisabledServersChange={setDisabledServers}
                preloadedServers={preloadedServers}
                activeConversationId={activeConversationId}
            />
            {content}
        </RhsContainer>
    );
}
