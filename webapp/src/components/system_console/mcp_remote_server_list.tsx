// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {ChevronRightIcon, TrashCanOutlineIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tag} from '@mattermost/compass-ui/components/tag';

import ConfirmationDialog from '../confirmation_dialog';

import {MCPServerConfig, MCPToolsResponse} from './mcp_types';

type Props = {
    servers: MCPServerConfig[];

    // Discovered tools, when loaded, for counting tools without a chosen policy.
    toolsData: MCPToolsResponse | null;
    onEdit: (server: MCPServerConfig) => void;
    onDelete: (server: MCPServerConfig) => Promise<void>;
};

const DELETE_DIALOG_TITLE_ID = 'mcp-delete-server-title';

// unreviewedCount counts the server's discovered tools that have no chosen
// policy, or null when its tools are not loaded.
const unreviewedCount = (server: MCPServerConfig, toolsData: MCPToolsResponse | null): number | null => {
    const discovered = toolsData?.servers.find((s) => s.serverType === 'remote' && (server.id ? s.id === server.id : s.url === server.baseURL));
    if (!discovered || discovered.error || discovered.needsOAuth) {
        return null;
    }
    const chosen = new Set((server.tool_configs ?? []).filter((tc) => tc.policy).map((tc) => tc.name));
    return discovered.tools.filter((tool) => !chosen.has(tool.name)).length;
};

const MCPRemoteServerList = ({servers, toolsData, onEdit, onDelete}: Props) => {
    const intl = useIntl();
    const [pendingDelete, setPendingDelete] = useState<MCPServerConfig | null>(null);
    const [deleting, setDeleting] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);

    const confirmDelete = async () => {
        if (!pendingDelete) {
            return;
        }
        setDeleting(true);
        setDeleteError(null);
        try {
            await onDelete(pendingDelete);
            setPendingDelete(null);
        } catch (err) {
            setDeleteError(err instanceof Error && err.message ? err.message : intl.formatMessage({defaultMessage: 'Failed to delete the server.'}));
            setPendingDelete(null);
        } finally {
            setDeleting(false);
        }
    };

    return (
        <>
            {deleteError && (
                <SectionNotice
                    type='danger'
                    title={deleteError}
                />
            )}
            <List aria-label={intl.formatMessage({defaultMessage: 'Remote MCP servers'})}>
                {servers.map((server, index) => {
                    const count = unreviewedCount(server, toolsData);
                    const name = server.name || intl.formatMessage({defaultMessage: 'Server {number}'}, {number: index + 1});
                    return (
                        <Row key={server.id ?? index}>
                            <RowButton
                                type='button'
                                onClick={() => onEdit(server)}
                                disabled={!server.id}
                                aria-label={intl.formatMessage({defaultMessage: 'Edit {name}'}, {name})}
                            >
                                <Avatar aria-hidden={true}>{name.charAt(0).toUpperCase()}</Avatar>
                                <RowText>
                                    <RowName>{name}</RowName>
                                    <RowURL>{server.baseURL}</RowURL>
                                </RowText>
                                <RowTags>
                                    {!server.enabled && (
                                        <Tag label={<FormattedMessage defaultMessage='Disabled'/>}/>
                                    )}
                                    {count !== null && count > 0 && (
                                        <Tag
                                            type='warning'
                                            label={(
                                                <FormattedMessage
                                                    defaultMessage='{count, plural, one {# tool not reviewed} other {# tools not reviewed}}'
                                                    values={{count}}
                                                />
                                            )}
                                        />
                                    )}
                                </RowTags>
                                <Chevron aria-hidden={true}>
                                    <ChevronRightIcon size={16}/>
                                </Chevron>
                            </RowButton>
                            <IconButton
                                icon={<Icon glyph={<TrashCanOutlineIcon/>}/>}
                                size='small'
                                destructive={true}
                                onClick={() => setPendingDelete(server)}
                                disabled={!server.id}
                                aria-label={intl.formatMessage({defaultMessage: 'Delete {name}'}, {name})}
                            />
                        </Row>
                    );
                })}
            </List>
            <ConfirmationDialog
                show={pendingDelete !== null}
                titleId={DELETE_DIALOG_TITLE_ID}
                title={intl.formatMessage({defaultMessage: 'Delete {name}?'}, {name: pendingDelete?.name ?? ''})}
                message={<FormattedMessage defaultMessage='Agents and users lose access to the server’s tools, and its tool policies are removed. This takes effect right away.'/>}
                confirmButtonText={deleting ? <FormattedMessage defaultMessage='Deleting...'/> : <FormattedMessage defaultMessage='Delete'/>}
                onConfirm={confirmDelete}
                onCancel={() => setPendingDelete(null)}
                confirmPending={deleting}
                isDestructive={true}
                managedAccessibility={true}
                zIndex={2100}
            />
        </>
    );
};

const List = styled.ul`
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: var(--radius-s);
    overflow: hidden;
`;

const Row = styled.li`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    padding-right: var(--spacing-m);
    background: var(--center-channel-bg);

    & + & {
        border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const RowButton = styled.button`
    display: flex;
    align-items: center;
    flex: 1;
    min-width: 0;
    gap: var(--spacing-s);
    padding: var(--spacing-m);
    border: none;
    background: none;
    text-align: left;
    font: inherit;
    color: inherit;
    cursor: pointer;

    &:hover:not(:disabled) {
        background-color: rgba(var(--center-channel-color-rgb), 0.04);
    }

    &:disabled {
        cursor: default;
    }
`;

const Avatar = styled.span`
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: var(--spacing-xxxxl);
    height: var(--spacing-xxxxl);
    border-radius: var(--radius-full);
    background: rgba(var(--center-channel-color-rgb), 0.08);
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
`;

const RowText = styled.span`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
`;

const RowName = styled.span`
    font-weight: var(--font-weight-semibold);
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);
    color: var(--center-channel-color);
`;

const RowURL = styled.span`
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const RowTags = styled.span`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    flex-shrink: 0;
`;

const Chevron = styled.span`
    display: flex;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

export default MCPRemoteServerList;
