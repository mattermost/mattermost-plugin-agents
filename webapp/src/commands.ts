// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {doRunSearch, getChannelInterval} from './client';
import {doSelectPost} from './hooks';

export async function handleAskChannelCommand(
    message: string,
    args: {
        channel_id: string;
        team_id: string;
        root_id: string;
    },
    store: any,
    rhs: { showRHSPlugin: any },
) {
    if (!message.trim()) {
        return {
            error: {
                message: 'Please provide a search query after /ask-channel',
            },
        };
    }

    try {
        const result = await doRunSearch(
            message,
            args.team_id,
            args.channel_id,
        );

        // Get store and dispatch actions to select post and open RHS
        doSelectPost(result.postid, result.channelid, store.dispatch);
        store.dispatch(rhs.showRHSPlugin);

        // Return empty object to prevent default error message
        return {};
    } catch (error) {
        return {
            error: {
                message: 'Failed to process search request',
            },
        };
    }
}

export async function handleSummarizeChannelCommand(
    message: string,
    args: {
        channel_id: string;
        team_id: string;
        root_id: string;
    },
    store: any,
    rhs: { showRHSPlugin: any },
) {
    // Process command options
    const options = parseOptionsFromMessage(message);
    const botUsername = options.bot || '';

    // Default to summarize 24 hours (in milliseconds)
    const defaultTimePeriod = 24 * 60 * 60 * 1000;

    // Calculate time since based on period option
    let timeSince: number;
    if (options.period) {
        timeSince = calculateTimeSince(options.period);
    } else {
        timeSince = Date.now() - defaultTimePeriod;
    }

    try {
        const result = await getChannelInterval(
            args.channel_id,
            timeSince,
            0,
            'summarize_range',
            '',
            botUsername,
        );

        // Get store and dispatch actions to select post and open RHS
        doSelectPost(result.postid, result.channelid, store.dispatch);
        store.dispatch(rhs.showRHSPlugin);

        // Return empty object to prevent default error message
        return {};
    } catch (error) {
        return {
            error: {
                message: 'Failed to summarize channel ' + error,
            },
        };
    }
}

// Parses options from the command message
function parseOptionsFromMessage(message: string): { bot?: string; period?: string } {
    const options: { bot?: string; period?: string } = {};

    // Split the message by spaces and look for options
    const parts = message.trim().split(/\s+/);
    for (let i = 0; i < parts.length; i++) {
        if (parts[i] === '--bot' && i + 1 < parts.length) {
            options.bot = parts[i + 1];
            i++; // Skip the next part as it's the bot name
        } else if (parts[i] === '--period' && i + 1 < parts.length) {
            options.period = parts[i + 1];
            i++; // Skip the next part as it's the period value
        }
    }

    return options;
}

// Calculates timestamp based on period string
function calculateTimeSince(periodStr: string): number {
    const now = Date.now();
    const hourInMs = 60 * 60 * 1000;
    const dayInMs = 24 * hourInMs;

    // Match patterns like "5h" or "3d"
    const hourMatch = (/^(\d+)h$/).exec(periodStr);
    const dayMatch = (/^(\d+)d$/).exec(periodStr);

    if (hourMatch) {
        const hours = parseInt(hourMatch[1], 10);
        return now - (hours * hourInMs);
    } else if (dayMatch) {
        const days = parseInt(dayMatch[1], 10);
        return now - (days * dayInMs);
    } else if (periodStr === '1w') {
        // Support legacy "1w" format
        return now - (7 * dayInMs);
    } else if (periodStr === '2w') {
        // Support legacy "2w" format
        return now - (14 * dayInMs);
    }

    // Default to 24 hours if the period is not recognized
    return now - (24 * hourInMs);
}
