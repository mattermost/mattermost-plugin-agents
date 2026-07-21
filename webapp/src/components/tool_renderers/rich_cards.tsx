// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Rich per-tool cards for embedded Mattermost tools. Each renders only the
// arguments body inside ToolCardShell, and falls back to the generic ToolCard
// when its strict argument parse fails (e.g. redacted or unexpected payloads).

import React from 'react';
import {FormattedMessage} from 'react-intl';

import ToolCard from '../tool_card';

import ToolCardShell from './tool_card_shell';
import {ChannelChip, UserChip} from './entity_chips';
import {RichCardProps, RichBody, Section, SectionLabel, SectionRow, MessagePreview, LabeledPill, TagPill} from './rich_card_parts';
import {
    parseCreatePost,
    parseDm,
    parseGroupMessage,
    parseSearchPosts,
    parseSearchUsers,
    parseReadPost,
    parseGetChannelInfo,
    SearchFilter,
    SearchFilterKey,
} from './rich_card_parsers';

// Localized label for a stable filter key emitted by the parsers.
const FilterLabel: React.FC<{filterKey: SearchFilterKey}> = ({filterKey}) => {
    switch (filterKey) {
    case 'team':
        return (
            <FormattedMessage
                id='ai.tool_card.filter.team'
                defaultMessage='Team'
            />
        );
    case 'channel':
        return (
            <FormattedMessage
                id='ai.tool_card.filter.channel'
                defaultMessage='Channel'
            />
        );
    case 'from':
        return (
            <FormattedMessage
                id='ai.tool_card.filter.from'
                defaultMessage='From'
            />
        );
    case 'in':
        return (
            <FormattedMessage
                id='ai.tool_card.filter.in'
                defaultMessage='In'
            />
        );
    case 'before':
        return (
            <FormattedMessage
                id='ai.tool_card.filter.before'
                defaultMessage='Before'
            />
        );
    case 'after':
        return (
            <FormattedMessage
                id='ai.tool_card.filter.after'
                defaultMessage='After'
            />
        );
    case 'limit':
        return (
            <FormattedMessage
                id='ai.tool_card.filter.limit'
                defaultMessage='Limit'
            />
        );
    default:
        return null;
    }
};

// ---- create_post -----------------------------------------------------------

const CreatePostCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseCreatePost(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell
            {...props}
            headerContext={parsed.channelDisplayName ? `“${parsed.channelDisplayName}”` : undefined} // eslint-disable-line no-undefined
        >
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

const DmCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseDm(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell
            {...props}
            headerContext={parsed.username ? '@' + parsed.username.replace(/^@/, '') : undefined} // eslint-disable-line no-undefined
        >
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

const GroupMessageCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseGroupMessage(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell
            {...props}
            headerContext={parsed.usernames.map((u) => '@' + u.replace(/^@/, '')).join(', ')}
        >
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

const QuerySection: React.FC<{query: string; filters: SearchFilter[]}> = ({query, filters}) => (
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
                            key={f.key}
                            label={<FilterLabel filterKey={f.key}/>}
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
        <ToolCardShell
            {...props}
            headerContext={`“${parsed.query}”`}
        >
            <QuerySection
                query={parsed.query}
                filters={parsed.filters}
            />
        </ToolCardShell>
    );
};

// ---- search_users ----------------------------------------------------------

const SearchUsersCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseSearchUsers(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell
            {...props}
            headerContext={`“${parsed.term}”`}
        >
            <QuerySection
                query={parsed.term}
                filters={parsed.filters}
            />
        </ToolCardShell>
    );
};

// ---- read_post -------------------------------------------------------------

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
                            label={
                                <FormattedMessage
                                    id='ai.tool_card.post_id'
                                    defaultMessage='ID'
                                />
                            }
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

const GetChannelInfoCard: React.FC<RichCardProps> = (props) => {
    const parsed = parseGetChannelInfo(props.tool.arguments);
    if (!parsed) {
        return <ToolCard {...props}/>;
    }
    return (
        <ToolCardShell
            {...props}
            headerContext={parsed.channelName ? `“${parsed.channelName}”` : undefined} // eslint-disable-line no-undefined
        >
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
                                        label={
                                            <FormattedMessage
                                                id='ai.tool_card.channel_name'
                                                defaultMessage='Name'
                                            />
                                        }
                                        value={parsed.channelName}
                                    />
                                )}
                                {parsed.teamId && (
                                    <LabeledPill
                                        label={<FilterLabel filterKey='team'/>}
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
