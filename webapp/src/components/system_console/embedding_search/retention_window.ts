// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// 0 means unbounded (index all posts). undefined stored days means the
// index predates this setting (upgrade) — do not nag.
export const retentionWindowWidened = (currentDays: number, storedDays?: number): boolean => {
    if (typeof storedDays !== 'number') {
        return false;
    }
    const current = currentDays <= 0 ? Number.POSITIVE_INFINITY : currentDays;
    const stored = storedDays <= 0 ? Number.POSITIVE_INFINITY : storedDays;
    return current > stored;
};

export const retentionWindowTightened = (currentDays: number, storedDays?: number): boolean => {
    if (typeof storedDays !== 'number') {
        return false;
    }
    const current = currentDays <= 0 ? Number.POSITIVE_INFINITY : currentDays;
    const stored = storedDays <= 0 ? Number.POSITIVE_INFINITY : storedDays;
    return current < stored;
};
