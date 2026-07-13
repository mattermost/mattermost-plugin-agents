// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Rich per-tool cards for high-traffic embedded Mattermost tools. Each has a
// strict parse<X>() that returns null on any shape mismatch; the registry only
// routes to a rich card when its parser succeeds, so a redacted or unexpected
// payload falls back to the generic ToolCard field list — never a broken card.
// Cards render only the arguments body; ToolCardShell provides the approval
// chrome and the View raw affordance.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {ToolCall} from '../tool_types';
import ToolCard from '../tool_card';

import ToolCardShell from './tool_card_shell';
import {ChannelChip, UserChip} from './entity_chips';
import {RichCardProps, RichBody, Section, SectionLabel, SectionRow, MessagePreview, LabeledPill, TagPill} from './rich_card_parts';

// ---- parse helpers ---------------------------------------------------------

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

// ---- create_post -----------------------------------------------------------

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

const CreatePostCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseCreatePost(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell {...props}>
            <RichBody>
                <Section>
                    <SectionLabel>
                        <FormattedMessage
                            id='ai.tool_card.channel'
                            defaultMessage='Channel'
                        />
                    </SectionLabel>
                    <SectionRow>
                        <ChannelChip
                            channelId={parsed.channelId}
                            fallbackName={parsed.channelDisplayName}
                            fallbackTeam={parsed.teamDisplayName}
                        />
                        {parsed.rootId && (
                            <TagPill>
                                <FormattedMessage
                                    id='ai.tool_card.reply'
                                    defaultMessage='Reply'
                                />
                            </TagPill>
                        )}
                    </SectionRow>
                </Section>
                <Section>
                    <SectionLabel>
                        <FormattedMessage
                            id='ai.tool_card.message'
                            defaultMessage='Message'
                        />
                    </SectionLabel>
                    <MessagePreview text={parsed.message}/>
                </Section>
            </RichBody>
        </ToolCardShell>
    );
};

// ---- dm --------------------------------------------------------------------

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

const DmCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseDm(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell {...props}>
            <RichBody>
                <Section>
                    <SectionLabel>
                        <FormattedMessage
                            id='ai.tool_card.to'
                            defaultMessage='To'
                        />
                    </SectionLabel>
                    <SectionRow>
                        {parsed.username ? (
                            <UserChip username={parsed.username}/>
                        ) : (
                            <span>
                                <FormattedMessage
                                    id='ai.tool_card.yourself'
                                    defaultMessage='Yourself'
                                />
                            </span>
                        )}
                    </SectionRow>
                </Section>
                <Section>
                    <SectionLabel>
                        <FormattedMessage
                            id='ai.tool_card.message'
                            defaultMessage='Message'
                        />
                    </SectionLabel>
                    <MessagePreview text={parsed.message}/>
                </Section>
            </RichBody>
        </ToolCardShell>
    );
};

// ---- group_message ---------------------------------------------------------

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

const GroupMessageCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseGroupMessage(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell {...props}>
            <RichBody>
                <Section>
                    <SectionLabel>
                        <FormattedMessage
                            id='ai.tool_card.recipients'
                            defaultMessage='Recipients'
                        />
                    </SectionLabel>
                    <SectionRow>
                        {parsed.usernames.map((u) => (
                            <UserChip
                                key={u}
                                username={u}
                            />
                        ))}
                    </SectionRow>
                </Section>
                <Section>
                    <SectionLabel>
                        <FormattedMessage
                            id='ai.tool_card.message'
                            defaultMessage='Message'
                        />
                    </SectionLabel>
                    <MessagePreview text={parsed.message}/>
                </Section>
            </RichBody>
        </ToolCardShell>
    );
};

// ---- search_posts ----------------------------------------------------------

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
    filters: Array<{label: string; value: string}>;
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
    const filters: Array<{label: string; value: string}> = [];
    for (const [key, label] of searchFilterKeys) {
        const value = str(obj[key]);
        if (value !== undefined) { // eslint-disable-line no-undefined
            filters.push({label, value});
        }
    }
    return {query, filters};
}

const QuerySection: React.FC<{query: string; filters: Array<{label: string; value: string}>}> = ({query, filters}) => (
    <RichBody>
        <Section>
            <SectionLabel>
                <FormattedMessage
                    id='ai.tool_card.query'
                    defaultMessage='Query'
                />
            </SectionLabel>
            <MessagePreview text={query}/>
        </Section>
        {filters.length > 0 && (
            <Section>
                <SectionLabel>
                    <FormattedMessage
                        id='ai.tool_card.filters'
                        defaultMessage='Filters'
                    />
                </SectionLabel>
                <SectionRow>
                    {filters.map((f) => (
                        <LabeledPill
                            key={f.label}
                            label={f.label}
                            value={f.value}
                        />
                    ))}
                </SectionRow>
            </Section>
        )}
    </RichBody>
);

const SearchPostsCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseSearchPosts(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell {...props}>
            <QuerySection
                query={parsed.query}
                filters={parsed.filters}
            />
        </ToolCardShell>
    );
};

// ---- search_users ----------------------------------------------------------

export interface SearchUsersParsed {
    term: string;
    filters: Array<{label: string; value: string}>;
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
    const filters: Array<{label: string; value: string}> = [];
    if (typeof obj.limit === 'number') {
        filters.push({label: 'Limit', value: String(obj.limit)});
    }
    return {term, filters};
}

const SearchUsersCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseSearchUsers(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell {...props}>
            <QuerySection
                query={parsed.term}
                filters={parsed.filters}
            />
        </ToolCardShell>
    );
};

// ---- read_post -------------------------------------------------------------

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

const ReadPostCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseReadPost(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell {...props}>
            <RichBody>
                <Section>
                    <SectionLabel>
                        <FormattedMessage
                            id='ai.tool_card.post'
                            defaultMessage='Post'
                        />
                    </SectionLabel>
                    <SectionRow>
                        <LabeledPill
                            label='ID'
                            value={parsed.postId}
                        />
                        {parsed.includeThread !== false && (
                            <TagPill>
                                <FormattedMessage
                                    id='ai.tool_card.with_thread'
                                    defaultMessage='With thread'
                                />
                            </TagPill>
                        )}
                    </SectionRow>
                </Section>
            </RichBody>
        </ToolCardShell>
    );
};

// ---- get_channel_info ------------------------------------------------------

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

const GetChannelInfoCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseGetChannelInfo(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell {...props}>
            <RichBody>
                <Section>
                    <SectionLabel>
                        <FormattedMessage
                            id='ai.tool_card.channel'
                            defaultMessage='Channel'
                        />
                    </SectionLabel>
                    <SectionRow>
                        {parsed.channelId ? (
                            <ChannelChip channelId={parsed.channelId}/>
                        ) : (
                            <>
                                {parsed.channelName && (
                                    <LabeledPill
                                        label='Name'
                                        value={parsed.channelName}
                                    />
                                )}
                                {parsed.teamId && (
                                    <LabeledPill
                                        label='Team'
                                        value={parsed.teamId}
                                    />
                                )}
                            </>
                        )}
                    </SectionRow>
                </Section>
            </RichBody>
        </ToolCardShell>
    );
};

export {
    CreatePostCard,
    DmCard,
    GroupMessageCard,
    SearchPostsCard,
    SearchUsersCard,
    ReadPostCard,
    GetChannelInfoCard,
};
