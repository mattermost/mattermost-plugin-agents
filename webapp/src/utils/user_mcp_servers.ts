// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Shared, short-lived cache of the current user's MCP server list
// (GET /mcp/tools without an agent or catalog filter). Every caller that needs
// the user's connection status reads through here so several cards and the RHS
// share one discovery request instead of each triggering their own.

import {getUserMCPTools, type UserMCPToolsResponse} from '@/client';

const MAX_AGE_MS = 30 * 1000;

let cached: {response: UserMCPToolsResponse; fetchedAt: number} | null = null;
let inFlight: Promise<UserMCPToolsResponse> | null = null;

// Bumped on invalidation so a request that started before a connection change
// cannot repopulate the cache with stale status.
let generation = 0;

export function getUserMCPServers(): Promise<UserMCPToolsResponse> {
    if (cached && Date.now() - cached.fetchedAt < MAX_AGE_MS) {
        return Promise.resolve(cached.response);
    }
    if (inFlight) {
        return inFlight;
    }
    const startedGeneration = generation;
    const request = getUserMCPTools().then((response) => {
        if (startedGeneration === generation) {
            cached = {response, fetchedAt: Date.now()};
        }
        return response;
    }).finally(() => {
        if (inFlight === request) {
            inFlight = null;
        }
    });
    inFlight = request;
    return request;
}

/** Seed the cache with a response fetched elsewhere (e.g. a forced refresh). */
export function setUserMCPServers(response: UserMCPToolsResponse) {
    generation++;
    inFlight = null;
    cached = {response, fetchedAt: Date.now()};
}

/** Drop cached status; the next read refetches. Call when a connection changes. */
export function invalidateUserMCPServers() {
    generation++;
    inFlight = null;
    cached = null;
}
