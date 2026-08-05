// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

const ID_PATTERN = /^[a-z0-9]{26}$/;

export function isValidId(id: unknown): id is string {
    return typeof id === 'string' && ID_PATTERN.test(id);
}
