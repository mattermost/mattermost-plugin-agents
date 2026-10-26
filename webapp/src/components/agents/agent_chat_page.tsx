// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useDispatch, useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Button} from '@mattermost/compass-ui/components/button';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {getPost, updateRead} from '@/client';
import {fetchAndStoreBots, useBotlist} from '@/bots';
import {requestAgentThreadsRefresh} from '@/websocket';

import AgentChatThreadView, {isThreadViewerAvailable} from '../agent_chat/agent_chat_thread_view';
import AgentNewChat from '../agent_chat/agent_new_chat';

import AgentChatHeader from './agent_chat_header';
import AgentConfigView from './agent_config_view';
import {useAgentDMChannel} from './use_agent_dm_channel';
import {useAgentEditAccess} from './use_agent_edit_access';

const READ_AHEAD_MS = 24 * 60 * 60 * 1000;
const missingThreadMembershipMessage = 'User thread membership doesn\'t exist';

const isMissingThreadMembershipError = (error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    return message.includes(missingThreadMembershipMessage);
};

// The product URL is not team-scoped, so currentTeamId may be empty on a cold
// load. Fall back to the user's first team; '' means skip updateRead.
export const resolveReadTeamId = (state: GlobalState): string => {
    const {currentTeamId, myMembers} = state.entities.teams;
    if (currentTeamId) {
        return currentTeamId;
    }
    return Object.keys(myMembers ?? {})[0] ?? '';
};

const useMarkThreadRead = () => {
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const teamId = useSelector<GlobalState, string>(resolveReadTeamId);

    return useCallback((rootId: string) => {
        if (!teamId || !currentUserId) {
            return;
        }

        // Mark read until tomorrow so agent threads never show as unread (as the RHS does).
        updateRead(currentUserId, teamId, rootId, Date.now() + READ_AHEAD_MS).catch((error: unknown) => {
            if (isMissingThreadMembershipError(error)) {
                console.warn('Skipping AI thread read marker because thread membership is missing.', error); // eslint-disable-line no-console
                return;
            }
            console.error('Failed to update AI thread read marker:', error); // eslint-disable-line no-console
        });
    }, [currentUserId, teamId]);
};

interface AgentChatPageProps {
    botId: string;
    rootId?: string;
    toChat: (botId: string, rootId?: string) => void;
    toAll: () => void;
}

const AgentChatPage = ({botId, rootId, toChat, toAll}: AgentChatPageProps) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const {bots} = useBotlist();
    const bot = useMemo(() => bots?.find((b) => b.id === botId) ?? null, [bots, botId]);
    const dm = useAgentDMChannel(bot);
    const {access, reload: reloadAccess} = useAgentEditAccess(bot ? botId : '');
    const [settingsOpen, setSettingsOpen] = useState(false);

    // Only a thread confirmed to be a root in this agent's DM gets chat actions.
    const [validRootId, setValidRootId] = useState<string | null>(null);

    useEffect(() => {
        setSettingsOpen(false);
    }, [botId]);

    const newChat = useCallback(() => toChat(botId), [toChat, botId]);

    if (!bots) {
        return <LoadingState/>;
    }

    // Fail closed: an agent the user cannot access (or that no longer exists) is not rendered.
    if (!bot) {
        return (
            <CenteredState>
                <EmptyState
                    title={intl.formatMessage({defaultMessage: 'This agent isn\'t available'})}
                    description={intl.formatMessage({defaultMessage: 'It may have been removed, or you may not have access to it.'})}
                    action={{children: intl.formatMessage({defaultMessage: 'View all agents'}), onClick: toAll}}
                />
            </CenteredState>
        );
    }

    if (settingsOpen && access.status === 'allowed') {
        const close = () => setSettingsOpen(false);
        return (
            <SettingsFrame>
                <SettingsColumn>
                    <AgentConfigView
                        mode='edit'
                        agent={access.agent}
                        services={access.services}
                        onBack={close}
                        onSaved={() => {
                            close();
                            reloadAccess();

                            // A rename or new avatar shows up in the header and sidebar.
                            fetchAndStoreBots(dispatch).catch((e: unknown) => {
                                console.error('Failed to refresh agents after saving settings:', e); // eslint-disable-line no-console
                            });
                        }}
                        onRestored={reloadAccess}
                    />
                </SettingsColumn>
            </SettingsFrame>
        );
    }

    let body;
    if (dm.status === 'error') {
        body = (
            <CenteredState>
                <EmptyState
                    title={intl.formatMessage({defaultMessage: 'Couldn\'t open this chat'})}
                    description={intl.formatMessage({defaultMessage: 'Something went wrong while setting up the chat. Please try again later.'})}
                    action={{children: intl.formatMessage({defaultMessage: 'View all agents'}), onClick: toAll}}
                />
            </CenteredState>
        );
    } else if (dm.status === 'loading') {
        body = <LoadingState label={intl.formatMessage({defaultMessage: 'Setting up chat…'})}/>;
    } else if (rootId) {
        body = (
            <AgentThread
                key={rootId}
                channelId={dm.channelId}
                rootId={rootId}
                agentName={bot.displayName}
                onNewChat={newChat}
                toAll={toAll}
                onValidated={setValidRootId}
            />
        );
    } else {
        body = (
            <AgentNewChat
                key={bot.id}
                layout='page'
                bot={bot}
                channelId={dm.channelId}
                onCreated={(newRootId) => {
                    // The sidebar refetches its threads, plus a delayed retry for the async title.
                    requestAgentThreadsRefresh();
                    toChat(bot.id, newRootId);
                }}
            />
        );
    }

    return (
        <ChatPane
            aria-label={intl.formatMessage({defaultMessage: 'Chat with {name}'}, {name: bot.displayName})}
        >
            <AgentChatHeader
                bot={bot}
                rootId={dm.status === 'ready' && rootId && validRootId === rootId ? rootId : ''}
                onNewChat={newChat}
                onArchived={newChat}
                {...(access.status === 'allowed' ? {onOpenSettings: () => setSettingsOpen(true)} : {})}
            />
            {body}
        </ChatPane>
    );
};

const LoadingState = ({label}: {label?: string}) => (
    <CenteredState>
        <Spinner
            size='20'
            aria-label={label}
        />
        {label && <span>{label}</span>}
    </CenteredState>
);

type ThreadCheck = 'loading' | 'valid' | 'invalid' | 'error';

interface AgentThreadProps {
    channelId: string;
    rootId: string;
    agentName: string;
    onNewChat: () => void;
    toAll: () => void;
    onValidated: (rootId: string | null) => void;
}

// Renders one thread, but only after confirming the root post is a root post in
// this agent's DM channel. Fails closed on mismatch and on lookup errors.
const AgentThread = ({channelId, rootId, agentName, onNewChat, toAll, onValidated}: AgentThreadProps) => {
    const intl = useIntl();
    const store = useStore<GlobalState>();
    const markRead = useMarkThreadRead();
    const [check, setCheck] = useState<ThreadCheck>('loading');
    const [fetchedReplyCount, setFetchedReplyCount] = useState(0);

    // The host keeps the root's reply_count (and the loaded replies) current as
    // replies arrive over the websocket.
    const storeReplyCount = useSelector<GlobalState, number>((state) => Math.max(
        state.entities.posts.posts[rootId]?.reply_count ?? 0,
        state.entities.posts.postsInThread?.[rootId]?.length ?? 0,
    ));
    const replyCount = Math.max(storeReplyCount, fetchedReplyCount);

    useEffect(() => {
        let current = true;
        const isRootInChannel = (post: {channel_id: string; root_id?: string}) => post.channel_id === channelId && !post.root_id;

        // After a submit the host has already put the new post in the store.
        const cached = store.getState().entities.posts.posts[rootId];
        setFetchedReplyCount(0);
        if (cached) {
            setCheck(isRootInChannel(cached) ? 'valid' : 'invalid');
        } else {
            setCheck('loading');
            getPost(rootId).then((post) => {
                if (current) {
                    setFetchedReplyCount(post.reply_count ?? 0);
                    setCheck(isRootInChannel(post) ? 'valid' : 'invalid');
                }
            }).catch((error: unknown) => {
                if (current) {
                    console.error('Failed to load agent thread root post:', error); // eslint-disable-line no-console
                    setCheck('error');
                }
            });
        }
        return () => {
            current = false;
        };
    }, [channelId, rootId, store]);

    useEffect(() => {
        onValidated(check === 'valid' ? rootId : null);
    }, [check, rootId, onValidated]);

    // The user's thread membership only exists once the thread has a reply, so
    // marking a reply-less thread 404s and leaves the agent's first reply unread.
    // Mark once replies exist, and again whenever another arrives.
    const canMarkRead = check === 'valid' && replyCount > 0;
    useEffect(() => {
        if (canMarkRead) {
            markRead(rootId);
        }
    }, [canMarkRead, replyCount, markRead, rootId]);

    // The server can lag behind the open; mark again on switch/unmount (as the RHS does).
    const markOnLeave = useRef<(() => void) | null>(null);
    markOnLeave.current = canMarkRead ? () => markRead(rootId) : null;
    useEffect(() => () => markOnLeave.current?.(), [rootId]);

    if (check === 'loading') {
        return <LoadingState/>;
    }
    if (check !== 'valid' || !isThreadViewerAvailable) {
        return (
            <CenteredState>
                <EmptyState
                    title={intl.formatMessage({defaultMessage: 'Couldn\'t open this chat'})}
                    description={check === 'invalid' ? intl.formatMessage({defaultMessage: 'This chat doesn\'t belong to this agent, or it no longer exists.'}) : intl.formatMessage({defaultMessage: 'Something went wrong while loading this chat. Please try again later.'})}
                    action={{children: intl.formatMessage({defaultMessage: 'Start a new chat'}), onClick: onNewChat}}
                />
                <Button
                    emphasis='tertiary'
                    size='small'
                    onClick={toAll}
                >
                    {intl.formatMessage({defaultMessage: 'View all agents'})}
                </Button>
            </CenteredState>
        );
    }

    return (
        <AgentChatThreadView
            layout='page'
            testId='agent-chat-thread'
            channelId={channelId}
            rootId={rootId}
            agentName={agentName}
            isThreadView={true}
        />
    );
};

// Definite height, no own scroll: the host ThreadViewer brings its own scroller.
const ChatPane = styled.section`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
    color: var(--center-channel-color);
    background: var(--center-channel-bg);
`;

const CenteredState = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--spacing-s);
    min-height: 0;
    padding: var(--spacing-xxxxl);
`;

// Same column as the All agents editor (agents_list.tsx ContentColumn); no
// compass token covers content widths.
const SETTINGS_MAX_WIDTH = '960px';

const SettingsFrame = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-height: 0;
    height: 100%;
    overflow: hidden;
`;

const SettingsColumn = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    width: 100%;
    max-width: ${SETTINGS_MAX_WIDTH};
    min-height: 0;
    margin: 0 auto;
    padding: 0 var(--spacing-xxxl);
`;

export default AgentChatPage;
