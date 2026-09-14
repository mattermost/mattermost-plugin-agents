// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {ChevronDownIcon, ChevronUpIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {pluginIDFromServerOrigin} from '../../utils/tool_names';

import ConsolePolicySection from '../access_control/console_policy_section';

import {MCPServerInfo} from './mcp_types';

// Read-only card for the embedded Mattermost MCP server and live plugin servers.
const BuiltInServerCard = ({
    title,
    badge,
    subtitle,
    helpText,
    policyId,
}: {
    title: string;
    badge: React.ReactNode;
    subtitle?: string;
    helpText?: React.ReactNode;
    policyId?: string;
}) => {
    const intl = useIntl();
    const [isExpanded, setIsExpanded] = useState(true);

    const toggleExpanded = () => {
        setIsExpanded(!isExpanded);
    };

    return (
        <ReadOnlyServerContainer data-testid='built-in-server-card'>
            <ServerHeader
                $expanded={isExpanded}
                role='button'
                tabIndex={0}
                aria-expanded={isExpanded}
                aria-label={intl.formatMessage(
                    {defaultMessage: '{name} settings'},
                    {name: title},
                )}
                onClick={toggleExpanded}
                onKeyDown={(e: React.KeyboardEvent) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        toggleExpanded();
                    }
                }}
            >
                <ReadOnlyTitleRow>
                    <ReadOnlyServerTitle>{title}</ReadOnlyServerTitle>
                    {badge}
                </ReadOnlyTitleRow>
                {isExpanded ? <ChevronUpIcon size={16}/> : <ChevronDownIcon size={16}/>}
            </ServerHeader>
            {isExpanded && (
                <ServerBody>
                    {subtitle && <ReadOnlySubtitle>{subtitle}</ReadOnlySubtitle>}
                    {helpText && <ReadOnlyHelpText>{helpText}</ReadOnlyHelpText>}
                    {policyId && (
                        <ConsolePolicySection
                            resourceType='mcp'
                            resourceId={policyId}
                            resourceDisplayName={title}
                        />
                    )}
                </ServerBody>
            )}
        </ReadOnlyServerContainer>
    );
};

type BuiltInPluginServersSectionProps = {
    embeddedServerId?: string;
    pluginServers: MCPServerInfo[];
};

export const BuiltInPluginServersSection = ({
    embeddedServerId,
    pluginServers,
}: BuiltInPluginServersSectionProps) => {
    const intl = useIntl();

    return (
        <BuiltInSection data-testid='built-in-plugin-servers-section'>
            <BuiltInSectionHeader>
                <BuiltInSectionTitle>
                    <FormattedMessage defaultMessage='Built-in & plugin servers'/>
                </BuiltInSectionTitle>
                <BuiltInSectionDescription>
                    <FormattedMessage defaultMessage='These servers are provided by Mattermost or other plugins. They are not remote connections and cannot be edited here.'/>
                </BuiltInSectionDescription>
            </BuiltInSectionHeader>
            <ServersList>
                <BuiltInServerCard
                    title={intl.formatMessage({defaultMessage: 'Mattermost'})}
                    badge={(
                        <TypeBadge>
                            <FormattedMessage defaultMessage='Built-in'/>
                        </TypeBadge>
                    )}
                    helpText={(
                        <FormattedMessage defaultMessage='Denying access to the built-in Mattermost server removes nearly all in-product Mattermost tools for matching users. This has broader impact than denying a single remote MCP server.'/>
                    )}
                    policyId={embeddedServerId}
                />
                {pluginServers.map((server) => {
                    const pluginID = pluginIDFromServerOrigin(server.url);
                    return (
                        <BuiltInServerCard
                            key={server.url}
                            title={server.name || pluginID || intl.formatMessage({defaultMessage: 'Plugin server'})}
                            badge={(
                                <TypeBadge>
                                    <FormattedMessage defaultMessage='Plugin'/>
                                </TypeBadge>
                            )}
                            subtitle={pluginID ? intl.formatMessage(
                                {defaultMessage: 'Plugin ID: {pluginID}'},
                                {pluginID},
                            ) : ''}
                            policyId={server.id}
                        />
                    );
                })}
            </ServersList>
        </BuiltInSection>
    );
};

const BuiltInSection = styled.div`
    margin-top: 32px;
    margin-bottom: 8px;
`;

const BuiltInSectionHeader = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
`;

const BuiltInSectionTitle = styled.div`
    font-weight: 600;
    font-size: 16px;
    color: var(--center-channel-color);
`;

const BuiltInSectionDescription = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ServersList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
    margin-top: 16px;
    margin-bottom: 16px;
`;

const ServerContainer = styled.div`
    display: flex;
    flex-direction: column;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    background-color: var(--center-channel-bg);
    overflow: hidden;
    text-align: left;
`;

const ReadOnlyServerContainer = styled(ServerContainer)`
    background-color: rgba(var(--center-channel-color-rgb), 0.02);
`;

const ServerHeader = styled.div<{$expanded?: boolean}>`
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 12px 16px;
    cursor: pointer;
    user-select: none;
    ${({$expanded}) => $expanded && `
        border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    `}
`;

const ServerBody = styled.div`
    display: flex;
    flex-direction: column;
    align-items: stretch;
    gap: 16px;
    padding: 16px 16px 20px;
    text-align: left;
`;

const ReadOnlyTitleRow = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    text-align: left;
`;

const ReadOnlyServerTitle = styled.div`
    font-weight: 600;
    font-size: 14px;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const ReadOnlySubtitle = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ReadOnlyHelpText = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    line-height: 1.5;
`;

const TypeBadge = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    font-size: 11px;
    font-weight: 600;
    color: var(--center-channel-bg);
    background-color: rgba(var(--center-channel-color-rgb), 0.56);
    border-radius: 10px;
`;
