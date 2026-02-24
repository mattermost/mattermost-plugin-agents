// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import Panel, {PanelFooterText} from './panel';
import {BooleanItem, ItemList} from './item';
import {ApprovedProvidersConfig} from './mcp_servers';

type Props = {
    value: ApprovedProvidersConfig;
    onChange: (value: ApprovedProvidersConfig) => void;
};

const ApprovedProvidersPanel = ({value, onChange}: Props) => {
    const intl = useIntl();

    return (
        <Panel
            title={intl.formatMessage({defaultMessage: 'Approved Tool Providers'})}
            subtitle={intl.formatMessage({
                defaultMessage:
                    'Mattermost-vetted MCP servers whose read-only tools can run automatically without per-call user approval.',
            })}
        >
            <ItemList>
                <BooleanItem
                    label={
                        <FormattedMessage defaultMessage='Atlassian (Jira & Confluence)'/>
                    }
                    value={Boolean(value.atlassian)}
                    onChange={(to) => onChange({...value, atlassian: to})}
                    helpText={intl.formatMessage({
                        defaultMessage:
                            'When enabled, 20 read-only Atlassian tools (search, getJiraIssue, getConfluencePage, etc.) auto-execute in channels without approval. Write tools like createJiraIssue still require approval. Result sharing in channels always requires approval.',
                    })}
                />
                <BooleanItem
                    label={
                        <FormattedMessage defaultMessage='GitHub'/>
                    }
                    value={Boolean(value.github)}
                    onChange={(to) => onChange({...value, github: to})}
                    helpText={intl.formatMessage({
                        defaultMessage:
                            'When enabled, 56 read-only GitHub tools (issue_read, pull_request_read, search_code, etc.) auto-execute in channels without approval. Write tools like create_pull_request still require approval. Result sharing in channels always requires approval.',
                    })}
                />
                <BooleanItem
                    label={
                        <FormattedMessage defaultMessage='Figma'/>
                    }
                    value={Boolean(value.figma)}
                    onChange={(to) => onChange({...value, figma: to})}
                    helpText={intl.formatMessage({
                        defaultMessage:
                            'When enabled, 9 read-only Figma tools (get_design_context, get_screenshot, get_metadata, etc.) auto-execute in channels without approval. Write tools like generate_diagram still require approval. Result sharing in channels always requires approval.',
                    })}
                />
            </ItemList>
            <PanelFooterText>
                <FormattedMessage
                    defaultMessage='Turning a provider off restores the standard accept/reject approval flow for all tools from that server. These settings only affect channel @mentions — DM tool calls are unaffected.'
                />
            </PanelFooterText>
        </Panel>
    );
};

export default ApprovedProvidersPanel;
