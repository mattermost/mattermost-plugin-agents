// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createContext, useContext} from 'react';

// True while the Agents settings page is saving. Settings controls disable
// themselves and actions that change something are disabled; controls that only
// reveal more (tabs, expanders) still work. A save's response replaces the
// page's configuration, so an edit made meanwhile would be lost.
export const SettingsReadOnlyContext = createContext(false);

export const useSettingsReadOnly = () => useContext(SettingsReadOnlyContext);
