// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Tag} from '@mattermost/compass-ui/components/tag';

import {pluginIDFromServerOrigin} from '../../utils/tool_names';
import {useABACSupport} from '../../utils/access_control';

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
    return (
        <ReadOnlyServerContainer data-testid='built-in-server-card'>
            <ServerHeader>
                <ReadOnlyTitleRow>
                    <ReadOnlyServerTitle>{title}</ReadOnlyServerTitle>
                    {badge}
                </ReadOnlyTitleRow>
            </ServerHeader>
            {subtitle && <ReadOnlySubtitle>{subtitle}</ReadOnlySubtitle>}
            {helpText && <ReadOnlyHelpText>{helpText}</ReadOnlyHelpText>}
            {policyId && (
                <ConsolePolicySection
                    resourceType='mcp'
                    resourceId={policyId}
                    resourceDisplayName={title}
                />
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

    // The warning is about access policies, which exist only where ABAC does.
    const {supported: abacSupported} = useABACSupport();

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
                    badge={<Tag label={<FormattedMessage defaultMessage='Built-in'/>}/>}
                    helpText={abacSupported && (
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
                            badge={<Tag label={<FormattedMessage defaultMessage='Plugin'/>}/>}
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

const BuiltInSection = styled.div``;

const BuiltInSectionHeader = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxs);
`;

const BuiltInSectionTitle = styled.div`
    font-family: var(--font-family-heading);
    font-size: var(--font-size-200);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-200);
    color: var(--center-channel-color);
`;

const BuiltInSectionDescription = styled.div`
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ServersList = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
    margin-top: var(--spacing-l);
`;

const ServerContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: var(--radius-s);
    padding: var(--spacing-m);
    background-color: var(--center-channel-bg);
`;

const ReadOnlyServerContainer = styled(ServerContainer)`
    background-color: rgba(var(--center-channel-color-rgb), 0.02);
`;

const ServerHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
`;

const ReadOnlyTitleRow = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    flex-wrap: wrap;
`;

const ReadOnlyServerTitle = styled.div`
    font-weight: var(--font-weight-semibold);
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);
    color: var(--center-channel-color);
`;

const ReadOnlySubtitle = styled.div`
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ReadOnlyHelpText = styled.div`
    font-size: var(--font-size-75);
    color: rgba(var(--center-channel-color-rgb), 0.64);
    line-height: 1.5;
`;
