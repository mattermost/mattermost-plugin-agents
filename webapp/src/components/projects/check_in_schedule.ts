// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type CheckInFrequency = 'off' | 'daily' | 'weekdays' | 'custom';

export type CheckInSchedule = {
    frequency: CheckInFrequency;

    // HH:MM, 24 hours.
    time: string;
};

const DEFAULT_TIME = '09:00';

const pad = (value: number) => String(value).padStart(2, '0');

// The server accepts a five-field cron limited to a daily or weekday time. Anything else the
// agent may have set shows as custom and is kept as it is unless the person picks another schedule.
export function parseCheckInCron(cron: string): CheckInSchedule {
    const trimmed = cron.trim();
    if (!trimmed) {
        return {frequency: 'off', time: DEFAULT_TIME};
    }
    const match = (/^(\d{1,2}) (\d{1,2}) \* \* (\*|1-5|MON-FRI)$/i).exec(trimmed);
    const minute = match ? Number(match[1]) : NaN;
    const hour = match ? Number(match[2]) : NaN;
    if (!match || minute > 59 || hour > 23) {
        return {frequency: 'custom', time: DEFAULT_TIME};
    }
    return {frequency: match[3] === '*' ? 'daily' : 'weekdays', time: `${pad(hour)}:${pad(minute)}`};
}

export function buildCheckInCron(schedule: CheckInSchedule, currentCron: string): string {
    if (schedule.frequency === 'off') {
        return '';
    }
    if (schedule.frequency === 'custom') {
        return currentCron;
    }
    const time = (/^(\d{1,2}):(\d{2})$/).exec(schedule.time);
    if (!time) {
        return currentCron;
    }
    const hour = Number(time[1]);
    const minute = Number(time[2]);
    return `${minute} ${hour} * * ${schedule.frequency === 'weekdays' ? '1-5' : '*'}`;
}

export function browserTimezone(): string {
    try {
        return new Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch {
        return 'UTC';
    }
}
