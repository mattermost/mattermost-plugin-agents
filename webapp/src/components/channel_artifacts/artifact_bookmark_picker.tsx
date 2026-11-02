// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Button} from '@mattermost/compass-ui/components/button';
import {FileCodeOutlineIcon} from '@mattermost/compass-icons/components';
import {Icon} from '@mattermost/compass-ui/components/icon';

import {ChannelArtifactEntry, listChannelArtifacts} from '@/client';
import {getChannelArtifactsEnabled} from '@/selectors';

import {
    artifactIdFromBookmarkUrl,
    canAddChannelBookmarks,
    pinLibraryArtifactBookmark,
} from './bookmark';

type OpenRequest = {channelId: string; nonce: number};

let openRequest: OpenRequest | null = null;
const listeners = new Set<() => void>();

/** Opens the channel-artifact bookmark picker for `channelId` (from the Bookmarks + menu). */
export function openArtifactBookmarkPicker(channelId: string) {
    openRequest = {channelId, nonce: Date.now()};
    listeners.forEach((l) => l());
}

/**
 * Root component: listens for openArtifactBookmarkPicker and shows a list of
 * library artifacts to pin via the native bookmarks add flow.
 */
const ArtifactBookmarkPicker = () => {
    const intl = useIntl();
    const store = useStore<GlobalState>();
    const enabled = useSelector(getChannelArtifactsEnabled);
    const [request, setRequest] = useState<OpenRequest | null>(null);
    const [entries, setEntries] = useState<ChannelArtifactEntry[]>([]);
    const [pinnedIds, setPinnedIds] = useState<Set<string>>(() => new Set());
    const [loading, setLoading] = useState(false);
    const [pinningId, setPinningId] = useState<string | null>(null);
    const [error, setError] = useState('');

    useEffect(() => {
        const sync = () => setRequest(openRequest);
        listeners.add(sync);
        return () => {
            listeners.delete(sync);
        };
    }, []);

    const channelId = request?.channelId || '';
    const canPin = useSelector((state: GlobalState) => canAddChannelBookmarks(state, channelId));
    const bookmarksById = useSelector((state: GlobalState) => {
        if (!channelId) {
            return null;
        }
        return state.entities.channelBookmarks?.byChannelId?.[channelId] ?? null;
    });

    const close = useCallback(() => {
        openRequest = null;
        setRequest(null);
        setEntries([]);
        setPinnedIds(new Set());
        setError('');
        setPinningId(null);
        setLoading(false);
    }, []);

    useEffect(() => {
        if (!request) {
            return undefined; // eslint-disable-line no-undefined
        }
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                close();
            }
        };
        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, [request, close]);

    useEffect(() => {
        if (!request || !enabled) {
            return undefined; // eslint-disable-line no-undefined
        }
        if (!canPin) {
            setLoading(false);
            setError(intl.formatMessage({defaultMessage: 'You do not have permission to add bookmarks in this channel.'}));
            setEntries([]);
            return undefined; // eslint-disable-line no-undefined
        }
        let cancelled = false;
        setLoading(true);
        setError('');
        listChannelArtifacts(request.channelId).then((list) => {
            if (cancelled) {
                return;
            }
            setEntries(list.entries || []);
        }, () => {
            if (!cancelled) {
                setError(intl.formatMessage({defaultMessage: 'Could not load channel artifacts.'}));
                setEntries([]);
            }
        }).finally(() => {
            if (!cancelled) {
                setLoading(false);
            }
        });
        return () => {
            cancelled = true;
        };

        // intl.formatMessage is stable enough; omit intl to avoid reload loops.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [request, enabled, canPin]);

    useEffect(() => {
        const pinned = new Set<string>();
        if (bookmarksById) {
            for (const b of Object.values(bookmarksById)) {
                const id = artifactIdFromBookmarkUrl(b.link_url);
                if (id) {
                    pinned.add(id);
                }
            }
        }
        setPinnedIds(pinned);
    }, [bookmarksById]);

    if (!request || !enabled) {
        return null;
    }

    const onPick = async (entry: ChannelArtifactEntry) => {
        if (pinnedIds.has(entry.id) || pinningId) {
            return;
        }
        setPinningId(entry.id);
        setError('');
        try {
            await pinLibraryArtifactBookmark(store.getState(), request.channelId, entry.id, entry.title, store.dispatch);
            close();
        } catch {
            setError(intl.formatMessage({defaultMessage: 'Could not add bookmark.'}));
            setPinningId(null);
        }
    };

    return (
        <Layer>
            <Backdrop
                type='button'
                aria-label={intl.formatMessage({defaultMessage: 'Close'})}
                data-testid='channel-artifact-bookmark-picker'
                onClick={close}
            />
            <Card
                role='dialog'
                aria-modal='true'
                aria-labelledby='channel-artifact-bookmark-picker-title'
            >
                <Heading id='channel-artifact-bookmark-picker-title'>
                    <FormattedMessage defaultMessage='Add channel artifact'/>
                </Heading>
                <Copy>
                    <FormattedMessage defaultMessage='Choose a saved library artifact to pin on this channel bookmark bar. Everyone can open it from there.'/>
                </Copy>
                {loading && (
                    <Empty><FormattedMessage defaultMessage='Loading…'/></Empty>
                )}
                {!loading && error && <ErrorText>{error}</ErrorText>}
                {!loading && !error && entries.length === 0 && (
                    <Empty data-testid='channel-artifact-bookmark-picker-empty'>
                        <FormattedMessage defaultMessage='No artifacts in this channel yet. A channel admin can save one from an agent HTML card first.'/>
                    </Empty>
                )}
                <List>
                    {entries.map((entry) => {
                        const pinned = pinnedIds.has(entry.id);
                        return (
                            <Row
                                key={entry.id}
                                type='button'
                                disabled={pinned || pinningId === entry.id}
                                onClick={() => onPick(entry)}
                                data-testid={`channel-artifact-bookmark-pick-${entry.id}`}
                            >
                                <Icon
                                    size='16'
                                    glyph={<FileCodeOutlineIcon/>}
                                />
                                <RowMeta>
                                    <RowTitle>{entry.title}</RowTitle>
                                    <RowSub>
                                        {entry.source_agent_display_name || 'Agent'}
                                        {entry.version && entry.version > 1 ? ` · v${entry.version}` : ''}
                                    </RowSub>
                                </RowMeta>
                                <RowStatus>
                                    {pinned ? (
                                        <FormattedMessage defaultMessage='Pinned'/>
                                    ) : (
                                        <FormattedMessage defaultMessage='Add'/>
                                    )}
                                </RowStatus>
                            </Row>
                        );
                    })}
                </List>
                <Actions>
                    <Button
                        emphasis='tertiary'
                        size='small'
                        onClick={close}
                    >
                        <FormattedMessage defaultMessage='Cancel'/>
                    </Button>
                </Actions>
            </Card>
        </Layer>
    );
};

export default ArtifactBookmarkPicker;

const Layer = styled.div`
    position: fixed;
    inset: 0;
    z-index: 11000;
`;

const Backdrop = styled.button`
    position: absolute;
    inset: 0;
    border: 0;
    padding: 0;
    background: rgba(0, 0, 0, 0.48);
    cursor: pointer;
`;

const Card = styled.div`
    position: absolute;
    top: 50%;
    left: 50%;
    z-index: 1;
    width: min(440px, calc(100% - 48px));
    max-height: min(560px, calc(100% - 48px));
    display: flex;
    flex-direction: column;
    padding: 24px;
    border-radius: 12px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.24);
    transform: translate(-50%, -50%);
    overflow: auto;
`;

const Heading = styled.h2`
    margin: 0 0 8px;
    font-size: 18px;
    font-weight: 600;
    line-height: 24px;
`;

const Copy = styled.p`
    margin: 0 0 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
`;

const List = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
    overflow: auto;
    margin: 0 -8px;
    padding: 0 8px;
`;

const Row = styled.button`
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 10px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 8px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    text-align: left;
    cursor: pointer;

    &:hover:not(:disabled) {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }

    &:disabled {
        cursor: default;
        opacity: 0.72;
    }
`;

const RowMeta = styled.div`
    flex: 1;
    min-width: 0;
`;

const RowTitle = styled.div`
    overflow: hidden;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const RowSub = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    line-height: 16px;
`;

const RowStatus = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    font-weight: 600;
`;

const Empty = styled.div`
    padding: 16px 8px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
    text-align: center;
`;

const ErrorText = styled.div`
    margin-bottom: 8px;
    color: var(--error-text);
    font-size: 13px;
    line-height: 18px;
`;

const Actions = styled.div`
    display: flex;
    justify-content: flex-end;
    margin-top: 16px;
`;
