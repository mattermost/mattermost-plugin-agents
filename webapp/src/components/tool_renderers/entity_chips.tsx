// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Entity chips resolve a channel_id / username referenced in a tool call's
// arguments to a real Mattermost entity (fetched via the store or the API) and
// render it with a type icon or avatar. This is the "resolved entity" styling:
// a chip ALWAYS carries an icon/avatar. On any resolution failure it degrades
// to plain text (the raw id/username), never to an icon-less chip, so a resolved
// entity can never be confused with a raw model-supplied value on the approval
// surface. Chips only ever render for the requester (non-requesters have no
// arguments and cannot expand the card).

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';
import {useSelector} from 'react-redux';
import {PoundIcon, GlobeIcon, LockOutlineIcon, AccountMultipleOutlineIcon, MessageTextOutlineIcon} from '@mattermost/compass-icons/components';

import {GlobalState} from '@mattermost/types/store';

import {getChannelById, getProfilesByUsernames, getProfilePictureUrl} from '@/client';

const Chip = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 6px;
    max-width: 100%;
    padding: 2px 8px 2px 6px;
    border-radius: 12px;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    font-size: 12px;
    line-height: 16px;
    color: var(--center-channel-color);
`;

const ChipLabel = styled.span`
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
`;

const ChipTeam = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

// Plain-text fallback for an unresolved entity (raw id/username). Deliberately
// NOT a chip and NOT carrying an icon.
const PlainFallback = styled.span`
    font-size: 12px;
    line-height: 18px;
    color: rgba(var(--center-channel-color-rgb), 0.9);
    overflow-wrap: anywhere;
`;

const ChannelIconWrap = styled.span`
    display: inline-flex;
    align-items: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const Avatar = styled.img`
    width: 18px;
    height: 18px;
    border-radius: 50%;
    object-fit: cover;
    background: rgba(var(--center-channel-color-rgb), 0.08);
`;

interface ResolvedChannel {
    displayName: string;
    teamName?: string;
    type?: string;
}

interface ResolvedUser {
    id: string;
    username: string;
    lastPictureUpdate: number;
}

// Module-level caches shared across all chip instances so the same channel/user
// is fetched once per session. A null value records a resolution failure so we
// don't retry a missing/inaccessible entity on every render.
const channelCache = new Map<string, ResolvedChannel | null>();
const userCache = new Map<string, ResolvedUser | null>();

const ChannelTypeIcon: React.FC<{type?: string}> = ({type}) => {
    let Icon = PoundIcon; // public channel
    if (type === 'P') {
        Icon = LockOutlineIcon;
    } else if (type === 'G') {
        Icon = AccountMultipleOutlineIcon;
    } else if (type === 'D') {
        Icon = MessageTextOutlineIcon;
    } else if (type && type !== 'O') {
        Icon = GlobeIcon; // shared/unknown
    }
    return <ChannelIconWrap><Icon size={14}/></ChannelIconWrap>;
};

interface ChannelChipProps {
    channelId?: string;

    // Fallbacks used as plain text when the channel_id cannot be resolved
    // (e.g. create_post passes the model-supplied channel/team display names).
    fallbackName?: string;
    fallbackTeam?: string;
}

export const ChannelChip: React.FC<ChannelChipProps> = ({channelId, fallbackName, fallbackTeam}) => {
    const reduxChannel = useSelector((state: GlobalState) => state.entities.channels.channels[channelId || '']);
    const reduxTeam = useSelector((state: GlobalState) => state.entities.teams.teams[reduxChannel?.team_id || '']);

    const [resolved, setResolved] = useState<ResolvedChannel | null>(
        () => (channelId ? channelCache.get(channelId) ?? null : null),
    );

    useEffect(() => {
        let cancelled = false;
        if (channelId && !reduxChannel) {
            if (channelCache.has(channelId)) {
                setResolved(channelCache.get(channelId) ?? null);
            } else {
                getChannelById(channelId).then((channel) => {
                    const value: ResolvedChannel = {
                        displayName: channel.display_name,
                        teamName: channel.team_display_name,
                        type: channel.type,
                    };
                    channelCache.set(channelId, value);
                    if (!cancelled) {
                        setResolved(value);
                    }
                }).catch(() => {
                    channelCache.set(channelId, null);
                    if (!cancelled) {
                        setResolved(null);
                    }
                });
            }
        }
        return () => {
            cancelled = true;
        };
    }, [channelId, reduxChannel]);

    const channel: ResolvedChannel | null = reduxChannel ? {
        displayName: reduxChannel.display_name,
        teamName: reduxTeam?.display_name,
        type: reduxChannel.type,
    } : resolved;

    if (channel && channel.displayName) {
        return (
            <Chip>
                <ChannelTypeIcon type={channel.type}/>
                <ChipLabel>
                    {channel.displayName}
                    {channel.teamName ? <ChipTeam>{' · ' + channel.teamName}</ChipTeam> : null}
                </ChipLabel>
            </Chip>
        );
    }

    if (fallbackName) {
        return <PlainFallback>{fallbackName}{fallbackTeam ? ' · ' + fallbackTeam : ''}</PlainFallback>;
    }
    if (channelId) {
        return <PlainFallback>{channelId}</PlainFallback>;
    }
    return null;
};

interface UserChipProps {
    username: string;
}

export const UserChip: React.FC<UserChipProps> = ({username}) => {
    const cleanUsername = username.replace(/^@/, '');

    const [resolved, setResolved] = useState<ResolvedUser | null>(
        () => userCache.get(cleanUsername) ?? null,
    );

    useEffect(() => {
        let cancelled = false;
        if (cleanUsername) {
            if (userCache.has(cleanUsername)) {
                setResolved(userCache.get(cleanUsername) ?? null);
            } else {
                getProfilesByUsernames([cleanUsername]).then((profiles) => {
                    const profile = profiles[0];
                    const value: ResolvedUser | null = profile ? {
                        id: profile.id,
                        username: profile.username,
                        lastPictureUpdate: profile.last_picture_update ?? 0,
                    } : null;
                    userCache.set(cleanUsername, value);
                    if (!cancelled) {
                        setResolved(value);
                    }
                }).catch(() => {
                    userCache.set(cleanUsername, null);
                    if (!cancelled) {
                        setResolved(null);
                    }
                });
            }
        }
        return () => {
            cancelled = true;
        };
    }, [cleanUsername]);

    if (resolved) {
        return (
            <Chip>
                <Avatar
                    src={getProfilePictureUrl(resolved.id, resolved.lastPictureUpdate)}
                    alt=''
                />
                <ChipLabel>{'@' + resolved.username}</ChipLabel>
            </Chip>
        );
    }

    return <PlainFallback>{'@' + cleanUsername}</PlainFallback>;
};
