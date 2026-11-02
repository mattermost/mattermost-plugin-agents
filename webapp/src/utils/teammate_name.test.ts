// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';

import {displayNameFor, selectTeammateNameDisplay} from './teammate_name';

const alice = {username: 'alice', nickname: 'Ali', first_name: 'Alice', last_name: 'Smith'};

describe('displayNameFor', () => {
    test.each([
        {setting: 'username', user: alice, expected: 'alice'},
        {setting: 'full_name', user: alice, expected: 'Alice Smith'},
        {setting: 'nickname_full_name', user: alice, expected: 'Ali'},
        {setting: 'nickname_full_name', user: {...alice, nickname: ''}, expected: 'Alice Smith'},
        {setting: 'full_name', user: {...alice, first_name: '', last_name: ''}, expected: 'alice'},
        {setting: 'nickname_full_name', user: {...alice, nickname: '', first_name: '', last_name: ''}, expected: 'alice'},
    ])('shows $expected for $setting', ({setting, user, expected}) => {
        expect(displayNameFor(user, setting)).toBe(expected);
    });
});

describe('selectTeammateNameDisplay', () => {
    function state({configSetting = 'full_name', preference, configLock = 'false', licenseLock = 'false'}: {
        configSetting?: string;
        preference?: string;
        configLock?: string;
        licenseLock?: string;
    }) {
        return {
            entities: {
                general: {
                    config: {TeammateNameDisplay: configSetting, LockTeammateNameDisplay: configLock},
                    license: {LockTeammateNameDisplay: licenseLock},
                },
                preferences: {
                    myPreferences: preference ? {'display_settings--name_format': {category: 'display_settings', name: 'name_format', user_id: 'me', value: preference}} : {},
                },
            },
        } as unknown as GlobalState;
    }

    test.each([
        {name: 'uses the server default without a preference', input: {}, expected: 'full_name'},
        {name: 'uses the viewer preference', input: {preference: 'nickname_full_name'}, expected: 'nickname_full_name'},
        {name: 'ignores the preference when an admin locks it', input: {preference: 'nickname_full_name', configLock: 'true', licenseLock: 'true'}, expected: 'full_name'},
        {name: 'keeps the preference when the license does not allow locking', input: {preference: 'nickname_full_name', configLock: 'true'}, expected: 'nickname_full_name'},
        {name: 'falls back to username without any setting', input: {configSetting: ''}, expected: 'username'},
    ])('$name', ({input, expected}) => {
        expect(selectTeammateNameDisplay(state(input))).toBe(expected);
    });
});
