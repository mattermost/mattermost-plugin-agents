// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {buildCheckInCron, parseCheckInCron, type CheckInSchedule} from './check_in_schedule';

describe('parseCheckInCron', () => {
    test.each([
        {cron: '', expected: {frequency: 'off', time: '09:00'}},
        {cron: '0 9 * * *', expected: {frequency: 'daily', time: '09:00'}},
        {cron: '30 14 * * 1-5', expected: {frequency: 'weekdays', time: '14:30'}},
        {cron: '5 7 * * MON-FRI', expected: {frequency: 'weekdays', time: '07:05'}},
        {cron: '0 9 * * 1', expected: {frequency: 'custom', time: '09:00'}},
        {cron: '61 9 * * *', expected: {frequency: 'custom', time: '09:00'}},
        {cron: '0 25 * * *', expected: {frequency: 'custom', time: '09:00'}},
        {cron: '*/5 * * * *', expected: {frequency: 'custom', time: '09:00'}},
    ])('reads "$cron"', ({cron, expected}) => {
        expect(parseCheckInCron(cron)).toEqual(expected);
    });
});

describe('buildCheckInCron', () => {
    test.each([
        {name: 'off clears the schedule', schedule: {frequency: 'off', time: '09:00'}, current: '0 9 * * *', expected: ''},
        {name: 'daily', schedule: {frequency: 'daily', time: '09:30'}, current: '', expected: '30 9 * * *'},
        {name: 'weekdays', schedule: {frequency: 'weekdays', time: '16:05'}, current: '', expected: '5 16 * * 1-5'},
        {name: 'custom keeps the current cron', schedule: {frequency: 'custom', time: '09:00'}, current: '0 9 * * 1', expected: '0 9 * * 1'},
        {name: 'an empty time keeps the current cron', schedule: {frequency: 'daily', time: ''}, current: '0 8 * * *', expected: '0 8 * * *'},
    ] as Array<{name: string; schedule: CheckInSchedule; current: string; expected: string}>)('$name', ({schedule, current, expected}) => {
        expect(buildCheckInCron(schedule, current)).toBe(expected);
    });

    test('a built cron reads back as the same schedule', () => {
        const schedule: CheckInSchedule = {frequency: 'weekdays', time: '08:15'};
        expect(parseCheckInCron(buildCheckInCron(schedule, ''))).toEqual(schedule);
    });
});
