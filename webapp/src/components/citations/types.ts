// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface Annotation {
    type: string;
    start_index: number;
    end_index: number;
    url: string;
    title: string;
    cited_text?: string;
    index: number;
}
