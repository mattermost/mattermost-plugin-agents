// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// What registerRightHandSidebarComponent returns for the Project sidebar: the id the host keeps in
// views.rhs.pluggableId, and the actions that show, hide and toggle it.
export type ProjectRHSHandle = {
    id: string;
    showRHSPlugin: unknown;
    hideRHSPlugin: unknown;
    toggleRHSPlugin: unknown;
};

let handle: ProjectRHSHandle | null = null;

export const setProjectRHSHandle = (next: ProjectRHSHandle | null) => {
    handle = next;
};

export const getProjectRHSHandle = () => handle;
