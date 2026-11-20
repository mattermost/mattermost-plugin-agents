// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Tracks the latest async request per key so a response that was superseded
// (by a newer request, a local write, or a reset) can be dropped instead of
// overwriting newer state.
export type RequestGenerations = {

    // Starts a request for key and returns its token; any earlier token for
    // the key stops being current.
    begin: (key: string) => string;
    isCurrent: (key: string, token: string) => boolean;

    // Supersedes every outstanding request for key.
    invalidate: (key: string) => void;
    invalidateAll: () => void;
};

export function createRequestGenerations(): RequestGenerations {
    const generations = new Map<string, number>();
    let epoch = 0;

    const current = (key: string) => `${epoch}:${generations.get(key) ?? 0}`;
    const bump = (key: string) => generations.set(key, (generations.get(key) ?? 0) + 1);

    return {
        begin: (key) => {
            bump(key);
            return current(key);
        },
        isCurrent: (key, token) => token === current(key),
        invalidate: (key) => {
            bump(key);
        },
        invalidateAll: () => {
            epoch++;
        },
    };
}
