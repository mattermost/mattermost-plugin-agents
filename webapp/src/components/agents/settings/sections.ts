// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Settings view's sections, in tab order. Each is a URL segment:
// /plug/<plugin>/agents/settings/<section>.
export const SETTINGS_SECTIONS = ['general', 'services', 'mcp', 'search', 'web-search', 'debug'] as const;

export type SettingsSection = typeof SETTINGS_SECTIONS[number];

export const DEFAULT_SETTINGS_SECTION: SettingsSection = 'general';

export const isSettingsSection = (value: string | undefined): value is SettingsSection => (
    (SETTINGS_SECTIONS as readonly string[]).includes(value ?? '')
);
