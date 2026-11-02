// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import {useSelector, useStore} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';

import {ChannelArtifactEntry, listChannelArtifacts} from '@/client';
import {getChannelArtifactsEnabled} from '@/selectors';

import HTMLArtifactFullscreen from '../html_artifacts/html_artifact_fullscreen';

import {parseLibraryArtifactIdFromSearch, stripLibraryArtifactQuery} from './bookmark';

type NoticeKind = 'disabled' | 'unavailable' | null;

/**
 * Root listener: channel bookmark links carry
 * `?mm_ai_library_artifact=<id>` on a normal channel URL. Opening that URL
 * loads the library artifact fullscreen and then strips the query so refresh
 * does not re-open the viewer.
 *
 * history.replace (strip) must not re-trigger a load for the same id — that
 * races with window.location still showing the old search and loops forever.
 */
const LibraryArtifactDeepLink = () => {
    const store = useStore<GlobalState>();
    const enabled = useSelector(getChannelArtifactsEnabled);
    const [entry, setEntry] = useState<ChannelArtifactEntry | null>(null);
    const [canManage, setCanManage] = useState(false);
    const [downloadEnabled, setDownloadEnabled] = useState(true);
    const [renderEnabled, setRenderEnabled] = useState(true);
    const [notice, setNotice] = useState<NoticeKind>(null);
    const openingRef = useRef<string | null>(null);
    const openedIdsRef = useRef<Set<string>>(new Set());

    const stripQuery = useCallback(() => {
        const {pathname, search, hash} = window.location;
        const next = stripLibraryArtifactQuery(pathname, search, hash);
        const current = pathname + search + hash;
        if (next === current) {
            return;
        }
        const history = window.WebappUtils?.browserHistory;
        if (history?.replace) {
            history.replace(next);
        } else {
            window.history.replaceState(window.history.state, '', next);
        }
    }, []);

    const tryOpen = useCallback(async (search = window.location.search) => {
        const artifactId = parseLibraryArtifactIdFromSearch(search);
        if (!artifactId) {
            return;
        }
        if (openingRef.current === artifactId || openedIdsRef.current.has(artifactId)) {
            return;
        }
        openingRef.current = artifactId;
        openedIdsRef.current.add(artifactId);

        if (!enabled) {
            setNotice('disabled');
            stripQuery();
            openingRef.current = null;
            return;
        }

        const state = store.getState();
        const channelId = state.entities.channels.currentChannelId;
        if (!channelId) {
            openedIdsRef.current.delete(artifactId);
            openingRef.current = null;
            return;
        }

        try {
            const list = await listChannelArtifacts(channelId);
            const match = (list.entries || []).find((e) => e.id === artifactId);
            if (!match || match.channel_id !== channelId) {
                setNotice('unavailable');
                stripQuery();
                openingRef.current = null;
                return;
            }
            setCanManage(Boolean(list.can_manage));
            setDownloadEnabled(list.download_enabled !== false);
            setRenderEnabled(list.render_enabled !== false);
            setEntry(match);
            stripQuery();
        } catch {
            setNotice('unavailable');
            stripQuery();
            openingRef.current = null;
        }
    }, [enabled, store, stripQuery]);

    useEffect(() => {
        tryOpen();

        const history = window.WebappUtils?.browserHistory;
        const unlisten = history?.listen?.((location: {search?: string}) => {
            const search = location?.search ?? window.location.search;
            const id = parseLibraryArtifactIdFromSearch(search);
            if (!id) {
                // Our strip finished (or unrelated nav without the param).
                openingRef.current = null;
                return;
            }
            if (openedIdsRef.current.has(id) || openingRef.current === id) {
                return;
            }
            tryOpen(search);
        });
        const onPop = () => {
            const id = parseLibraryArtifactIdFromSearch(window.location.search);
            if (id) {
                openedIdsRef.current.delete(id);
            }
            openingRef.current = null;
            tryOpen();
        };
        window.addEventListener('popstate', onPop);
        return () => {
            unlisten?.();
            window.removeEventListener('popstate', onPop);
        };
    }, [tryOpen]);

    return (
        <>
            {notice && (
                <NoticeBanner data-testid='library-artifact-deep-link-notice'>
                    <SectionNotice
                        type='danger'
                        title={notice === 'disabled' ? (
                            <FormattedMessage defaultMessage='Channel artifacts are disabled on this server.'/>
                        ) : (
                            <FormattedMessage defaultMessage='This channel artifact is unavailable.'/>
                        )}
                        primaryButtonLabel={<FormattedMessage defaultMessage='Dismiss'/>}
                        onPrimaryAction={() => setNotice(null)}
                    />
                </NoticeBanner>
            )}
            {entry && (
                <HTMLArtifactFullscreen
                    fileId={entry.id}
                    fileName={entry.title}
                    artifactKind='library'
                    startedByClick={true}
                    canManage={canManage}
                    downloadEnabled={downloadEnabled}
                    renderEnabled={renderEnabled}
                    headVersion={entry.version || 1}
                    onClose={() => {
                        setEntry(null);
                        openingRef.current = null;
                        // Allow the same bookmark to open again after close.
                        if (entry.id) {
                            openedIdsRef.current.delete(entry.id);
                        }
                    }}
                />
            )}
        </>
    );
};

export default LibraryArtifactDeepLink;

const NoticeBanner = styled.div`
    position: fixed;
    top: var(--spacing-md, 16px);
    left: 50%;
    z-index: 11000;
    width: min(480px, calc(100% - var(--spacing-xl, 24px)));
    transform: translateX(-50%);
`;
