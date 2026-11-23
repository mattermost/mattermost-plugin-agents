// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type React from 'react';

export type ProjectState = 'active' | 'ending' | 'ended';

export type ProjectReplyMode = 'ambient' | 'mentions';

export type ProjectCreatedFrom = 'channel_modal' | 'dm' | 'api';

// Mirrors the server's ProjectResponse. Timestamps are milliseconds; empty
// strings and zeros mean "not set".
export type Project = {
    channel_id: string;
    team_id: string;
    agent_id: string;
    agent_bot_user_id: string;
    agent_display_name: string;
    agent_username: string;
    setup_completed: boolean;
    state: ProjectState;
    lead_user_id: string;
    reply_mode: ProjectReplyMode;
    setup_root_post_id: string;
    board_channel_id: string;
    playbook_run_id: string;
    charter_artifact_id: string;
    wrap_up_artifact_id: string;
    status_note: string;
    status_update_at: number;
    checkin_cron: string;
    checkin_timezone: string;
    next_checkin_at: number;
    last_checkin_at: number;
    last_checkin_post_id: string;
    created_by: string;
    created_from: ProjectCreatedFrom;
    create_at: number;
    update_at: number;
    ended_by: string;
    ended_at: number;
    ended_without_wrap_up: boolean;
};

export type CreateProjectRequest = {
    team_id: string;
    name: string;
    display_name: string;
    purpose?: string;
    header?: string;
    private?: boolean;

    // Attaches the project to a channel the caller already created.
    channel_id?: string;
};

export type ProjectChecklist = {
    available: boolean;
    run_id: string;
    name: string;
    status: string;
    completed_tasks: number;
    total_tasks: number;
    url: string;
};

export type ProjectTab = 'setup' | 'board' | 'checklist' | 'artifacts';

// split: beside the channel's messages; pane: over the whole center column; fullscreen: also
// over the channel sidebar.
export type ProjectPaneMode = 'split' | 'pane' | 'fullscreen';

export type ProjectPaneState = {
    open: Record<string, boolean>;
    tab: Record<string, ProjectTab>;
    mode: ProjectPaneMode;
};

// What the Boards v2 plugin publishes for other plugins to embed.
export type BoardsV2Global = {
    version: number;
    EmbeddedBoard: React.ComponentType<{channelId: string; compact?: boolean}>;
};

export type EmbeddedChecklistProps = {
    runId: string;
    channelId?: string;
    compact?: boolean;
    onOpenInPlaybooks?: () => void;
};

// What the Playbooks plugin publishes for other plugins to embed a checklist run, and to keep its
// own sidebar from opening in a channel that shows the run elsewhere.
export type PlaybooksEmbedGlobal = {
    version: number;
    EmbeddedChecklist: React.ComponentType<EmbeddedChecklistProps>;
    suppressAutoOpen?: (channelId: string) => () => void;
};

declare global {
    interface Window {
        MattermostBoardsV2?: BoardsV2Global;
        MattermostPlaybooks?: PlaybooksEmbedGlobal;
    }
}
