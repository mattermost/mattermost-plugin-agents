// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Mirrors model.IsValidId: 26 z-base-32 characters.
const ID_PATTERN = /^[a-z0-9]{26}$/;

/**
 * Returns true if the value is a well-formed Mattermost ID.
 */
export function isValidId(id: string): boolean {
    return ID_PATTERN.test(id);
}
