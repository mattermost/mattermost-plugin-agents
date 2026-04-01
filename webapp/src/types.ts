// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface CustomPrompt {
    id: string;
    creator_id: string;
    name: string;
    description: string;
    template: string;
    is_shared: boolean;
    created_at: number;
    updated_at: number;
    deleted_at: number;
}
