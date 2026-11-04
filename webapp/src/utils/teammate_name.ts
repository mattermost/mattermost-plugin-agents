// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';
import {UserProfile} from '@mattermost/types/users';

const SHOW_NICKNAME_FULLNAME = 'nickname_full_name';
const SHOW_FULLNAME = 'full_name';

// Mirrors the host's getTeammateNameDisplaySetting, which plugins cannot import:
// the viewer's preference wins unless the admin locked the setting under a license.
export function selectTeammateNameDisplay(state: GlobalState): string {
    const {config, license} = state.entities.general;
    const locked = license?.LockTeammateNameDisplay === 'true' && config?.LockTeammateNameDisplay === 'true';
    const preference = state.entities.preferences?.myPreferences?.['display_settings--name_format']?.value;
    if (preference && !locked) {
        return preference;
    }
    return config?.TeammateNameDisplay || 'username';
}

// Mirrors the host's displayUsername, falling back to the username when the
// chosen name is empty.
export function displayNameFor(user: Pick<UserProfile, 'username' | 'nickname' | 'first_name' | 'last_name'>, teammateNameDisplay: string): string {
    const fullName = [user.first_name, user.last_name].filter(Boolean).join(' ');
    let name = user.username;
    if (teammateNameDisplay === SHOW_NICKNAME_FULLNAME) {
        name = user.nickname || fullName;
    } else if (teammateNameDisplay === SHOW_FULLNAME) {
        name = fullName;
    }
    return name.trim() ? name : user.username;
}
