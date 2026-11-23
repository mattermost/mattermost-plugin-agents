// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import styled from 'styled-components';

import type {GlobalState} from '@mattermost/types/store';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {ArrowCollapseIcon, ArrowExpandAllIcon, ArrowExpandIcon, CloseIcon} from '@mattermost/compass-icons/components';

import manifest from '@/manifest';
import {getProject, getProjectChannelsEnabled} from '@/selectors';
import type {ProjectPaneMode, ProjectPaneState} from '@/types/projects';

import {isChannelRoute, useBrowserLocation} from './browser_location';
import {PANE_DEFAULT_WIDTH, clampPaneWidth, effectivePaneMode} from './pane_geometry';
import {PaneLayoutStyle} from './pane_layout_style';
import {closeProjectPane, getProjectPane, restoreProjectPane, setProjectPaneMode} from './pane_state';
import ProjectPane from './project_pane';
import ResizeHandle from './resize_handle';

const stateKey = (userId: string) => `mm-ai.projectPane.${userId}`;
const widthKey = (userId: string) => `mm-ai.projectPane.${userId}.width`;

function readStored<T>(key: string): T | null {
    try {
        const raw = window.localStorage.getItem(key);
        return raw ? JSON.parse(raw) as T : null;
    } catch {
        return null;
    }
}

function writeStored(key: string, value: unknown) {
    try {
        window.localStorage.setItem(key, JSON.stringify(value));
    } catch {
        // Storage can be full or blocked; the pane still works for this session.
    }
}

// Restores the user's open panes, tabs and layout once, then saves every change.
function usePanePersistence(userId: string, pane: ProjectPaneState) {
    const dispatch = useDispatch();
    const restoredFor = useRef('');

    useEffect(() => {
        if (!userId || restoredFor.current === userId) {
            return;
        }
        restoredFor.current = userId;
        const stored = readStored<Partial<ProjectPaneState>>(stateKey(userId));
        if (stored) {
            dispatch(restoreProjectPane(stored));
        }
    }, [userId, dispatch]);

    useEffect(() => {
        if (userId && restoredFor.current === userId) {
            writeStored(stateKey(userId), pane);
        }
    }, [userId, pane]);
}

// Keeps Playbooks from opening its own sidebar in project channels, where the checklist shows in
// the project pane. Playbooks decides on channel entry, so every known project channel is
// suppressed up front, not only the current one. Playbooks may load after this plugin.
function usePlaybooksAutoOpenSuppressed(channelIds: string[]) {
    const key = channelIds.join(',');

    useEffect(() => {
        if (!key) {
            return () => {/* nothing suppressed */};
        }
        const ids = key.split(',');
        const releases: Array<() => void> = [];
        let attempts = 0;
        let timer = 0;
        const suppress = () => {
            const fn = window.MattermostPlaybooks?.suppressAutoOpen;
            if (typeof fn === 'function') {
                for (const id of ids) {
                    releases.push(fn(id));
                }
                return;
            }
            if (attempts < 20) {
                attempts++;
                timer = window.setTimeout(suppress, 250);
            }
        };
        suppress();
        return () => {
            window.clearTimeout(timer);
            for (const release of releases) {
                release();
            }
        };
    }, [key]);
}

const isEditable = (target: EventTarget | null) => target instanceof HTMLElement &&
    (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName));

type FrameProps = {
    channelId: string;
    userId: string;
    mode: ProjectPaneMode;
};

// The pane beside the channel: its width, layout mode and the host layout it needs.
const ProjectPaneFrame = ({channelId, userId, mode}: FrameProps) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const paneRef = useRef<HTMLElement>(null);
    const lastPostColumnWidth = useRef(0);
    const [available, setAvailable] = useState(0);
    const [viewport, setViewport] = useState(() => window.innerWidth);
    const [width, setWidth] = useState(() => readStored<number>(widthKey(userId)) || PANE_DEFAULT_WIDTH);
    const [dragging, setDragging] = useState(false);
    const [postColumnWidth, setPostColumnWidth] = useState(0);

    const effective = available ? effectivePaneMode(mode, available, viewport) : mode;
    const shownWidth = available ? clampPaneWidth(available, width) : width;

    const setPaneWidthVar = (px: number) => paneRef.current?.style.setProperty('--mm-ai-project-pane-width', `${px}px`);

    useLayoutEffect(() => {
        const container = paneRef.current?.parentElement;
        if (!container) {
            return () => {/* nothing measured */};
        }
        const measure = () => {
            setAvailable(container.clientWidth);
            setViewport(window.innerWidth);
            const postColumn = container.querySelector<HTMLElement>(':scope > .inner-wrap');
            if (postColumn && getComputedStyle(postColumn).visibility !== 'hidden') {
                lastPostColumnWidth.current = postColumn.offsetWidth;
            }
        };
        measure();
        const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure);
        observer?.observe(container);
        window.addEventListener('resize', measure);
        return () => {
            observer?.disconnect();
            window.removeEventListener('resize', measure);
        };
    }, []);

    useLayoutEffect(() => {
        setPaneWidthVar(shownWidth);
    }, [shownWidth]);

    useLayoutEffect(() => {
        if (effective === 'split') {
            return;
        }
        setPostColumnWidth(lastPostColumnWidth.current);

        // The hidden messages can't keep the focus.
        const postColumn = paneRef.current?.parentElement?.querySelector(':scope > .inner-wrap');
        if (postColumn && document.activeElement && postColumn.contains(document.activeElement)) {
            paneRef.current?.focus();
        }
    }, [effective]);

    const commitWidth = (next: number) => {
        setWidth(next);
        setPaneWidthVar(next);
        writeStored(widthKey(userId), next);
    };

    const modeButton = (target: ProjectPaneMode, label: string, glyph: React.ReactNode, testId: string) => (
        <IconButton
            size='small'
            icon={<Icon glyph={glyph}/>}
            aria-label={label}
            title={label}
            onClick={() => dispatch(setProjectPaneMode(target))}
            data-testid={testId}
        />
    );

    const controls = (
        <>
            {effective === 'split' ? (
                modeButton('pane', intl.formatMessage({defaultMessage: 'Expand over the channel'}), <ArrowExpandIcon/>, 'project-pane-expand')
            ) : (
                mode !== 'split' && modeButton('split', intl.formatMessage({defaultMessage: 'Show the channel beside the project'}), <ArrowCollapseIcon/>, 'project-pane-split')
            )}
            {effective !== 'fullscreen' && modeButton('fullscreen', intl.formatMessage({defaultMessage: 'Expand to the full window'}), <ArrowExpandAllIcon/>, 'project-pane-fullscreen')}
            <IconButton
                size='small'
                icon={<Icon glyph={<CloseIcon/>}/>}
                aria-label={intl.formatMessage({defaultMessage: 'Close the project pane'})}
                title={intl.formatMessage({defaultMessage: 'Close the project pane'})}
                onClick={() => dispatch(closeProjectPane(channelId))}
                data-testid='project-pane-close'
            />
        </>
    );

    return (
        <>
            <PaneLayoutStyle $postColumnWidth={postColumnWidth}/>

            {/* eslint-disable-next-line agents/no-noninteractive-element-interactions -- the pane only stops typed characters from leaving it, so Mattermost doesn't move them to the channel's message box */}
            <Pane
                ref={paneRef}
                data-mm-ai-project-pane={effective}
                data-testid='project-pane-host'
                aria-label={intl.formatMessage({defaultMessage: 'Project'})}
                tabIndex={-1}
                $resizing={dragging}
                onKeyDown={(e) => {
                    if (!e.ctrlKey && !e.metaKey && !e.altKey && e.key.length === 1 && !isEditable(e.target)) {
                        e.stopPropagation();
                    }
                }}
            >
                {effective === 'split' && (
                    <ResizeHandle
                        width={shownWidth}
                        available={available}
                        onResize={setPaneWidthVar}
                        onResizeEnd={commitWidth}
                        onDragChange={setDragging}
                    />
                )}
                <Content $resizing={dragging}>
                    <ProjectPane
                        channelId={channelId}
                        controls={controls}
                        showMessagesHint={mode === 'split' && effective !== 'split'}
                    />
                </Content>
            </Pane>
        </>
    );
};

const pluginState = (state: GlobalState) => (state as unknown as Record<string, {projects?: {byChannelId?: Record<string, unknown>}} | undefined>)[`plugins-${manifest.id}`];

// Root component: shows the project pane beside the channel when the current channel is a project
// whose pane is open, and nowhere else (not in Threads, Drafts or other views that keep the last
// channel selected).
const ProjectPaneHost = () => {
    const enabled = useSelector((state: GlobalState) => getProjectChannelsEnabled(state as never));
    const userId = useSelector((state: GlobalState) => state.entities.users.currentUserId);
    const channelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const channelName = useSelector((state: GlobalState) => state.entities.channels.channels[channelId]?.name ?? '');
    const teamName = useSelector((state: GlobalState) => {
        const teamId = state.entities.channels.channels[channelId]?.team_id || state.entities.teams.currentTeamId;
        return state.entities.teams.teams[teamId]?.name ?? '';
    });
    const isProject = useSelector((state: GlobalState) => Boolean(getProject(state as never, channelId)));
    const pane = useSelector((state: GlobalState) => getProjectPane(state as never));
    const projectIdsKey = useSelector((state: GlobalState) => (enabled ? Object.keys(pluginState(state)?.projects?.byChannelId ?? {}).sort().join(',') : ''));
    const projectChannelIds = useMemo(() => (projectIdsKey ? projectIdsKey.split(',') : []), [projectIdsKey]);
    const location = useBrowserLocation();

    usePanePersistence(userId, pane);
    usePlaybooksAutoOpenSuppressed(projectChannelIds);

    if (!enabled || !isProject || !pane.open[channelId] || !isChannelRoute(location.pathname, teamName, channelName)) {
        return null;
    }
    return (
        <ProjectPaneFrame
            key={channelId}
            channelId={channelId}
            userId={userId}
            mode={pane.mode}
        />
    );
};

export default ProjectPaneHost;

const Pane = styled.aside<{$resizing: boolean}>`
    position: relative;
    display: flex;
    flex: 0 0 var(--mm-ai-project-pane-width, ${PANE_DEFAULT_WIDTH}px);
    flex-direction: column;
    min-width: 0;
    height: 100%;
    border-left: var(--border-light);
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    ${({$resizing}) => $resizing && 'cursor: col-resize; user-select: none;'}

    &[data-mm-ai-project-pane='pane'],
    &[data-mm-ai-project-pane='fullscreen'] {
        flex: 1 1 auto;
        border-left: none;
    }

    &:focus {
        outline: none;
    }
`;

// While the pane is resized, nothing inside it, such as an artifact's frame, takes the pointer.
const Content = styled.div<{$resizing: boolean}>`
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
    ${({$resizing}) => $resizing && 'pointer-events: none;'}
`;
