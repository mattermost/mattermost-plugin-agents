// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useSelector, useStore} from 'react-redux';
import styled, {createGlobalStyle} from 'styled-components';

import type {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {setAgentDMOpenChat} from '@/agent_dm_open_chat';
import {dmOtherUserId} from '@/agent_dm_view';
import {useBotlist} from '@/bots';
import {getPost} from '@/client';

import AgentChatPage from './agent_chat_page';
import {dmChannelPath, dmPostId, navigationAction} from './agent_dm_route';
import {ALL_AGENTS_PATH, buildChatPath, getHistory} from './use_agents_route';

// The props the host passes a registerChannelViewComponent component.
export interface AgentDMChannelViewProps {
    channel: Channel;
    channelId: string;
    teamId: string;

    // A permalinked post in this DM (`/:team/messages/@agent/:postid`). The host keeps it after the
    // id leaves the URL, until the channel changes; the view opens it when it changes.
    focusedPostId?: string;
}

export {dmChannelPath};

// The DM URL for another agent (its username), on the same team as `dmPath`.
const otherAgentDMPath = (dmPath: string, username: string): string => {
    const team = dmPath.split('/')[1];
    return `/${team}/messages/@${encodeURIComponent(username)}`;
};

// What the chat pane shows: a chat (its root post id), a new chat (undefined), or, while a
// permalinked post is being looked up, nothing yet.
type Selection = {resolving: true} | {resolving: false; rootId?: string};

// An agent's DM in the Channels app, shown as the Agents product's chat under the chat's own title bar
// in place of the host's channel header. The host keeps its LHS and RHS around it, and marks the DM
// read when this mounts. The agent's chats are listed under its item in the host's sidebar.
//
// The open chat is this component's own state, with one entry point, `open`. Requests to open a chat
// come from the DM URL when the view mounts and on each navigation that names a post (the sidebar's
// chats, permalinks, back/forward), from the host's focusedPostId when it changes (all the popout
// has), and from the chat pane itself. The URL (`/:team/messages/@agent/:postId`, which the host's
// channel route accepts without reloading the channel) is written from here so a chat survives a
// reload and can be linked, but is not read back to decide what is open, because the host rewrites
// it by itself: it drops a permalink's post id a few seconds after opening it. A permalink may name
// a reply, whose chat is its thread.
const AgentDMChannelViewForChannel = ({channel, focusedPostId}: AgentDMChannelViewProps) => {
    const intl = useIntl();
    const store = useStore<GlobalState>();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const {bots} = useBotlist();
    const botId = dmOtherUserId(channel.name, currentUserId);
    const channelId = channel.id;

    const [initialPostId] = useState(() => dmPostId(getHistory()?.location?.pathname) ?? focusedPostId ?? '');
    const [selection, setSelection] = useState<Selection>(initialPostId ? {resolving: true} : {resolving: false});

    // The post id last asked for ('' is a new chat), so the same request twice (the URL and
    // focusedPostId both reporting one permalink, the URL echoing a pick made here) is not a second one.
    const requested = useRef<string | null>(null);

    // Bumped by every request, so a permalink lookup that resolves after a newer one is dropped.
    const requestSeq = useRef(0);

    const mounted = useRef(false);

    // knownRootId is given when the chat is already known (picked here), so there is nothing to look up.
    const open = useCallback((postId: string, knownRootId?: string) => {
        if (postId === requested.current) {
            return;
        }
        requested.current = postId;
        const seq = ++requestSeq.current;
        if (!postId) {
            setSelection({resolving: false});
            return;
        }
        const select = (rootId: string) => {
            if (mounted.current && seq === requestSeq.current) {
                setSelection({resolving: false, rootId});
            }
        };

        // The chat is the post's thread. Whether it belongs to this agent's DM is checked by the chat
        // pane, which fails closed.
        if (knownRootId) {
            select(knownRootId);
            return;
        }
        const cached = store.getState().entities.posts.posts[postId];
        if (cached) {
            select(cached.root_id || cached.id);
            return;
        }
        setSelection({resolving: true});
        getPost(postId).then((post) => {
            // The sidebar's nested chats read the post from the store to find the chat to highlight.
            store.dispatch({type: 'RECEIVED_POST', data: post});
            select(post.root_id || post.id);
        }).catch((error: unknown) => {
            console.error('Failed to load the linked agent chat post:', error); // eslint-disable-line no-console
            select(postId);
        });
    }, [store]);

    useEffect(() => {
        mounted.current = true;
        open(initialPostId);
        const unlisten = getHistory()?.listen?.((...args: unknown[]) => {
            const urlPostId = dmPostId(getHistory()?.location?.pathname);

            // A replace that drops the post id is the host tidying a permalink's URL; the chat stays.
            if (urlPostId === null || (!urlPostId && navigationAction(args) === 'REPLACE')) {
                return;
            }
            open(urlPostId);
        });
        return () => {
            mounted.current = false;
            if (typeof unlisten === 'function') {
                unlisten();
            }
        };
    }, [open, initialPostId]);

    const lastFocusedPostId = useRef(focusedPostId);
    useEffect(() => {
        if (focusedPostId && focusedPostId !== lastFocusedPostId.current) {
            open(focusedPostId);
        }
        lastFocusedPostId.current = focusedPostId;
    }, [focusedPostId, open]);

    const openRootId = selection.resolving ? '' : selection.rootId ?? '';
    useEffect(() => {
        store.dispatch(setAgentDMOpenChat(channelId, openRootId));
        return () => {
            store.dispatch(setAgentDMOpenChat(channelId, ''));
        };
    }, [store, channelId, openRootId]);

    const toChat = useCallback((targetBotId: string, rootId?: string) => {
        const history = getHistory();
        const dmPath = dmChannelPath(history?.location?.pathname);

        // Another agent's chat: that agent's DM (or the Agents product outside the Channels route).
        if (targetBotId !== botId) {
            const target = bots?.find((b) => b.id === targetBotId);
            history?.push(dmPath && target ? otherAgentDMPath(dmPath, target.username) : buildChatPath(targetBotId, rootId));
            return;
        }

        open(rootId ?? '', rootId);
        const path = rootId ? `${dmPath}/${encodeURIComponent(rootId)}` : dmPath;
        if (dmPath && path !== history?.location?.pathname) {
            history?.push(path);
        }
    }, [botId, bots, open]);

    const toAll = useCallback(() => getHistory()?.push(ALL_AGENTS_PATH), []);

    const rootId = selection.resolving ? '' : selection.rootId ?? '';

    return (
        <Layout data-testid='agent-dm-channel-view'>
            <HideHostChannelHeader/>
            <ChatColumn>
                {selection.resolving ? (
                    <Centered>
                        <Spinner
                            size='20'
                            aria-label={intl.formatMessage({defaultMessage: 'Opening chat…'})}
                        />
                    </Centered>
                ) : (
                    <AgentChatPage
                        botId={botId}
                        rootId={rootId}
                        toChat={toChat}
                        toAll={toAll}
                    />
                )}
            </ChatColumn>
        </Layout>
    );
};

const Layout = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: row;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
`;

// The host's channel header would sit above the chat's own title bar; the chat view replaces it
// while it is mounted (the host renders it for every channel view).
const HideHostChannelHeader = createGlobalStyle`
    #app-content > #channel-header {
        display: none;
    }
`;

const ChatColumn = styled.div`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
`;

const Centered = styled.div`
    display: flex;
    flex: 1 1 auto;
    align-items: center;
    justify-content: center;
`;

// Keyed by channel so that the open chat and any pending permalink lookup never carry over to another DM.
const AgentDMChannelView = (props: AgentDMChannelViewProps) => (
    <AgentDMChannelViewForChannel
        key={props.channel.id}
        {...props}
    />
);

export default AgentDMChannelView;
