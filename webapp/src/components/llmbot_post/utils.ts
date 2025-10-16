// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PermalinkData} from './types';

export function extractPermalinkData(post: any): PermalinkData | null {
    for (const embed of post?.metadata?.embeds || []) {
        if (embed.type === 'permalink') {
            return embed.data;
        }
    }
    return null;
}

