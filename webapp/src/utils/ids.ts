// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Stricter than model.IsValidId (any 26 unicode letters or digits); accepts everything model.NewId emits.
const ID_PATTERN = /^[a-z0-9]{26}$/;

/** Takes unknown: decoded JSON (post props, API payloads) can put any value in a string-typed field. */
export function isValidId(id: unknown): id is string {
    return typeof id === 'string' && ID_PATTERN.test(id);
}
