// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getArtifactToken} from '@/client';

// Bridge tokens are cached per viewer and file for the page lifetime, so the
// inline card and the fullscreen viewer share one fetch. The key includes the
// user id so a different user on the same page never reuses a token.
const tokens = new Map<string, Promise<string>>();

function cacheKey(userId: string, fileId: string) {
    return `${userId}:${fileId}`;
}

export function fetchArtifactToken(fileId: string, userId: string): Promise<string> {
    const key = cacheKey(userId, fileId);
    let token = tokens.get(key);
    if (!token) {
        token = getArtifactToken(fileId);
        tokens.set(key, token);

        // A failed fetch is not cached, so Retry fetches again.
        token.catch(() => {
            if (tokens.get(key) === token) {
                tokens.delete(key);
            }
        });
    }
    return token;
}

// Test-only reset.
export function resetArtifactTokenCache() {
    tokens.clear();
}
