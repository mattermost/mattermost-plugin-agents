// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {
    ArrowExpandIcon,
    FileCodeOutlineIcon,
    FileTextOutlineIcon,
    PencilOutlineIcon,
    RefreshIcon,
    TrashCanOutlineIcon,
} from '@mattermost/compass-icons/components';

import {ChannelArtifactEntry} from '@/client';
import {getChannelArtifactsEnabled} from '@/selectors';

import {syncLibraryArtifactBookmarkTitle, unpinLibraryArtifactBookmark} from './bookmark';
import FormatBadge from './format_badge';
import {useChannelLibrary} from './library';
import {getChannelLibraryClient} from './library_client';
import {openArtifact} from './viewers/artifact_viewer';

export const ChannelArtifactsRHSTitle = () => (
    <FormattedMessage defaultMessage='Channel artifacts'/>
);

const ChannelArtifactsRHS = () => {
    const intl = useIntl();
    const store = useStore<GlobalState>();
    const enabled = useSelector(getChannelArtifactsEnabled);
    const channelId = useSelector<GlobalState, string>((state) => state.entities.channels.currentChannelId);
    const library = useChannelLibrary(channelId);
    const [bookmarkWarning, setBookmarkWarning] = useState(false);
    const [deleteError, setDeleteError] = useState(false);
    const [openId, setOpenId] = useState<string | null>(null);
    const [editingId, setEditingId] = useState<string | null>(null);
    const [editTitle, setEditTitle] = useState('');
    const renamePending = useRef(false);
    const skipRenameBlur = useRef(false);

    const [lastChannelId, setLastChannelId] = useState(channelId);
    if (lastChannelId !== channelId) {
        setLastChannelId(channelId);
        setBookmarkWarning(false);
        setDeleteError(false);
        setOpenId(null);
        setEditingId(null);
    }

    if (!enabled) {
        return (
            <Empty data-testid='channel-artifacts-rhs'>
                <FormattedMessage defaultMessage='Channel artifacts are disabled. Enable HTML artifacts and Channel artifacts in System Console.'/>
            </Empty>
        );
    }

    if (library.status === 'unsupported') {
        return (
            <Empty data-testid='channel-artifacts-unsupported'>
                <FormattedMessage defaultMessage='Channel artifacts are available in channels, not in direct or group messages.'/>
            </Empty>
        );
    }

    const entries = library.entries;
    const loading = library.status === 'loading';
    const error = library.status === 'error';
    const openEntry = entries.find((e) => e.id === openId) || null;

    const startRename = (entry: ChannelArtifactEntry) => {
        skipRenameBlur.current = false;
        setEditingId(entry.id);
        setEditTitle(entry.title);
    };

    const saveRename = async (id: string) => {
        const next = editTitle.trim();
        if (!next) {
            setEditingId(null);
            return;
        }
        if (renamePending.current) {
            return;
        }
        renamePending.current = true;
        setBookmarkWarning(false);
        try {
            const entry = await getChannelLibraryClient().rename(id, next);
            store.dispatch({type: 'CHANNEL_ARTIFACT_UPSERT', entry});
            if (channelId) {
                try {
                    await syncLibraryArtifactBookmarkTitle(store.getState(), channelId, id, entry.title || next, store.dispatch);
                } catch {
                    setBookmarkWarning(true);
                }
            }
        } catch {
            // The title stays as it was; the list is unchanged.
        } finally {
            renamePending.current = false;
            setEditingId(null);
        }
    };

    const onDelete = async (entry: ChannelArtifactEntry) => {
        // Destructive confirm: removes the entry and every version record.
        if (!window.confirm(intl.formatMessage({defaultMessage: 'Remove this artifact from the channel? The original message and file are not affected.'}))) { // eslint-disable-line no-alert
            return;
        }
        setDeleteError(false);
        try {
            await getChannelLibraryClient().remove(entry.id);
            store.dispatch({type: 'CHANNEL_ARTIFACT_REMOVE', channelId, id: entry.id});
            if (openId === entry.id) {
                setOpenId(null);
            }
            try {
                await unpinLibraryArtifactBookmark(store.getState(), channelId, entry.id, store.dispatch);
            } catch {
                // The entry is already gone; a leftover pin explains itself when opened.
            }
        } catch {
            setDeleteError(true);
        }
    };

    return (
        <Container data-testid='channel-artifacts-rhs'>
            <Toolbar>
                <Count>
                    <FormattedMessage
                        defaultMessage='{count, plural, =0 {No artifacts} one {# artifact} other {# artifacts}}'
                        values={{count: entries.length}}
                    />
                </Count>
                <IconButton
                    size='x-small'
                    icon={<Icon glyph={<RefreshIcon/>}/>}
                    aria-label={intl.formatMessage({defaultMessage: 'Refresh'})}
                    onClick={library.reload}
                    data-testid='channel-artifacts-refresh'
                />
            </Toolbar>
            {bookmarkWarning && (
                <Warning>
                    <FormattedMessage defaultMessage='Renamed the artifact, but could not update its bookmark.'/>
                </Warning>
            )}
            {deleteError && (
                <Warning>
                    <FormattedMessage defaultMessage='Could not remove the artifact.'/>
                </Warning>
            )}
            {loading && (
                <Empty><FormattedMessage defaultMessage='Loading…'/></Empty>
            )}
            {error && !loading && (
                <Empty><FormattedMessage defaultMessage='Could not load channel artifacts.'/></Empty>
            )}
            {!loading && !error && entries.length === 0 && (
                <Empty data-testid='channel-artifacts-empty'>
                    <FormattedMessage defaultMessage='No artifacts saved in this channel.'/>
                </Empty>
            )}
            <List>
                {entries.map((entry) => (
                    <Card
                        key={entry.id}
                        data-testid={`channel-artifact-${entry.id}`}
                    >
                        <Icon
                            size='16'
                            glyph={entry.format === 'markdown' ? <FileTextOutlineIcon/> : <FileCodeOutlineIcon/>}
                        />
                        <Meta>
                            {editingId === entry.id ? (
                                <RenameInput
                                    value={editTitle}
                                    maxLength={64}
                                    autoFocus={true}
                                    onChange={(e) => setEditTitle(e.target.value)}
                                    onBlur={() => {
                                        if (skipRenameBlur.current) {
                                            skipRenameBlur.current = false;
                                            return;
                                        }
                                        saveRename(entry.id);
                                    }}
                                    onKeyDown={(e) => {
                                        if (e.key === 'Enter') {
                                            skipRenameBlur.current = true;
                                            saveRename(entry.id);
                                        }
                                        if (e.key === 'Escape') {
                                            skipRenameBlur.current = true;
                                            setEditingId(null);
                                        }
                                    }}
                                    data-testid={`channel-artifact-rename-${entry.id}`}
                                />
                            ) : (
                                <Title
                                    title={entry.title}
                                    onDoubleClick={() => {
                                        if (library.canEdit) {
                                            startRename(entry);
                                        }
                                    }}
                                >
                                    {entry.title}
                                </Title>
                            )}
                            <Sub>
                                {entry.source_agent_display_name ? `${entry.source_agent_display_name} · ` : ''}
                                {new Date(entry.update_at || entry.create_at).toLocaleString()}
                                {entry.version > 1 ? ` · v${entry.version}` : ''}
                                {!entry.available && (
                                    <>
                                        {' · '}
                                        <Unavailable data-testid={`channel-artifact-unavailable-${entry.id}`}>
                                            <FormattedMessage defaultMessage='Unavailable'/>
                                        </Unavailable>
                                    </>
                                )}
                            </Sub>
                            {!entry.available && (
                                <Sub>
                                    <FormattedMessage defaultMessage='The original message or file was deleted. Open it to see earlier versions that still exist.'/>
                                </Sub>
                            )}
                        </Meta>
                        <FormatBadge entry={entry}/>
                        {entry.version > 1 && (
                            <Tag
                                size='x-small'
                                type='info'
                                label={`v${entry.version}`}
                            />
                        )}
                        <Actions>
                            <IconButton
                                size='x-small'
                                icon={<Icon glyph={<ArrowExpandIcon/>}/>}
                                aria-label={intl.formatMessage({defaultMessage: 'Open'})}
                                onClick={() => setOpenId(entry.id)}
                                data-testid={`channel-artifact-open-${entry.id}`}
                            />
                            {library.canEdit && (
                                <IconButton
                                    size='x-small'
                                    icon={<Icon glyph={<PencilOutlineIcon/>}/>}
                                    aria-label={intl.formatMessage({defaultMessage: 'Rename'})}
                                    onClick={() => startRename(entry)}
                                    data-testid={`channel-artifact-edit-${entry.id}`}
                                />
                            )}
                            {library.canDelete && (
                                <IconButton
                                    size='x-small'
                                    icon={<Icon glyph={<TrashCanOutlineIcon/>}/>}
                                    aria-label={intl.formatMessage({defaultMessage: 'Remove'})}
                                    title={intl.formatMessage({defaultMessage: 'Remove from the channel'})}
                                    onClick={() => onDelete(entry)}
                                    data-testid={`channel-artifact-delete-${entry.id}`}
                                />
                            )}
                        </Actions>
                    </Card>
                ))}
            </List>
            {openEntry && openArtifact(openEntry, {
                presentation: 'fullscreen',
                canEdit: library.canEdit,
                canDelete: library.canDelete,
                startedByClick: true,
                onClose: () => setOpenId(null),
            })}
        </Container>
    );
};

export default ChannelArtifactsRHS;

const Container = styled.div`
    display: flex;
    flex-direction: column;
    height: 100%;
    padding: var(--spacing-sm, 12px) var(--spacing-md, 16px);
    gap: var(--spacing-xs, 8px);
`;

const Toolbar = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
`;

const Count = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.02em;
`;

const List = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs, 8px);
    overflow: auto;
`;

const Card = styled.div`
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--spacing-xs, 8px);
    padding: 10px var(--spacing-sm, 12px);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 8px;
    background: var(--center-channel-bg);
`;

const Meta = styled.div`
    flex: 1;
    min-width: 0;
`;

const Title = styled.div`
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    text-overflow: ellipsis;
    white-space: nowrap;
    cursor: default;
`;

const RenameInput = styled.input`
    width: 100%;
    padding: 2px 6px;
    border: 1px solid rgba(var(--button-bg-rgb), 0.48);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
`;

const Sub = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    line-height: 16px;
`;

const Actions = styled.div`
    display: inline-flex;
    gap: var(--spacing-xxs, 4px);
`;

const Empty = styled.div`
    padding: var(--spacing-xl, 24px) var(--spacing-xs, 8px);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
    text-align: center;
`;

const Warning = styled.div`
    margin: 0 0 var(--spacing-sm, 12px);
    color: var(--error-text);
    font-size: 13px;
    line-height: 18px;
`;

const Unavailable = styled.span`
    color: var(--error-text);
    font-weight: 600;
`;
