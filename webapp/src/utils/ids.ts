// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Mirrors model.IsValidId: 26 z-base-32 characters.
const ID_PATTERN = /^[a-z0-9]{26}$/;

/**
 * Returns true if the value is a well-formed Mattermost ID. Takes unknown
 * because decoded JSON (post props, API payloads) carries no runtime guarantee
 * that a field typed as a string actually holds one.
 */
export function isValidId(id: unknown): id is string {
    return typeof id === 'string' && ID_PATTERN.test(id);
}
