// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from '@/manifest';
import {ProjectPaneClosed, ProjectPaneModeSet, ProjectPaneOpened, ProjectPaneRestored, ProjectPaneTabSet} from '@/redux';
import type {Project, ProjectPaneMode, ProjectPaneState, ProjectTab} from '@/types/projects';

export const PROJECT_TABS: readonly ProjectTab[] = ['setup', 'board', 'checklist', 'artifacts'];

export const isProjectTab = (value: unknown): value is ProjectTab => PROJECT_TABS.includes(value as ProjectTab);

export const openProjectPane = (channelId: string, tab?: ProjectTab) => ({type: ProjectPaneOpened, channelId, tab});
export const closeProjectPane = (channelId: string) => ({type: ProjectPaneClosed, channelId});
export const setProjectPaneTab = (channelId: string, tab: ProjectTab) => ({type: ProjectPaneTabSet, channelId, tab});
export const setProjectPaneMode = (mode: ProjectPaneMode) => ({type: ProjectPaneModeSet, mode});
export const restoreProjectPane = (state: Partial<ProjectPaneState>) => ({type: ProjectPaneRestored, state});

type PaneAppState = {[key: string]: unknown};

const EMPTY_PANE: ProjectPaneState = {open: {}, tab: {}, mode: 'split'};

export const getProjectPane = (state: PaneAppState): ProjectPaneState =>
    (state[`plugins-${manifest.id}`] as {projectPane?: ProjectPaneState} | undefined)?.projectPane ?? EMPTY_PANE;

export const isProjectPaneOpen = (state: PaneAppState, channelId: string): boolean => Boolean(getProjectPane(state).open[channelId]);

export const getProjectPaneTab = (state: PaneAppState, channelId: string): ProjectTab | undefined => getProjectPane(state).tab[channelId];

export const getProjectPaneMode = (state: PaneAppState): ProjectPaneMode => getProjectPane(state).mode;

// The tabs a project shows: Setup while its setup thread runs (or while someone keeps it open), the
// board and checklist once the agent created them, and always the artifacts.
export function visibleProjectTabs(project: Project, selected?: ProjectTab): ProjectTab[] {
    const tabs: ProjectTab[] = [];
    if (project.setup_root_post_id && (!project.setup_completed || selected === 'setup')) {
        tabs.push('setup');
    }
    if (project.board_channel_id) {
        tabs.push('board');
    }
    if (project.playbook_run_id) {
        tabs.push('checklist');
    }
    tabs.push('artifacts');
    return tabs;
}

// The tab the pane shows: the chosen one when the project has it, else the first it has.
export function selectedProjectTab(project: Project, chosen?: ProjectTab): ProjectTab {
    const tabs = visibleProjectTabs(project, chosen);
    return chosen && tabs.includes(chosen) ? chosen : tabs[0];
}
