// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createContext, useContext} from 'react';

// True while the plugin settings are shown to a user with read-only access.
// Settings controls disable themselves and actions that change something are
// hidden or disabled; controls that only reveal more (tabs, expanders) still work.
// The server enforces the same access, this only keeps the page honest.
export const SettingsReadOnlyContext = createContext(false);

export const useSettingsReadOnly = () => useContext(SettingsReadOnlyContext);
