// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useState} from 'react';
import {useIntl} from 'react-intl';
import {useDispatch, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Button} from '@mattermost/compass-ui/components/button';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {getPost} from '@/client';
import {fetchAndStoreBots, useBotlist} from '@/bots';
import {useMarkAgentThreadRead} from '@/hooks/use_agent_thread_read';
import {useRefreshAgentThreads} from '@/hooks/use_agent_threads';

import AgentChatDropZone from '../agent_chat/agent_chat_drop_zone';
import AgentChatThreadView, {isThreadViewerAvailable} from '../agent_chat/agent_chat_thread_view';
import AgentNewChat from '../agent_chat/agent_new_chat';
import ChatSetupStatus from '../agent_chat/chat_setup_status';

import AgentChatTitleBar from './agent_chat_title_bar';
import AgentSettingsModal from './agent_settings_modal';
import {useAgentDMChannel} from './use_agent_dm_channel';
import {useAgentEditAccess} from './use_agent_edit_access';

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
    const refreshThreads = useRefreshAgentThreads();

    // Only a thread confirmed to be a root in this agent's DM gets chat actions.
    const [validRootId, setValidRootId] = useState<string | null>(null);

    useEffect(() => {
        setSettingsOpen(false);
    }, [botId]);

    const newChat = useCallback(() => toChat(botId), [toChat, botId]);

    const closeSettings = useCallback(() => setSettingsOpen(false), []);
    const handleSettingsSaved = useCallback(() => {
        closeSettings();
        reloadAccess();

        // A rename or new avatar shows up in the header and sidebar.
        fetchAndStoreBots(dispatch).catch((e: unknown) => {
            console.error('Failed to refresh agents after saving settings:', e); // eslint-disable-line no-console
        });
    }, [closeSettings, reloadAccess, dispatch]);

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
        body = <ChatSetupStatus/>;
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
                    // The chat lists pick up the new chat; its title follows over the websocket.
                    refreshThreads();
                    toChat(bot.id, newRootId);
                }}
            />
        );
    }

    // The settings open over the chat, so its state (the composer draft, the scroll position) stays.
    return (
        <>
            <ChatPane
                aria-label={intl.formatMessage({defaultMessage: 'Chat with {name}'}, {name: bot.displayName})}
            >
                <AgentChatTitleBar
                    surface='page'
                    bot={bot}
                    rootId={dm.status === 'ready' && rootId && validRootId === rootId ? rootId : ''}
                    inThread={Boolean(rootId)}
                    bots={bots}
                    onSwitchAgent={(other) => toChat(other.id)}
                    onSelectChat={(chatRootId) => toChat(bot.id, chatRootId)}
                    onNewChat={newChat}
                    onViewAll={toAll}
                    {...(access.status === 'allowed' ? {onOpenSettings: () => setSettingsOpen(true)} : {})}
                />
                <AgentChatDropZone testId='agent-chat-file-drop-zone'>
                    {body}
                </AgentChatDropZone>
            </ChatPane>
            {access.status === 'allowed' && (
                <AgentSettingsModal
                    show={settingsOpen}
                    agent={access.agent}
                    services={access.services}
                    onClose={closeSettings}
                    onSaved={handleSettingsSaved}
                    onRestored={reloadAccess}
                />
            )}
        </>
    );
};

const LoadingState = () => (
    <CenteredState>
        <Spinner size='20'/>
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
    const [check, setCheck] = useState<ThreadCheck>('loading');
    const [fetchedReplyCount, setFetchedReplyCount] = useState(0);

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

    useMarkAgentThreadRead(rootId, check === 'valid', fetchedReplyCount);

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

export default AgentChatPage;
