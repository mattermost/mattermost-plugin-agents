// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {getChannelById, getProfilesByIds} from '@/client';
import {bareToolName} from '@/utils/tool_identity';

import {ToolCall} from '../tool_types';
import ToolCard from '../tool_card';
import ToolArguments from '../tool_arguments';

import ToolCardShell, {RichCardProps} from './tool_card_shell';
import {isAwaitingDecision} from './posts/common';

const Summary = styled.div`
    margin-top: 12px;
    font-size: 13px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    overflow-wrap: anywhere;
`;

// Plain text, never formatText/markdown — matching the generic argument list.
const ResolvedName = styled.span`
    font-weight: 600;
    color: var(--center-channel-color);
`;

// The bare name of the membership tool that removes rather than adds.
const RemoveMemberTool = 'remove_channel_member';

interface MembershipParsed {
    channelId: string;
    userIds: string[];
}

/**
 * Read the channel and the member(s) out of a membership tool's arguments.
 * The singular tools carry user_id, the bulk tool carries user_ids; anything
 * that doesn't match that shape exactly yields null so the caller renders the
 * generic card.
 */
function parseMembership(args: ToolCall['arguments']): MembershipParsed | null {
    if (args == null || typeof args !== 'object' || Array.isArray(args)) {
        return null;
    }
    const obj = args as {[key: string]: unknown};

    const channelId = obj.channel_id;
    if (typeof channelId !== 'string' || channelId === '') {
        return null;
    }

    const userIds: string[] = [];
    if ('user_id' in obj) {
        if (typeof obj.user_id !== 'string' || obj.user_id === '') {
            return null;
        }
        userIds.push(obj.user_id);
    }
    if ('user_ids' in obj) {
        if (!Array.isArray(obj.user_ids)) {
            return null;
        }
        for (const userId of obj.user_ids) {
            if (typeof userId !== 'string' || userId === '') {
                return null;
            }
            userIds.push(userId);
        }
    }
    if (userIds.length === 0) {
        return null;
    }

    return {channelId, userIds};
}

/**
 * Card for the channel membership tools (add_channel_member,
 * add_channel_members, remove_channel_member): while the call awaits a
 * decision, it resolves the member and channel IDs in the arguments to the
 * username(s) and channel display name they point at, so the card names who
 * is affected and where. Names come from the store when it already holds
 * them and are fetched otherwise. Falls back to the generic card when the
 * arguments don't parse, when the channel can't be resolved, or when the call
 * has already executed.
 */
const ChannelMembershipCard: React.FC<RichCardProps> = (props) => {
    const parsed = useMemo(() => parseMembership(props.tool.arguments), [props.tool.arguments]);
    const awaiting = isAwaitingDecision(props.tool);
    const resolving = Boolean(parsed) && awaiting;

    const profiles = useSelector((state: GlobalState) => state.entities.users.profiles);
    const channels = useSelector((state: GlobalState) => state.entities.channels.channels);

    const [fetchedUsernames, setFetchedUsernames] = useState<{[id: string]: string}>({});
    const [fetchedChannelName, setFetchedChannelName] = useState('');
    const [channelFailed, setChannelFailed] = useState(false);

    const storedChannel = resolving && parsed ? channels[parsed.channelId] : null;
    const storedChannelName = storedChannel ? (storedChannel.display_name || storedChannel.name) : '';

    // The IDs the store can't name, joined so the effect depends on a value
    // that only changes when the set of unnamed members does.
    const unnamedUserKey = useMemo(() => {
        if (!resolving || !parsed) {
            return '';
        }
        return parsed.userIds.filter((userId) => !profiles[userId]?.username).join(',');
    }, [resolving, parsed, profiles]);

    useEffect(() => {
        let cancelled = false;
        if (unnamedUserKey) {
            getProfilesByIds(unnamedUserKey.split(',')).then((fetched) => {
                if (!cancelled) {
                    setFetchedUsernames((prev) => {
                        const next = {...prev};
                        for (const profile of fetched) {
                            next[profile.id] = profile.username;
                        }
                        return next;
                    });
                }
            }).catch(() => {
                // A member left unnamed keeps its ID in the summary.
            });
        }
        return () => {
            cancelled = true;
        };
    }, [unnamedUserKey]);

    const channelToFetch = resolving && parsed && !storedChannelName ? parsed.channelId : '';

    useEffect(() => {
        let cancelled = false;
        if (channelToFetch) {
            getChannelById(channelToFetch).then((channel) => {
                if (!cancelled) {
                    setFetchedChannelName(channel.display_name || channel.name);
                }
            }).catch(() => {
                if (!cancelled) {
                    setChannelFailed(true);
                }
            });
        }
        return () => {
            cancelled = true;
        };
    }, [channelToFetch]);

    if (!parsed || !awaiting || channelFailed) {
        return <ToolCard {...props}/>;
    }

    const channelName = storedChannelName || fetchedChannelName;
    const memberNames = parsed.userIds.
        map((userId) => profiles[userId]?.username || fetchedUsernames[userId] || userId).
        join(', ');
    const nameChunks = (chunks: React.ReactNode) => <ResolvedName>{chunks}</ResolvedName>;

    return (
        <ToolCardShell {...props}>
            {channelName ? (
                <Summary>
                    {bareToolName(props.tool) === RemoveMemberTool ? (
                        <FormattedMessage
                            id='ai.tool_call.channel_membership.remove'
                            defaultMessage='Remove <name>{members}</name> from <name>{channel}</name>'
                            values={{members: memberNames, channel: channelName, name: nameChunks}}
                        />
                    ) : (
                        <FormattedMessage
                            id='ai.tool_call.channel_membership.add'
                            defaultMessage='Add <name>{members}</name> to <name>{channel}</name>'
                            values={{members: memberNames, channel: channelName, name: nameChunks}}
                        />
                    )}
                </Summary>
            ) : (
                <ToolArguments arguments={props.tool.arguments}/>
            )}
        </ToolCardShell>
    );
};

export default ChannelMembershipCard;
