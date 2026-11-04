// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type AnnotationType = 'url_citation' | 'mattermost_channel';

export interface Annotation {
    type: AnnotationType;
    start_index: number;
    end_index: number;
    url?: string;
    title?: string;
    cited_text?: string;
    index: number;
    channel_id?: string;
    channel_name?: string;
    private?: boolean;
}
