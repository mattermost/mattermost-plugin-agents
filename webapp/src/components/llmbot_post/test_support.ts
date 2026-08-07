// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act} from '@testing-library/react';

import {ToolCall, ToolCallStatus} from '../tool_types';

import type {Round} from './turn_content_utils';

export function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'tc_1',
        name: 'search_tools',
        description: '',
        status: ToolCallStatus.Success,
        ...overrides,
    };
}

export function makeRound(id: string, text: string, toolCalls: ToolCall[] = []): Round {
    return {
        id,
        text,
        toolCalls,
        reasoning: {summary: '', signature: ''},
        annotations: [],
    };
}

/** Runs every pending timer, for tests that installed jest's fake ones. */
export function advanceBy(ms: number) {
    act(() => {
        jest.advanceTimersByTime(ms);
    });
}

/**
 * Settles an animation that retires itself in two steps: the state update
 * that swaps a row only schedules the timer clearing the outgoing one once
 * React has flushed the first.
 */
export function advanceAnimation() {
    advanceBy(1000);
    advanceBy(1000);
}
