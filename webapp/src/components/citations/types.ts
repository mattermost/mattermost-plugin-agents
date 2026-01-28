// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface Annotation {
    type: 'url_citation' | 'post_citation';
    start_index: number;
    end_index: number;
    url?: string;
    title?: string;
    cited_text?: string;
    index: number;

    // Post citation fields
    post_id?: string;
    channel_id?: string;
    channel_name?: string;
    username?: string;
}
