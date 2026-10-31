// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface AIThread {
    id: string;
    channel_id: string | null;
    bot_id: string;
    root_post_id: string | null;
    title: string;
    turn_count: number;
    update_at: number;
}
