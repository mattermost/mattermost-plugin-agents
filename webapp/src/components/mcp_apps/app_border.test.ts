// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {prefersAppBorder} from './app_border';

describe('prefersAppBorder', () => {
    test('defaults to bordered when omitted', () => {
        expect(prefersAppBorder(undefined)).toBe(true); // eslint-disable-line no-undefined
    });

    test('respects explicit false', () => {
        expect(prefersAppBorder(false)).toBe(false);
    });

    test('respects explicit true', () => {
        expect(prefersAppBorder(true)).toBe(true);
    });
});
