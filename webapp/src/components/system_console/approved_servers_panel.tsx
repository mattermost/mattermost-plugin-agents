// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useRef} from 'react';
import styled from 'styled-components';
import {
    PlusIcon,
    TrashCanOutlineIcon,
    ChevronDownIcon,
    ChevronRightIcon,
    ShieldOutlineIcon,
} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {TertiaryButton} from '../assets/buttons';

import {ApprovedMCPServer} from './mcp_servers';
import {BooleanItem, ItemList, TextItem} from './item';

type Props = {
    approvedServers: ApprovedMCPServer[];
    onChange: (servers: ApprovedMCPServer[]) => void;
};

const BUILTIN_APPROVED_SERVERS: ApprovedMCPServer[] = [
    {
        name: 'Atlassian',
        url_patterns: ['mcp.atlassian.com'],
        auto_approve_tools: [
            'search', 'fetch', 'atlassianUserInfo',
            'getAccessibleAtlassianResources',
            'getConfluenceSpaces', 'getConfluencePage',
            'getPagesInConfluenceSpace', 'getConfluencePageAncestors',
            'getConfluencePageDescendants', 'getConfluencePageFooterComments',
            'getConfluencePageInlineComments', 'searchConfluenceUsingCql',
            'getJiraIssue', 'getJiraIssueRemoteIssueLinks',
            'getTransitionsForJiraIssue', 'getVisibleJiraProjects',
            'getJiraProjectIssueTypesMetadata', 'getJiraIssueTypeMetaWithFields',
            'lookupJiraAccountId', 'searchJiraIssuesUsingJql',
        ],
        enabled: true,
    },
    {
        name: 'GitHub',
        url_patterns: ['api.githubcopilot.com'],
        auto_approve_tools: [
            'get_me', 'get_team_members', 'get_teams',
            'get_commit', 'get_file_contents', 'get_latest_release',
            'get_release_by_tag', 'get_tag', 'list_branches',
            'list_commits', 'list_releases', 'list_tags',
            'search_code', 'search_repositories', 'get_label',
            'issue_read', 'list_issue_types', 'list_issues',
            'search_issues', 'list_pull_requests', 'pull_request_read',
            'search_pull_requests', 'search_users',
            'actions_get', 'actions_list', 'get_job_logs',
            'get_code_scanning_alert', 'list_code_scanning_alerts',
            'get_dependabot_alert', 'list_dependabot_alerts',
            'get_discussion', 'get_discussion_comments',
            'list_discussion_categories', 'list_discussions',
            'get_gist', 'list_gists', 'get_repository_tree',
            'list_label', 'get_notification_details', 'list_notifications',
            'search_orgs', 'projects_get', 'projects_list',
            'get_secret_scanning_alert', 'list_secret_scanning_alerts',
            'get_global_security_advisory', 'list_global_security_advisories',
            'list_org_repository_security_advisories',
            'list_repository_security_advisories',
            'list_starred_repositories',
            'get_copilot_job_status', 'get_copilot_space',
            'list_copilot_spaces', 'github_support_docs_search',
        ],
        enabled: true,
    },
    {
        name: 'Figma',
        url_patterns: ['mcp.figma.com'],
        auto_approve_tools: [
            'get_design_context', 'get_metadata', 'get_screenshot',
            'get_variable_defs', 'get_figjam',
            'get_code_connect_map', 'get_code_connect_suggestions', 'whoami',
        ],
        enabled: true,
    },
];

const builtinNames = new Set(BUILTIN_APPROVED_SERVERS.map((s) => s.name));

const generateId = (): string => {
    if (typeof crypto !== 'undefined' && crypto.randomUUID) {
        return crypto.randomUUID();
    }
    return `custom-${Date.now()}-${Math.random().toString(36).slice(2, 11)}`;
};

const parseCommaList = (s: string): string[] =>
    s.split(',').map((p) => p.trim()).filter((p) => p !== '');

type BuiltinServerItemProps = {
    server: ApprovedMCPServer;
    isEnabled: boolean;
    onToggleEnabled: (enabled: boolean) => void;
};

const BuiltinServerItem = ({server, isEnabled, onToggleEnabled}: BuiltinServerItemProps) => {
    const intl = useIntl();
    const [isExpanded, setIsExpanded] = useState(false);

    return (
        <ServerCard>
            <ServerCardHeader>
                <ServerCardHeaderLeft>
                    <ExpandButton onClick={() => setIsExpanded(!isExpanded)}>
                        {isExpanded ? (
                            <ChevronDownIcon size={16}/>
                        ) : (
                            <ChevronRightIcon size={16}/>
                        )}
                    </ExpandButton>
                    <ServerCardInfo>
                        <ServerCardName>{server.name}</ServerCardName>
                        <ServerCardUrl>
                            {server.url_patterns.join(', ')}
                        </ServerCardUrl>
                    </ServerCardInfo>
                </ServerCardHeaderLeft>
                <ServerCardHeaderRight>
                    <BuiltinBadge>
                        <FormattedMessage defaultMessage='Built-in'/>
                    </BuiltinBadge>
                    <ToolCountBadge>
                        <FormattedMessage
                            defaultMessage='{count} tools'
                            values={{count: server.auto_approve_tools.length}}
                        />
                    </ToolCountBadge>
                </ServerCardHeaderRight>
            </ServerCardHeader>

            <EnabledToggleRow>
                <ItemList>
                    <BooleanItem
                        label={intl.formatMessage({defaultMessage: 'Enabled'})}
                        value={isEnabled}
                        onChange={onToggleEnabled}
                        helpText={intl.formatMessage(
                            {defaultMessage: 'When enabled, READ-only tools from {name} will be auto-executed without user approval in channels.'},
                            {name: server.name},
                        )}
                    />
                </ItemList>
            </EnabledToggleRow>

            {isExpanded && (
                <ToolsListContainer>
                    <ToolsListTitle>
                        <FormattedMessage defaultMessage='Auto-approved tools'/>
                    </ToolsListTitle>
                    <ToolsGrid>
                        {server.auto_approve_tools.map((tool) => (
                            <ToolChip key={tool}>{tool}</ToolChip>
                        ))}
                    </ToolsGrid>
                </ToolsListContainer>
            )}
        </ServerCard>
    );
};

type CustomServerItemProps = {
    server: ApprovedMCPServer;
    onChange: (server: ApprovedMCPServer) => void;
    onDelete: () => void;
};

const CustomServerItem = ({server, onChange, onDelete}: CustomServerItemProps) => {
    const intl = useIntl();
    const [isExpanded, setIsExpanded] = useState(true);

    const [urlPatternsRaw, setUrlPatternsRaw] = useState(() => server.url_patterns.join(', '));
    const [autoApproveToolsRaw, setAutoApproveToolsRaw] = useState(() => server.auto_approve_tools.join(', '));
    const urlPatternsFocusedRef = useRef(false);
    const autoApproveToolsFocusedRef = useRef(false);

    useEffect(() => {
        if (!urlPatternsFocusedRef.current) {
            setUrlPatternsRaw(server.url_patterns.join(', '));
        }
    }, [server.url_patterns]);

    useEffect(() => {
        if (!autoApproveToolsFocusedRef.current) {
            setAutoApproveToolsRaw(server.auto_approve_tools.join(', '));
        }
    }, [server.auto_approve_tools]);

    const parseAndCommitUrlPatterns = (raw: string) => {
        const patterns = parseCommaList(raw);
        onChange({...server, url_patterns: patterns});
    };

    const parseAndCommitAutoApproveTools = (raw: string) => {
        const tools = parseCommaList(raw);
        onChange({...server, auto_approve_tools: tools});
    };

    return (
        <ServerCard>
            <ServerCardHeader>
                <ServerCardHeaderLeft>
                    <ExpandButton onClick={() => setIsExpanded(!isExpanded)}>
                        {isExpanded ? (
                            <ChevronDownIcon size={16}/>
                        ) : (
                            <ChevronRightIcon size={16}/>
                        )}
                    </ExpandButton>
                    <ServerCardInfo>
                        <ServerCardName>
                            {server.name || intl.formatMessage({defaultMessage: 'New Approved Server'})}
                        </ServerCardName>
                        <ServerCardUrl>
                            {server.url_patterns.length > 0 ?
                                server.url_patterns.join(', ') :
                                intl.formatMessage({defaultMessage: 'No URL patterns configured'})
                            }
                        </ServerCardUrl>
                    </ServerCardInfo>
                </ServerCardHeaderLeft>
                <ServerCardHeaderRight>
                    <ToolCountBadge>
                        <FormattedMessage
                            defaultMessage='{count} tools'
                            values={{count: server.auto_approve_tools.length}}
                        />
                    </ToolCountBadge>
                    <DeleteButton onClick={onDelete}>
                        <TrashCanOutlineIcon size={16}/>
                        <FormattedMessage defaultMessage='Delete'/>
                    </DeleteButton>
                </ServerCardHeaderRight>
            </ServerCardHeader>

            {isExpanded && (
                <CustomServerForm>
                    <ItemList>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Server Name'})}
                            value={server.name}
                            placeholder={intl.formatMessage({defaultMessage: 'e.g., Internal API'})}
                            onChange={(e) => onChange({...server, name: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'A human-readable name for this approved server.'})}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'URL Patterns'})}
                            value={urlPatternsRaw}
                            placeholder={intl.formatMessage({defaultMessage: 'e.g., mcp.example.com, api.internal.com'})}
                            onChange={(e) => setUrlPatternsRaw(e.target.value)}
                            onFocus={() => {
                                urlPatternsFocusedRef.current = true;
                            }}
                            onBlur={(e) => {
                                urlPatternsFocusedRef.current = false;
                                parseAndCommitUrlPatterns(e.target.value);
                            }}
                            helptext={intl.formatMessage({defaultMessage: 'Comma-separated URL substrings to match MCP server URLs. A server matches if its URL contains any of these patterns.'})}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Auto-Approve Tools'})}
                            value={autoApproveToolsRaw}
                            placeholder={intl.formatMessage({defaultMessage: 'e.g., get_status, list_items, search'})}
                            multiline={true}
                            onChange={(e) => setAutoApproveToolsRaw(e.target.value)}
                            onFocus={() => {
                                autoApproveToolsFocusedRef.current = true;
                            }}
                            onBlur={(e) => {
                                autoApproveToolsFocusedRef.current = false;
                                parseAndCommitAutoApproveTools(e.target.value);
                            }}
                            helptext={intl.formatMessage({defaultMessage: 'Comma-separated list of tool names that are READ-only and can be auto-executed without user approval. Only list tools that do not modify external data.'})}
                        />
                        <BooleanItem
                            label={intl.formatMessage({defaultMessage: 'Enabled'})}
                            value={server.enabled}
                            onChange={(enabled) => onChange({...server, enabled})}
                            helpText={intl.formatMessage({defaultMessage: 'Enable or disable auto-approval for this server.'})}
                        />
                    </ItemList>
                </CustomServerForm>
            )}
        </ServerCard>
    );
};

const ApprovedServersPanel = ({approvedServers, onChange}: Props) => {
    useEffect(() => {
        const customWithoutId = approvedServers.filter(
            (s) => !builtinNames.has(s.name) && !s.id,
        );
        if (customWithoutId.length === 0) {
            return;
        }
        const migrated = approvedServers.map((s) => {
            if (builtinNames.has(s.name) || s.id) {
                return s;
            }
            return {...s, id: generateId()};
        });
        onChange(migrated);
    }, [approvedServers, onChange]);

    const getUserOverride = (builtinName: string): ApprovedMCPServer | undefined => {
        return approvedServers.find((s) => s.name === builtinName);
    };

    const customServers = approvedServers.filter((s) => !builtinNames.has(s.name));

    const toggleBuiltinEnabled = (builtin: ApprovedMCPServer, enabled: boolean) => {
        const existingOverrideIndex = approvedServers.findIndex((s) => s.name === builtin.name);

        if (enabled) {
            // Remove the override to restore built-in default (which is enabled)
            if (existingOverrideIndex >= 0) {
                const updated = [...approvedServers];
                updated.splice(existingOverrideIndex, 1);
                onChange(updated);
            }

            // If no override exists, it's already enabled by default
        } else {
            // Create/update override with enabled: false
            const override: ApprovedMCPServer = {
                ...builtin,
                enabled: false,
            };
            if (existingOverrideIndex >= 0) {
                const updated = [...approvedServers];
                updated[existingOverrideIndex] = override;
                onChange(updated);
            } else {
                onChange([...approvedServers, override]);
            }
        }
    };

    const addCustomServer = () => {
        const newServer: ApprovedMCPServer = {
            id: generateId(),
            name: '',
            url_patterns: [],
            auto_approve_tools: [],
            enabled: true,
        };
        onChange([...approvedServers, newServer]);
    };

    const getServerKey = (server: ApprovedMCPServer, index: number): string =>
        server.id ?? `legacy-${index}`;

    const updateCustomServer = (serverKey: string, server: ApprovedMCPServer) => {
        const idx = serverKey.startsWith('legacy-') ?
            parseInt(serverKey.replace('legacy-', ''), 10) :
            approvedServers.findIndex((s) => s.id === serverKey);
        if (idx < 0) {
            return;
        }
        const updated = [...approvedServers];
        updated[idx] = server;
        onChange(updated);
    };

    const deleteCustomServer = (serverKey: string) => {
        const updated = serverKey.startsWith('legacy-') ?
            approvedServers.filter(
                (_, i) => `legacy-${i}` !== serverKey,
            ) :
            approvedServers.filter((s) => s.id !== serverKey);
        onChange(updated);
    };

    return (
        <Container>
            <SectionHeader>
                <SectionTitle>
                    <ShieldOutlineIcon size={20}/>
                    <FormattedMessage defaultMessage='Approved MCP Servers'/>
                </SectionTitle>
                <SectionDescription>
                    <FormattedMessage
                        defaultMessage='Approved MCP servers have pre-classified READ-only tools that can be auto-executed without user approval in channels. Tool results still require approval before being shared.'
                    />
                </SectionDescription>
            </SectionHeader>

            <SubSectionTitle>
                <FormattedMessage defaultMessage='Built-in Servers'/>
            </SubSectionTitle>
            <SubSectionDescription>
                <FormattedMessage
                    defaultMessage='These servers are curated by Mattermost. You can enable or disable them.'
                />
            </SubSectionDescription>

            <ServersList>
                {BUILTIN_APPROVED_SERVERS.map((builtin) => {
                    const override = getUserOverride(builtin.name);
                    const isEnabled = override ? override.enabled : builtin.enabled;

                    return (
                        <BuiltinServerItem
                            key={builtin.name}
                            server={builtin}
                            isEnabled={isEnabled}
                            onToggleEnabled={(enabled) => toggleBuiltinEnabled(builtin, enabled)}
                        />
                    );
                })}
            </ServersList>

            <SubSectionTitle>
                <FormattedMessage defaultMessage='Custom Approved Servers'/>
            </SubSectionTitle>
            <SubSectionDescription>
                <FormattedMessage
                    defaultMessage='Add your own approved MCP servers. Specify URL patterns to match server URLs and list READ-only tool names that can be auto-executed.'
                />
            </SubSectionDescription>

            {customServers.length === 0 ? (
                <EmptyState>
                    <FormattedMessage
                        defaultMessage='No custom approved servers configured.'
                    />
                </EmptyState>
            ) : (
                <ServersList>
                    {approvedServers.map((server, index) => {
                        if (builtinNames.has(server.name)) {
                            return null;
                        }
                        const serverKey = getServerKey(server, index);
                        return (
                            <CustomServerItem
                                key={serverKey}
                                server={server}
                                onChange={(updated) => updateCustomServer(serverKey, updated)}
                                onDelete={() => deleteCustomServer(serverKey)}
                            />
                        );
                    })}
                </ServersList>
            )}

            <AddServerContainer>
                <TertiaryButton onClick={addCustomServer}>
                    <PlusIcon size={18}/>
                    <FormattedMessage defaultMessage='Add Custom Approved Server'/>
                </TertiaryButton>
            </AddServerContainer>
        </Container>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
`;

const SectionHeader = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-bottom: 8px;
`;

const SectionTitle = styled.h3`
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    font-size: 18px;
    font-weight: 600;
    color: var(--center-channel-color);
`;

const SectionDescription = styled.div`
    font-size: 14px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    line-height: 1.5;
`;

const SubSectionTitle = styled.h4`
    margin: 8px 0 0 0;
    font-size: 14px;
    font-weight: 600;
    color: var(--center-channel-color);
`;

const SubSectionDescription = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    margin-bottom: 8px;
`;

const ServersList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 12px;
`;

const ServerCard = styled.div`
    display: flex;
    flex-direction: column;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
    background-color: var(--center-channel-bg);
    overflow: hidden;
`;

const ServerCardHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 12px 16px;
    background-color: rgba(var(--center-channel-color-rgb), 0.02);
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const ServerCardHeaderLeft = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    flex: 1;
`;

const ServerCardHeaderRight = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
`;

const ExpandButton = styled.button`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    background: none;
    border: none;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    cursor: pointer;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ServerCardInfo = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
`;

const ServerCardName = styled.div`
    font-weight: 600;
    font-size: 14px;
    color: var(--center-channel-color);
`;

const ServerCardUrl = styled.div`
    font-size: 12px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-family: monospace;
`;

const BuiltinBadge = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 10px;
    font-weight: 600;
    color: var(--button-bg);
    background: rgba(var(--button-bg-rgb), 0.08);
    white-space: nowrap;
`;

const ToolCountBadge = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 10px;
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    background: rgba(var(--center-channel-color-rgb), 0.08);
    white-space: nowrap;
`;

const DeleteButton = styled.button`
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    background: none;
    border: none;
    border-radius: 4px;
    color: var(--error-text);
    cursor: pointer;
    font-size: 12px;
    font-weight: 600;

    &:hover {
        background: rgba(var(--error-text-color-rgb), 0.08);
    }
`;

const EnabledToggleRow = styled.div`
    padding: 12px 16px;
`;

const ToolsListContainer = styled.div`
    padding: 12px 16px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const ToolsListTitle = styled.div`
    font-size: 12px;
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    margin-bottom: 8px;
`;

const ToolsGrid = styled.div`
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
`;

const ToolChip = styled.span`
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 11px;
    font-family: monospace;
    color: var(--center-channel-color);
    background: rgba(var(--center-channel-color-rgb), 0.06);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const CustomServerForm = styled.div`
    padding: 16px;
`;

const EmptyState = styled.div`
    padding: 24px;
    text-align: center;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    background-color: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
`;

const AddServerContainer = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 12px;
    margin-top: 8px;
`;

export default ApprovedServersPanel;
