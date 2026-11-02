// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {getAgentScopedAccess} from '@/client';
import type {
    ScopedAccessReport,
    ScopedChannelModeBlocker,
    ScopedExtraChannelStatus,
    ScopedOpenTeamGrantOff,
    ScopedTeamClosedReason,
    ScopedToolRemoval,
} from '@/types/agents';

type Props = {
    agentId: string;

    /** The form has edits the report, computed from the saved agent, does not include yet. */
    hasUnsavedChanges: boolean;
};

const ScopedAccessView = ({agentId, hasUnsavedChanges}: Props) => {
    const intl = useIntl();
    const [report, setReport] = useState<ScopedAccessReport | null>(null);
    const [failed, setFailed] = useState(false);

    useEffect(() => {
        let superseded = false;
        setReport(null);
        setFailed(false);
        getAgentScopedAccess(agentId).then((result) => {
            if (!superseded) {
                setReport(result);
            }
        }).catch(() => {
            if (!superseded) {
                setFailed(true);
            }
        });
        return () => {
            superseded = true;
        };
    }, [agentId]);

    return (
        <View
            data-testid='scoped-access-view'
            aria-label={intl.formatMessage({defaultMessage: 'What this agent can access in channels'})}
        >
            <ViewTitle>
                <FormattedMessage defaultMessage='What this agent can access in channels'/>
            </ViewTitle>
            {failed && (
                <SectionNotice
                    type='danger'
                    title={<FormattedMessage defaultMessage="Couldn't load what this agent can access."/>}
                />
            )}
            {!failed && !report && (
                <Loading>
                    <Spinner size='20'/>
                    <FormattedMessage defaultMessage='Working out what this agent can access...'/>
                </Loading>
            )}
            {report && (
                <ReportBody
                    report={report}
                    hasUnsavedChanges={hasUnsavedChanges}
                />
            )}
        </View>
    );
};

const ReportBody = ({report, hasUnsavedChanges}: {report: ScopedAccessReport; hasUnsavedChanges: boolean}) => {
    if (!report.configured) {
        return (
            <SectionNotice
                type='info'
                title={<FormattedMessage defaultMessage='Save the agent to see what it can access in channels.'/>}
            />
        );
    }

    return (
        <>
            {hasUnsavedChanges && (
                <SectionNotice
                    type='info'
                    title={<FormattedMessage defaultMessage='This shows the last saved settings. Save your changes to update it.'/>}
                />
            )}
            {!report.active && (
                <SectionNotice
                    type='warning'
                    title={<FormattedMessage defaultMessage="This agent isn't running tools in channels without asking yet."/>}
                    description={(
                        <List>
                            {report.blockers.map((blocker) => (
                                <li key={blocker}>{blockerText(blocker)}</li>
                            ))}
                        </List>
                    )}
                />
            )}
            <Summary data-testid='scoped-access-summary'>
                <FormattedMessage defaultMessage="In Mattermost this agent can only read. It never reads direct messages or private channels outside the ones listed here, and it can't post, edit, or delete anything beyond its own reply. External tools can do whatever their service account allows."/>
            </Summary>
            <ChannelsSection channels={report.channels}/>
            <ToolsSection tools={report.tools}/>
        </>
    );
};

const ChannelsSection = ({channels}: {channels: ScopedAccessReport['channels']}) => {
    const {openTeams, extraChannels} = channels;
    const listedOpen = openTeams.open.map((team) => team.displayName).join(', ');
    const hiddenOpen = openTeams.openCount - openTeams.open.length;

    return (
        <Section data-testid='scoped-access-channels'>
            <SectionTitle>
                <FormattedMessage defaultMessage='Channels it can read'/>
            </SectionTitle>
            <List>
                <li>
                    <Strong><FormattedMessage defaultMessage='The channel it is used in'/></Strong>
                    {' '}
                    <Muted><FormattedMessage defaultMessage="Including private channels, even if the agent isn't a member."/></Muted>
                </li>
                <li>
                    <Strong><FormattedMessage defaultMessage="Public channels on that channel's team"/></Strong>
                    {' '}
                    <Muted>
                        {channels.triggeringTeamPublicChannels ? (
                            <FormattedMessage defaultMessage='Always included, even if the team is invite-only.'/>
                        ) : teamGrantOffText(openTeams.disabledReason)}
                    </Muted>
                </li>
                <li>
                    <Strong><FormattedMessage defaultMessage='Public channels on open teams'/></Strong>
                    {' '}
                    {openTeams.enabled && openTeams.openCount === 0 && (
                        <Muted><FormattedMessage defaultMessage='No team on this server is open to everyone.'/></Muted>
                    )}
                    {openTeams.enabled && openTeams.openCount > 0 && (
                        <Muted>
                            <FormattedMessage
                                defaultMessage='{count, plural, one {# team counts as open} other {# teams count as open}}: {names}'
                                values={{count: openTeams.openCount, names: listedOpen}}
                            />
                            {hiddenOpen > 0 && (
                                <>
                                    {' '}
                                    <FormattedMessage
                                        defaultMessage='and {count} more'
                                        values={{count: hiddenOpen}}
                                    />
                                </>
                            )}
                        </Muted>
                    )}
                    {!openTeams.enabled && (
                        <Muted>{openGrantOffText(openTeams.disabledReason)}</Muted>
                    )}
                    {openTeams.enabled && openTeams.closed.length > 0 && (
                        <ClosedTeams data-testid='scoped-access-closed-teams'>
                            <Muted>
                                <FormattedMessage
                                    defaultMessage="{count, plural, one {# team doesn't} other {# teams don't}} count as open:"
                                    values={{count: openTeams.closedCount}}
                                />
                            </Muted>
                            <List>
                                {openTeams.closed.map((team) => (
                                    <li key={team.id}>
                                        {team.displayName}
                                        {' '}
                                        <Muted>
                                            {'('}
                                            {team.reasons.map((reason, index) => (
                                                <React.Fragment key={reason}>
                                                    {index > 0 && ', '}
                                                    {teamClosedText(reason)}
                                                </React.Fragment>
                                            ))}
                                            {')'}
                                        </Muted>
                                    </li>
                                ))}
                            </List>
                        </ClosedTeams>
                    )}
                </li>
                {extraChannels.map((channel) => (
                    <li
                        key={channel.id}
                        data-testid='scoped-access-extra-channel'
                    >
                        <Strong>{channel.displayName || channel.id}</Strong>
                        {channel.teamName && (
                            <>
                                {' '}
                                <Muted>{channel.teamName}</Muted>
                            </>
                        )}
                        {' '}
                        <Tag
                            type={channel.status === 'readable' ? 'success' : 'warning'}
                            label={extraChannelStatusText(channel.status)}
                        />
                    </li>
                ))}
            </List>
        </Section>
    );
};

const ToolsSection = ({tools}: {tools: ScopedAccessReport['tools']}) => {
    const offered = tools.filter((tool) => tool.offered);
    const removed = tools.filter((tool) => !tool.offered);

    const toolLabel = (tool: ScopedAccessReport['tools'][number]) => {
        const name = tool.name === '*' ? <FormattedMessage defaultMessage='All tools'/> : tool.name;
        const server = tool.serverKind === 'builtin' ? <FormattedMessage defaultMessage='Built-in tools'/> : tool.serverName || tool.serverOrigin;
        return (
            <>
                <Strong>{name}</Strong>
                {' '}
                <Muted>{server}</Muted>
            </>
        );
    };

    return (
        <Section data-testid='scoped-access-tools'>
            <SectionTitle>
                <FormattedMessage defaultMessage='Tools it can use'/>
            </SectionTitle>
            {offered.length === 0 ? (
                <Muted><FormattedMessage defaultMessage='None of the tools picked for this agent can be used in channels.'/></Muted>
            ) : (
                <List data-testid='scoped-access-offered-tools'>
                    {offered.map((tool) => (
                        <li key={`${tool.serverOrigin}/${tool.name}`}>{toolLabel(tool)}</li>
                    ))}
                </List>
            )}
            {removed.length > 0 && (
                <>
                    <SectionTitle>
                        <FormattedMessage defaultMessage='Picked, but left out in channels'/>
                    </SectionTitle>
                    <List data-testid='scoped-access-removed-tools'>
                        {removed.map((tool) => (
                            <li key={`${tool.serverOrigin}/${tool.name}`}>
                                {toolLabel(tool)}
                                {' '}
                                <Muted>{'- '}{removalText(tool.reason)}</Muted>
                            </li>
                        ))}
                    </List>
                </>
            )}
        </Section>
    );
};

function blockerText(blocker: ScopedChannelModeBlocker): React.ReactNode {
    switch (blocker) {
    case 'service_account_off':
        return <FormattedMessage defaultMessage='Service accounts are turned off for this agent.'/>;
    case 'all_tools_enabled':
        return <FormattedMessage defaultMessage='"Automatically enable all MCP tools" is on; pick the tools by name instead.'/>;
    case 'license':
        return <FormattedMessage defaultMessage="The server's license doesn't include MCP service accounts."/>;
    case 'channel_tool_calling_off':
        return <FormattedMessage defaultMessage='Tool calling in channels is turned off in the plugin settings.'/>;
    case 'server_unsupported':
        return <FormattedMessage defaultMessage="This Mattermost server can't create scoped sessions."/>;
    default:
        return blocker;
    }
}

function openGrantOffText(reason?: ScopedOpenTeamGrantOff): React.ReactNode {
    switch (reason) {
    case 'compliance_mode':
        return <FormattedMessage defaultMessage='Compliance mode is on, so no team counts as open.'/>;
    case 'users_cannot_join_teams':
        return <FormattedMessage defaultMessage="People can't join teams on their own on this server, so no team counts as open."/>;
    default:
        return <FormattedMessage defaultMessage="The server's settings couldn't be read, so no team counts as open."/>;
    }
}

function teamGrantOffText(reason?: ScopedOpenTeamGrantOff): React.ReactNode {
    if (reason === 'compliance_mode') {
        return <FormattedMessage defaultMessage='Not included: compliance mode is on.'/>;
    }
    return <FormattedMessage defaultMessage="Not included: the server's settings couldn't be read."/>;
}

function teamClosedText(reason: ScopedTeamClosedReason): React.ReactNode {
    switch (reason) {
    case 'archived':
        return <FormattedMessage defaultMessage='archived'/>;
    case 'invite_only':
        return <FormattedMessage defaultMessage='invite-only'/>;
    case 'group_constrained':
        return <FormattedMessage defaultMessage='limited to synced groups'/>;
    case 'domain_restricted':
        return <FormattedMessage defaultMessage='limited to some email domains'/>;
    case 'policy_enforced':
        return <FormattedMessage defaultMessage='governed by an access policy'/>;
    default:
        return <FormattedMessage defaultMessage='restricted'/>;
    }
}

function extraChannelStatusText(status: ScopedExtraChannelStatus): React.ReactNode {
    switch (status) {
    case 'readable':
        return <FormattedMessage defaultMessage='Readable'/>;
    case 'missing':
        return <FormattedMessage defaultMessage='Not found'/>;
    case 'archived':
        return <FormattedMessage defaultMessage='Archived'/>;
    case 'restricted':
        return <FormattedMessage defaultMessage='Restricted by an access policy'/>;
    default:
        return <FormattedMessage defaultMessage="Direct and group messages can't be read"/>;
    }
}

function removalText(reason?: ScopedToolRemoval): React.ReactNode {
    switch (reason) {
    case 'not_named':
        return <FormattedMessage defaultMessage='granted as all tools on a server; pick each tool by name'/>;
    case 'plugin_server':
        return <FormattedMessage defaultMessage="another plugin's tools can't stay within the channel limits"/>;
    case 'write_tool':
        return <FormattedMessage defaultMessage='changes data in Mattermost, and this agent can only read'/>;
    case 'needs_wider_access':
        return <FormattedMessage defaultMessage="needs access this agent doesn't have in channels, such as member lists or personal data"/>;
    case 'no_service_account':
        return <FormattedMessage defaultMessage='the server has no service account credentials'/>;
    case 'not_opted_in':
        return <FormattedMessage defaultMessage='reaches outside Mattermost; pick it under Built-in tools to allow it'/>;
    default:
        return <FormattedMessage defaultMessage="the server or tool wasn't found, is turned off, or couldn't be reached"/>;
    }
}

const View = styled.section`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
    padding: var(--spacing-l);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 8px;
    background: rgba(var(--center-channel-color-rgb), 0.02);
`;

const ViewTitle = styled.h4`
    margin: 0;
    font-size: 14px;
    font-weight: 600;
`;

const Loading = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-s);
`;

const Summary = styled.p`
    margin: 0;
    padding: var(--spacing-m);
    border-radius: 4px;
    background: rgba(var(--button-bg-rgb), 0.08);
    font-size: 12px;
    line-height: 16px;
`;

const Section = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

const SectionTitle = styled.h5`
    margin: 0;
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const List = styled.ul`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    margin: 0;
    padding-left: var(--spacing-l);
    font-size: 12px;
    line-height: 16px;
`;

const ClosedTeams = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxs);
    margin-top: var(--spacing-xxs);
`;

const Strong = styled.span`
    font-weight: 600;
`;

const Muted = styled.span`
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

export default ScopedAccessView;
