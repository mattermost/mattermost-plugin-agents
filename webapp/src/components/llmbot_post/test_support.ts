// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act} from '@testing-library/react';

import type {ServerToolUse} from '@/types/conversation';

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

export function makeServerTool(overrides: Partial<ServerToolUse> = {}): ServerToolUse {
    return {
        id: 'srv_1',
        tool: 'web_search',
        status: 'success',
        ...overrides,
    };
}

export function makeRound(id: string, text: string, toolCalls: ToolCall[] = [], serverTools: ServerToolUse[] = []): Round {
    return {
        id,
        text,
        toolCalls,
        reasoning: {summary: '', signature: ''},
        annotations: [],
        serverTools,
    };
}

export function withReasoning(round: Round, summary = 'Thinking it over'): Round {
    return {...round, reasoning: {summary, signature: ''}};
}

/** Lets every pending animation timer fire, for tests using jest's fake timers. */
export function advanceAnimation() {
    act(() => {
        jest.advanceTimersByTime(1000);
    });
}
