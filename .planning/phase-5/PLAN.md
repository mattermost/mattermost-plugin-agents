# Phase 5: Admin Configuration UI - Implementation Plan

## Overview

Phase 5 adds a System Console admin UI for managing Approved MCP Servers. This allows admins to:

1. View the three built-in Mattermost-approved servers (Atlassian, GitHub, Figma)
2. Enable/disable built-in servers
3. Add custom approved servers with URL patterns and auto-approve tool lists
4. Edit/remove custom approved servers

The config is persisted via the existing plugin settings mechanism (`mcp.approvedServers` field in the `Config` struct from Phase 1). Built-in servers come from `mcp.BuiltinApprovedServers()` and are displayed read-only (except for the enabled toggle). User-defined overrides and additions are stored in `mcp.approvedServers`.

---

## Architecture Summary

### How Admin Settings Work in This Plugin

1. **`plugin.json`** defines a single custom setting: `{ "key": "Config", "type": "custom" }`
2. **`webapp/src/index.tsx`** registers: `registry.registerAdminConsoleCustomSetting('Config', Config)`
3. **`webapp/src/components/system_console/config.tsx`** is the root Config component that receives `props.value` (the entire plugin config) and calls `props.onChange(props.id, newValue)` to update it.
4. Config changes flow: Admin UI -> `props.onChange` -> Mattermost server saves to `PluginSettings` -> `OnConfigurationChange()` -> `config.Container.Update()` -> listeners notified.
5. The MCP config is nested at `value.mcp` in the Config component.

### Data Model (from Phase 1)

```typescript
// Go: mcp.ApprovedMCPServer
type ApprovedMCPServer = {
    name: string;
    url_patterns: string[];
    auto_approve_tools: string[];
    enabled: boolean;
};
```

The backend `mcp.Config` struct has:
```go
type Config struct {
    // ... existing fields ...
    ApprovedServers []ApprovedMCPServer `json:"approvedServers,omitempty"`
}
```

The `config.Container.ApprovedMCPServers()` method merges `BuiltinApprovedServers()` with `cfg.MCP.ApprovedServers` using `MergeApprovedServers()`.

### Key Design Decisions

1. **Built-in servers are NOT stored in the config**. They are hardcoded in `mcp/approved_servers_builtin.go`. The admin UI displays them but modifications create user-defined overrides (same name = override).
2. **Disabling a built-in server** creates a user-defined override with `enabled: false` and the same `name`.
3. **Custom servers** are stored directly in `mcp.approvedServers`.
4. **The component lives inside the MCP Panel**, as a new tab alongside the existing "Configuration" and "Tools" tabs.

---

## Step 5.1: Extend the `MCPConfig` TypeScript Type

### File: `webapp/src/components/system_console/mcp_servers.tsx`

Add the `ApprovedMCPServer` type and extend `MCPConfig` to include `approvedServers`.

**Add new type** after the existing `MCPEmbeddedServerConfig` type (around line 26):

```typescript
export type ApprovedMCPServer = {
    name: string;
    url_patterns: string[];
    auto_approve_tools: string[];
    enabled: boolean;
};
```

**Extend `MCPConfig`** (around line 28-33):

```typescript
export type MCPConfig = {
    enabled: boolean;
    enablePluginServer: boolean;
    servers: MCPServerConfig[];
    embeddedServer: MCPEmbeddedServerConfig;
    idleTimeoutMinutes?: number;
    approvedServers?: ApprovedMCPServer[];  // NEW
};
```

---

## Step 5.2: Add "Approved Servers" Tab to MCP Panel

### File: `webapp/src/components/system_console/mcp_servers.tsx`

The MCP section already has a tab system (`config` | `tools`). Add a third tab: `approved`.

**Modify the `activeTab` state type** (line 238):

Change from:
```typescript
const [activeTab, setActiveTab] = useState<'config' | 'tools'>('config');
```
To:
```typescript
const [activeTab, setActiveTab] = useState<'config' | 'tools' | 'approved'>('config');
```

**Add the third tab button** in the `TabsContainer` (after the "Tools" tab button, around line 318):

```tsx
<TabButton
    active={activeTab === 'approved'}
    onClick={() => setActiveTab('approved')}
>
    <FormattedMessage defaultMessage='Approved Servers'/>
</TabButton>
```

**Add the tab content** in the `TabContent` (after the tools tab content block, around line 397):

```tsx
{activeTab === 'approved' && (
    <ApprovedServersPanel
        approvedServers={config.approvedServers || []}
        onChange={(approvedServers) => onChange({...config, approvedServers})}
    />
)}
```

**Add import** for the new component (top of file):

```typescript
import ApprovedServersPanel from './approved_servers_panel';
```

---

## Step 5.3: Create the Approved Servers Panel Component

### File: `webapp/src/components/system_console/approved_servers_panel.tsx` (NEW)

This is the main new component. It displays:
1. A description/help text explaining what approved servers are
2. The list of built-in servers (with enable/disable toggle)
3. A list of user-defined custom servers (fully editable)
4. An "Add Custom Approved Server" button

#### Component Structure

```
ApprovedServersPanel
  - HelpText (explains auto-approval)
  - BuiltinServersSection
    - BuiltinServerItem (x3: Atlassian, GitHub, Figma)
      - Name + URL pattern (read-only display)
      - Enabled toggle
      - Collapsible tool list (read-only)
  - CustomServersSection
    - CustomServerItem (x N)
      - Name input
      - URL patterns input (comma-separated)
      - Auto-approve tools input (comma-separated)
      - Enabled toggle
      - Delete button
    - Add Custom Server button
```

#### Full Component Implementation

```typescript
// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
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
```

#### Props Type

```typescript
type Props = {
    approvedServers: ApprovedMCPServer[];
    onChange: (servers: ApprovedMCPServer[]) => void;
};
```

#### Built-in Servers Definition (Frontend Mirror)

The component needs to know the built-in servers to display them. Since these are hardcoded in the Go backend, we mirror them in the frontend. This is acceptable because:
- They only change at plugin release time
- They are compile-time constants
- The backend is the source of truth for runtime merging

```typescript
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
            'get_variable_defs', 'get_figjam', 'create_design_system_rules',
            'get_code_connect_map', 'get_code_connect_suggestions', 'whoami',
        ],
        enabled: true,
    },
];
```

#### Determining Effective Built-in Server State

The user-defined `approvedServers` can contain overrides for built-in servers (matched by `name`). The component needs to determine the effective state:

```typescript
// Get the user override for a built-in server (if any)
const getUserOverride = (builtinName: string): ApprovedMCPServer | undefined => {
    return approvedServers.find((s) => s.name === builtinName);
};

// Get custom servers (those not overriding a built-in)
const builtinNames = new Set(BUILTIN_APPROVED_SERVERS.map((s) => s.name));
const customServers = approvedServers.filter((s) => !builtinNames.has(s.name));
```

#### Toggling a Built-in Server's Enabled State

When the admin toggles a built-in server:

- **Disabling**: Create/update a user-defined entry with `enabled: false` and the same name, url_patterns, and auto_approve_tools as the built-in.
- **Re-enabling**: Remove the user-defined override (so the built-in default takes effect).

```typescript
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
```

#### Custom Server CRUD

```typescript
const addCustomServer = () => {
    const newServer: ApprovedMCPServer = {
        name: '',
        url_patterns: [],
        auto_approve_tools: [],
        enabled: true,
    };
    onChange([...approvedServers, newServer]);
};

const updateCustomServer = (index: number, server: ApprovedMCPServer) => {
    // index is in the full approvedServers array
    const updated = [...approvedServers];
    updated[index] = server;
    onChange(updated);
};

const deleteCustomServer = (index: number) => {
    const updated = approvedServers.filter((_, i) => i !== index);
    onChange(updated);
};
```

#### JSX Structure

```tsx
const ApprovedServersPanel = ({approvedServers, onChange}: Props) => {
    const intl = useIntl();

    // ... helper functions above ...

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

            {/* Built-in Servers Section */}
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

            {/* Custom Servers Section */}
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
                            return null; // Skip built-in overrides
                        }
                        return (
                            <CustomServerItem
                                key={index}
                                server={server}
                                onChange={(updated) => updateCustomServer(index, updated)}
                                onDelete={() => deleteCustomServer(index)}
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

export default ApprovedServersPanel;
```

---

## Step 5.4: Built-in Server Item Sub-Component

### Inside: `webapp/src/components/system_console/approved_servers_panel.tsx`

This is a sub-component within the same file. It renders a single built-in approved server.

```tsx
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
```

---

## Step 5.5: Custom Server Item Sub-Component

### Inside: `webapp/src/components/system_console/approved_servers_panel.tsx`

This sub-component renders a fully editable custom approved server.

```tsx
type CustomServerItemProps = {
    server: ApprovedMCPServer;
    onChange: (server: ApprovedMCPServer) => void;
    onDelete: () => void;
};

const CustomServerItem = ({server, onChange, onDelete}: CustomServerItemProps) => {
    const intl = useIntl();
    const [isExpanded, setIsExpanded] = useState(true);

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
                            {server.url_patterns.length > 0
                                ? server.url_patterns.join(', ')
                                : intl.formatMessage({defaultMessage: 'No URL patterns configured'})
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
                            value={server.url_patterns.join(', ')}
                            placeholder={intl.formatMessage({defaultMessage: 'e.g., mcp.example.com, api.internal.com'})}
                            onChange={(e) => {
                                const patterns = e.target.value
                                    .split(',')
                                    .map((p) => p.trim())
                                    .filter((p) => p !== '');
                                onChange({...server, url_patterns: patterns});
                            }}
                            helptext={intl.formatMessage({defaultMessage: 'Comma-separated URL substrings to match MCP server URLs. A server matches if its URL contains any of these patterns.'})}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Auto-Approve Tools'})}
                            value={server.auto_approve_tools.join(', ')}
                            placeholder={intl.formatMessage({defaultMessage: 'e.g., get_status, list_items, search'})}
                            multiline={true}
                            onChange={(e) => {
                                const tools = e.target.value
                                    .split(',')
                                    .map((t) => t.trim())
                                    .filter((t) => t !== '');
                                onChange({...server, auto_approve_tools: tools});
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
```

---

## Step 5.6: Styled Components

### Inside: `webapp/src/components/system_console/approved_servers_panel.tsx`

All styled components for the Approved Servers Panel. These follow the exact conventions used in `mcp_servers.tsx` and `mcp_tools_viewer.tsx`.

```typescript
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
```

---

## Step 5.7: Update Default Config in `config.tsx`

### File: `webapp/src/components/system_console/config.tsx`

The `defaultConfig.mcp` object (line 109-113) should remain unchanged because `approvedServers` defaults to `undefined`/not present (using `omitempty` in the Go struct). No explicit default is needed.

However, the `MCPConfig` type import at line 22 already exports all types we need:

```typescript
import MCPServers, {MCPConfig} from './mcp_servers';
```

No changes needed since `MCPConfig` will be updated in `mcp_servers.tsx` (Step 5.1) and the `approvedServers` field is optional.

---

## Step 5.8: Config Pass-Through in `config.tsx`

### File: `webapp/src/components/system_console/config.tsx`

The existing code at lines 379-391 already passes the entire `mcpConfig` to `MCPServers` and receives the entire updated config back. Since `approvedServers` is part of `MCPConfig`, it will automatically be preserved through the existing `onChange` callback:

```typescript
<MCPServers
    mcpConfig={mcpConfig}
    onChange={(config) => {
        const updatedConfig = {
            ...config,
            servers: config.servers || {},
        };
        props.onChange(props.id, {...value, mcp: updatedConfig});
        props.setSaveNeeded();
    }}
/>
```

The `approvedServers` field will flow through this callback automatically. No changes needed to `config.tsx`.

---

## i18n Strings

All new user-facing text uses `react-intl` `FormattedMessage` or `intl.formatMessage`. New strings:

| Default Message | Location |
|----------------|----------|
| `Approved Servers` | Tab button in `mcp_servers.tsx` |
| `Approved MCP Servers` | Section title in `approved_servers_panel.tsx` |
| `Approved MCP servers have pre-classified READ-only tools that can be auto-executed without user approval in channels. Tool results still require approval before being shared.` | Section description |
| `Built-in Servers` | Sub-section title |
| `These servers are curated by Mattermost. You can enable or disable them.` | Sub-section description |
| `Custom Approved Servers` | Sub-section title |
| `Add your own approved MCP servers. Specify URL patterns to match server URLs and list READ-only tool names that can be auto-executed.` | Sub-section description |
| `Built-in` | Badge on built-in servers |
| `{count} tools` | Tool count badge |
| `Enabled` | Toggle label |
| `When enabled, READ-only tools from {name} will be auto-executed without user approval in channels.` | Help text for built-in toggle |
| `Auto-approved tools` | Collapsible tools list title |
| `New Approved Server` | Placeholder name for new custom server |
| `No URL patterns configured` | Placeholder URL for new custom server |
| `Server Name` | Input label |
| `e.g., Internal API` | Placeholder |
| `A human-readable name for this approved server.` | Help text |
| `URL Patterns` | Input label |
| `e.g., mcp.example.com, api.internal.com` | Placeholder |
| `Comma-separated URL substrings to match MCP server URLs. A server matches if its URL contains any of these patterns.` | Help text |
| `Auto-Approve Tools` | Input label |
| `e.g., get_status, list_items, search` | Placeholder |
| `Comma-separated list of tool names that are READ-only and can be auto-executed without user approval. Only list tools that do not modify external data.` | Help text |
| `Enable or disable auto-approval for this server.` | Help text |
| `No custom approved servers configured.` | Empty state |
| `Add Custom Approved Server` | Add button |
| `Delete` | Delete button |

These follow the pattern used throughout the plugin of using `react-intl` `FormattedMessage` with `defaultMessage` for compile-time extraction. The `en.json` file is auto-generated by the i18n extraction process.

---

## Files Changed Summary

| File | Change Type | Description |
|------|-------------|-------------|
| `webapp/src/components/system_console/mcp_servers.tsx` | MODIFY | Add `ApprovedMCPServer` type, extend `MCPConfig`, add "Approved Servers" tab, import `ApprovedServersPanel` |
| `webapp/src/components/system_console/approved_servers_panel.tsx` | NEW | Full component for managing approved MCP servers (built-in display + custom CRUD) |

---

## What Does NOT Change

1. **`config.tsx`** - No changes. The existing `MCPConfig` pass-through handles `approvedServers` automatically.
2. **`plugin.json`** - No changes. Settings schema remains a single custom "Config" entry.
3. **Backend Go files** - No changes. Phase 1 already added `ApprovedServers` to the MCP config and the merge logic.
4. **`mcp_tools_viewer.tsx`** - No changes.
5. **`item.tsx`, `panel.tsx`** - No changes. Existing components are reused.

---

## Edge Cases

1. **Name collision with built-in**: If a user creates a custom server named "GitHub", it becomes an override for the built-in GitHub server. The backend merge logic handles this correctly (user-defined takes precedence). The UI should prevent this by checking for name collisions. The `CustomServerItem` should show a warning if the name matches a built-in server name.

2. **Empty tool lists**: A custom server with an empty `auto_approve_tools` list is valid but useless (no tools would be auto-approved). The UI allows this but could show a hint.

3. **Empty URL patterns**: A custom server with empty `url_patterns` would never match any server. The UI allows this but could show a validation hint.

4. **Config migration**: Existing installations upgrading to this version have no `approvedServers` in their config. The `omitempty` tag means it defaults to `undefined`/empty. The built-in servers are always available via `BuiltinApprovedServers()`.

---

## Testing Considerations

### Manual Testing Scenarios

1. **View built-in servers**: Navigate to System Console > Plugins > Agents > MCP > Approved Servers tab. Verify three built-in servers are displayed with correct names, URL patterns, and tool counts.

2. **Expand built-in server**: Click the chevron on a built-in server. Verify the tool names list is displayed in a chip grid.

3. **Disable built-in server**: Toggle "Enabled" to false on GitHub. Save. Verify the config is saved with an override entry. Reload page and verify the toggle persists as disabled.

4. **Re-enable built-in server**: Toggle "Enabled" back to true on GitHub. Save. Verify the override entry is removed from config.

5. **Add custom server**: Click "Add Custom Approved Server". Fill in name, URL patterns, and tools. Save. Verify the config includes the new entry.

6. **Edit custom server**: Modify a custom server's fields. Save and verify changes persist.

7. **Delete custom server**: Click delete on a custom server. Save and verify it's removed.

8. **Verify backend behavior**: After configuring approved servers, trigger a tool call in a channel from an approved server. Verify it auto-executes (Phase 3 integration test).

---

## Implementation Summary (Completed)

### Files Modified
1. **`webapp/src/components/system_console/mcp_servers.tsx`**:
   - Added `ApprovedMCPServer` type export (name, url_patterns, auto_approve_tools, enabled)
   - Extended `MCPConfig` type with optional `approvedServers` field
   - Added `approvedServers` to config initialization to preserve pass-through
   - Extended `activeTab` state to include `'approved'` option
   - Added "Approved Servers" tab button in TabsContainer
   - Added `ApprovedServersPanel` tab content rendering
   - Added import for `ApprovedServersPanel`

### Files Created
2. **`webapp/src/components/system_console/approved_servers_panel.tsx`**:
   - `BUILTIN_APPROVED_SERVERS` constant mirroring the Go backend's `BuiltinApprovedServers()`
   - `BuiltinServerItem` sub-component with expand/collapse for tool list, enabled toggle, Built-in badge
   - `CustomServerItem` sub-component with full CRUD: name, URL patterns, auto-approve tools, enabled toggle, delete
   - `ApprovedServersPanel` main component with:
     - Section header with shield icon and description
     - Built-in servers section (read-only display with enable/disable override logic)
     - Custom servers section with empty state
     - "Add Custom Approved Server" button
   - Override logic: disabling a built-in creates an override entry; re-enabling removes it
   - All styled-components following existing patterns
   - All user-facing text uses `react-intl` for i18n

### Verification
- `make check-style-fix` passes with 0 lint issues
- TypeScript type checking passes
- Go vet passes
- golangci-lint passes
