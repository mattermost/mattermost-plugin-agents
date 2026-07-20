// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Strict, pure parsers for the rich per-tool cards. Each returns null on any
// shape mismatch (including redacted null arguments) so the registry falls
// back to the generic ToolCard. Free of React imports for isolated testing.

import {ToolCall} from '../tool_types';

type Args = {[key: string]: unknown};

function asObject(args: ToolCall['arguments']): Args | null {
    if (args == null || typeof args !== 'object' || Array.isArray(args)) {
        return null;
    }
    return args as Args;
}

function str(value: unknown): string | undefined {
    return typeof value === 'string' && value !== '' ? value : undefined; // eslint-disable-line no-undefined
}

export interface CreatePostParsed {
    message: string;
    channelId?: string;
    channelDisplayName?: string;
    teamDisplayName?: string;
    rootId?: string;
}

export function parseCreatePost(args: ToolCall['arguments']): CreatePostParsed | null {
    const obj = asObject(args);
    if (!obj) {
        return null;
    }
    const message = str(obj.message);
    if (message === undefined) { // eslint-disable-line no-undefined
        return null;
    }
    return {
        message,
        channelId: str(obj.channel_id),
        channelDisplayName: str(obj.channel_display_name),
        teamDisplayName: str(obj.team_display_name),
        rootId: str(obj.root_id),
    };
}

export interface DmParsed {
    username?: string;
    message: string;
}

export function parseDm(args: ToolCall['arguments']): DmParsed | null {
    const obj = asObject(args);
    if (!obj) {
        return null;
    }
    const message = str(obj.message);
    if (message === undefined) { // eslint-disable-line no-undefined
        return null;
    }
    return {message, username: str(obj.username)};
}

export interface GroupMessageParsed {
    usernames: string[];
    message: string;
}

export function parseGroupMessage(args: ToolCall['arguments']): GroupMessageParsed | null {
    const obj = asObject(args);
    if (!obj) {
        return null;
    }
    const message = str(obj.message);
    if (message === undefined) { // eslint-disable-line no-undefined
        return null;
    }
    if (!Array.isArray(obj.usernames) || obj.usernames.length === 0) {
        return null;
    }
    const usernames: string[] = [];
    for (const u of obj.usernames) {
        if (typeof u !== 'string' || u === '') {
            return null;
        }
        usernames.push(u);
    }
    return {usernames, message};
}

export interface SearchFilter {
    label: string;
    value: string;
}

const searchFilterKeys: Array<[string, string]> = [
    ['team_id', 'Team'],
    ['channel_id', 'Channel'],
    ['from', 'From'],
    ['in', 'In'],
    ['before', 'Before'],
    ['after', 'After'],
];

export interface SearchPostsParsed {
    query: string;
    filters: SearchFilter[];
}

export function parseSearchPosts(args: ToolCall['arguments']): SearchPostsParsed | null {
    const obj = asObject(args);
    if (!obj) {
        return null;
    }
    const query = str(obj.query);
    if (query === undefined) { // eslint-disable-line no-undefined
        return null;
    }
    const filters: SearchFilter[] = [];
    for (const [key, label] of searchFilterKeys) {
        const value = str(obj[key]);
        if (value !== undefined) { // eslint-disable-line no-undefined
            filters.push({label, value});
        }
    }
    return {query, filters};
}

export interface SearchUsersParsed {
    term: string;
    filters: SearchFilter[];
}

export function parseSearchUsers(args: ToolCall['arguments']): SearchUsersParsed | null {
    const obj = asObject(args);
    if (!obj) {
        return null;
    }
    const term = str(obj.term);
    if (term === undefined) { // eslint-disable-line no-undefined
        return null;
    }
    const filters: SearchFilter[] = [];
    if (typeof obj.limit === 'number') {
        filters.push({label: 'Limit', value: String(obj.limit)});
    }
    return {term, filters};
}

export interface ReadPostParsed {
    postId: string;
    includeThread?: boolean;
}

export function parseReadPost(args: ToolCall['arguments']): ReadPostParsed | null {
    const obj = asObject(args);
    if (!obj) {
        return null;
    }
    const postId = str(obj.post_id);
    if (postId === undefined) { // eslint-disable-line no-undefined
        return null;
    }
    return {
        postId,
        includeThread: typeof obj.include_thread === 'boolean' ? obj.include_thread : undefined, // eslint-disable-line no-undefined
    };
}

export interface GetChannelInfoParsed {
    channelId?: string;
    channelName?: string;
    teamId?: string;
}

export function parseGetChannelInfo(args: ToolCall['arguments']): GetChannelInfoParsed | null {
    const obj = asObject(args);
    if (!obj) {
        return null;
    }
    const channelId = str(obj.channel_id);
    const channelName = str(obj.channel_name);
    const teamId = str(obj.team_id);
    if (!channelId && !channelName && !teamId) {
        return null;
    }
    return {channelId, channelName, teamId};
}
