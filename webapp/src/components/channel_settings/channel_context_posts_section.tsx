// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import styled from 'styled-components';

import {CloseIcon} from '@mattermost/compass-icons/components';
import {GlobalState} from '@mattermost/types/store';

import {ChannelContextPost, unpinChannelContextPost} from '@/client';
import {receivedChannelContextPosts, useChannelContextPosts} from '@/channel_context';

import {useChannelInstructionsDraft} from './channel_instructions_state';

const SNIPPET_LENGTH = 160;

// Custom section of the Agents tab listing the posts pinned to the channel's
// agent context. Unpinning acts immediately, like unpinning a message, rather
// than waiting for the tab's Save.
export const ChannelContextPostsSection = () => {
    const draft = useChannelInstructionsDraft();
    const contextPosts = useChannelContextPosts(draft?.channelId ?? '');
    const dispatch = useDispatch();
    const [unpinFailed, setUnpinFailed] = useState(false);

    if (!draft) {
        return null;
    }
    if (typeof contextPosts === 'undefined') {
        return (
            <Muted>
                <FormattedMessage defaultMessage='Loading…'/>
            </Muted>
        );
    }
    if (contextPosts === null) {
        return (
            <ErrorText>
                <FormattedMessage defaultMessage='Pinned agent context could not be loaded.'/>
            </ErrorText>
        );
    }

    const unpin = async (postId: string) => {
        try {
            setUnpinFailed(false);
            dispatch(receivedChannelContextPosts(draft.channelId, await unpinChannelContextPost(draft.channelId, postId)));
        } catch {
            setUnpinFailed(true);
        }
    };

    return (
        <Container data-testid='channel-context-posts-section'>
            <Muted>
                {contextPosts.posts.length === 0 ? (
                    <FormattedMessage defaultMessage='No posts are pinned to agent context yet.'/>
                ) : (
                    <FormattedMessage
                        defaultMessage='{count} of {max} posts pinned. Agents working in this channel receive them with every request, as optional background they use only when it is relevant.'
                        values={{count: contextPosts.posts.length, max: contextPosts.max_posts}}
                    />
                )}
            </Muted>
            {contextPosts.posts.length > 0 && (
                <List>
                    {contextPosts.posts.map((post) => (
                        <ContextPostItem
                            key={post.post_id}
                            post={post}
                            onUnpin={() => unpin(post.post_id)}
                        />
                    ))}
                </List>
            )}
            {unpinFailed && (
                <ErrorText>
                    <FormattedMessage defaultMessage='Failed to unpin the post. Please try again.'/>
                </ErrorText>
            )}
        </Container>
    );
};

const ContextPostItem = ({post, onUnpin}: {post: ChannelContextPost; onUnpin: () => void}) => {
    const intl = useIntl();
    const username = useSelector((state: GlobalState) => state.entities.users.profiles[post.user_id]?.username);
    const snippet = post.message.length > SNIPPET_LENGTH ? `${post.message.slice(0, SNIPPET_LENGTH)}…` : post.message;

    return (
        <Item data-testid='channel-context-post'>
            <ItemBody>
                {username && <Author>{`@${username}`}</Author>}
                <Snippet>{snippet}</Snippet>
            </ItemBody>
            <UnpinButton
                type='button'
                aria-label={intl.formatMessage({defaultMessage: 'Unpin from agent context'})}
                title={intl.formatMessage({defaultMessage: 'Unpin from agent context'})}
                onClick={onUnpin}
            >
                <CloseIcon size={16}/>
            </UnpinButton>
        </Item>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
`;

const Muted = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
`;

const List = styled.ul`
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    list-style: none;
`;

const Item = styled.li`
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 8px 12px;

    & + & {
        border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ItemBody = styled.div`
    display: flex;
    flex: 1;
    flex-direction: column;
    min-width: 0;
    font-size: 14px;
    line-height: 20px;
`;

const Author = styled.span`
    font-weight: 600;
`;

const Snippet = styled.span`
    overflow-wrap: anywhere;
    white-space: pre-wrap;
`;

const UnpinButton = styled.button`
    display: inline-flex;
    flex-shrink: 0;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    cursor: pointer;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: rgba(var(--center-channel-color-rgb), 0.72);
    }
`;

const ErrorText = styled.div`
    color: var(--error-text);
    font-size: 12px;
`;
