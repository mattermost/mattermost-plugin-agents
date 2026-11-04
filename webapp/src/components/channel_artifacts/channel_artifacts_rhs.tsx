// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Button} from '@mattermost/compass-ui/components/button';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {
    ArrowExpandIcon,
    FileCodeOutlineIcon,
    PencilOutlineIcon,
    RefreshIcon,
    TrashCanOutlineIcon,
} from '@mattermost/compass-icons/components';

import {
    ChannelArtifactEntry,
    deleteChannelArtifact,
    listChannelArtifacts,
    patchChannelArtifact,
} from '@/client';
import {getChannelArtifactsEnabled} from '@/selectors';

import HTMLArtifactFullscreen from '../html_artifacts/html_artifact_fullscreen';

import {syncLibraryArtifactBookmarkTitle} from './bookmark';

export const ChannelArtifactsRHSTitle = () => (
    <FormattedMessage defaultMessage='Channel artifacts'/>
);

const ChannelArtifactsRHS = () => {
    const intl = useIntl();
    const store = useStore<GlobalState>();
    const enabled = useSelector(getChannelArtifactsEnabled);
    const channelId = useSelector<GlobalState, string>((state) => state.entities.channels.currentChannelId);
    const [entries, setEntries] = useState<ChannelArtifactEntry[]>([]);
    const [canManage, setCanManage] = useState(false);
    const [downloadEnabled, setDownloadEnabled] = useState(true);
    const [renderEnabled, setRenderEnabled] = useState(true);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState(false);
    const [bookmarkWarning, setBookmarkWarning] = useState(false);
    const [openId, setOpenId] = useState<string | null>(null);
    const [editingId, setEditingId] = useState<string | null>(null);
    const [editTitle, setEditTitle] = useState('');
    const refreshSeq = useRef(0);
    const renamePending = useRef(false);

    const refresh = useCallback(() => {
        const seq = ++refreshSeq.current;
        if (!enabled || !channelId) {
            setEntries([]);
            return;
        }
        setLoading(true);
        setError(false);
        listChannelArtifacts(channelId).then((resp) => {
            if (seq !== refreshSeq.current) {
                return;
            }
            setEntries(resp.entries || []);
            setCanManage(Boolean(resp.can_manage));
            setDownloadEnabled(resp.download_enabled !== false);
            setRenderEnabled(resp.render_enabled !== false);
        }, () => {
            if (seq !== refreshSeq.current) {
                return;
            }
            setError(true);
            setEntries([]);
        }).finally(() => {
            if (seq === refreshSeq.current) {
                setLoading(false);
            }
        });
    }, [enabled, channelId]);

    useEffect(() => {
        refresh();
    }, [refresh]);

    useEffect(() => {
        const onCreated = () => refresh();
        window.addEventListener('focus', onCreated);
        return () => window.removeEventListener('focus', onCreated);
    }, [refresh]);

    if (!enabled) {
        return (
            <Empty data-testid='channel-artifacts-rhs'>
                <FormattedMessage defaultMessage='Channel artifacts are disabled. Enable HTML artifacts and Channel artifacts in System Console.'/>
            </Empty>
        );
    }

    const openEntry = entries.find((e) => e.id === openId) || null;

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
            const entry = await patchChannelArtifact(id, {title: next});
            if (channelId) {
                try {
                    await syncLibraryArtifactBookmarkTitle(store.getState(), channelId, id, entry.title || next, store.dispatch);
                } catch {
                    setBookmarkWarning(true);
                }
            }
            refresh();
        } catch {
            // best effort
        } finally {
            renamePending.current = false;
            setEditingId(null);
        }
    };

    const onDelete = async (entry: ChannelArtifactEntry, e: React.MouseEvent) => {
        const version = entry.version || 1;
        const purge = e.shiftKey || (e.nativeEvent as MouseEvent).altKey;

        if (purge) {
            // Destructive confirm: intentional for purge-all.
            if (!window.confirm(intl.formatMessage({defaultMessage: 'Permanently delete this artifact and all versions? This cannot be undone.'}))) { // eslint-disable-line no-alert
                return;
            }
            try {
                await deleteChannelArtifact(entry.id, {purge: true});
                if (openId === entry.id) {
                    setOpenId(null);
                }
                refresh();
            } catch {
                // best effort
            }
            return;
        }

        if (version > 1) {
            // Destructive confirm: revert head to previous version.
            if (!window.confirm(intl.formatMessage({defaultMessage: 'Remove current version and restore the previous one?'}))) { // eslint-disable-line no-alert
                return;
            }
        } else if (!window.confirm(intl.formatMessage({defaultMessage: 'Remove this artifact from the channel? The original message is not affected.'}))) { // eslint-disable-line no-alert -- sole version
            return;
        }

        try {
            await deleteChannelArtifact(entry.id);
            if (openId === entry.id) {
                setOpenId(null);
            }
            refresh();
        } catch {
            // best effort
        }
    };

    const onPurge = async (entry: ChannelArtifactEntry) => {
        // Destructive confirm: purge every version.
        if (!window.confirm(intl.formatMessage({defaultMessage: 'Permanently delete this artifact and all versions? This cannot be undone.'}))) { // eslint-disable-line no-alert
            return;
        }
        try {
            await deleteChannelArtifact(entry.id, {purge: true});
            if (openId === entry.id) {
                setOpenId(null);
            }
            refresh();
        } catch {
            // best effort
        }
    };

    return (
        <Container data-testid='channel-artifacts-rhs'>
            <Intro>
                <FormattedMessage defaultMessage='Shared interactive HTML apps built by agents. Open one anytime — like a Claude Artifact pinned to this channel. Replacing content keeps prior versions you can restore from the fullscreen viewer.'/>
            </Intro>
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
                    onClick={refresh}
                    data-testid='channel-artifacts-refresh'
                />
            </Toolbar>
            {bookmarkWarning && (
                <Warning>
                    <FormattedMessage defaultMessage='Renamed the artifact, but could not update its bookmark.'/>
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
                    <FormattedMessage defaultMessage='No artifacts yet. When an agent builds something useful, a channel admin can pin it from the artifact card.'/>
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
                            glyph={<FileCodeOutlineIcon/>}
                        />
                        <Meta>
                            {editingId === entry.id ? (
                                <RenameInput
                                    value={editTitle}
                                    maxLength={64}
                                    autoFocus={true}
                                    onChange={(e) => setEditTitle(e.target.value)}
                                    onBlur={() => saveRename(entry.id)}
                                    onKeyDown={(e) => {
                                        if (e.key === 'Enter') {
                                            saveRename(entry.id);
                                        }
                                        if (e.key === 'Escape') {
                                            setEditingId(null);
                                        }
                                    }}
                                    data-testid={`channel-artifact-rename-${entry.id}`}
                                />
                            ) : (
                                <Title
                                    title={entry.title}
                                    onDoubleClick={() => {
                                        if (!canManage) {
                                            return;
                                        }
                                        setEditingId(entry.id);
                                        setEditTitle(entry.title);
                                    }}
                                >
                                    {entry.title}
                                </Title>
                            )}
                            <Sub>
                                {entry.source_agent_display_name || 'Agent'}
                                {' · '}
                                {new Date(entry.update_at || entry.create_at).toLocaleString()}
                                {entry.version && entry.version > 1 ? ` · v${entry.version}` : ''}
                            </Sub>
                        </Meta>
                        {(entry.version || 1) > 1 && (
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
                            {canManage && (
                                <>
                                    <IconButton
                                        size='x-small'
                                        icon={<Icon glyph={<PencilOutlineIcon/>}/>}
                                        aria-label={intl.formatMessage({defaultMessage: 'Rename'})}
                                        onClick={() => {
                                            setEditingId(entry.id);
                                            setEditTitle(entry.title);
                                        }}
                                        data-testid={`channel-artifact-edit-${entry.id}`}
                                    />
                                    <IconButton
                                        size='x-small'
                                        icon={<Icon glyph={<TrashCanOutlineIcon/>}/>}
                                        aria-label={intl.formatMessage({defaultMessage: 'Remove'})}
                                        title={intl.formatMessage({defaultMessage: 'Remove current version (Shift+click to purge all)'})}
                                        onClick={(e) => onDelete(entry, e)}
                                        data-testid={`channel-artifact-delete-${entry.id}`}
                                    />
                                </>
                            )}
                        </Actions>
                        {canManage && (entry.version || 1) > 1 && (
                            <PurgeRow>
                                <Button
                                    emphasis='tertiary'
                                    size='x-small'
                                    onClick={() => onPurge(entry)}
                                    data-testid={`channel-artifact-purge-${entry.id}`}
                                >
                                    <FormattedMessage defaultMessage='Delete all versions…'/>
                                </Button>
                            </PurgeRow>
                        )}
                    </Card>
                ))}
            </List>
            {openEntry && (
                <HTMLArtifactFullscreen
                    fileId={openEntry.id}
                    fileName={openEntry.title}
                    artifactKind='library'
                    startedByClick={true}
                    canManage={canManage}
                    downloadEnabled={downloadEnabled}
                    renderEnabled={renderEnabled}
                    headVersion={openEntry.version || 1}
                    onClose={() => setOpenId(null)}
                />
            )}
            {canManage && (
                <Hint>
                    <FormattedMessage defaultMessage='Tip: save a new agent HTML file and choose the library entry to update it in place. Use the pin checkbox when saving, or Bookmarks → Add channel artifact.'/>
                </Hint>
            )}
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

const Intro = styled.p`
    margin: 0;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 13px;
    line-height: 18px;
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

const PurgeRow = styled.div`
    flex: 1 0 100%;
    display: flex;
    justify-content: flex-end;
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

const Hint = styled.div`
    margin-top: auto;
    padding-top: var(--spacing-xs, 8px);
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 12px;
    line-height: 16px;
`;
