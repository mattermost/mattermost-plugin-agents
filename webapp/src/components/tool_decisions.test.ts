// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {selectDecisionToolCalls} from './tool_decisions';
import {ToolCall, ToolCallStatus} from './tool_types';

function call(overrides: Partial<ToolCall>): ToolCall {
    return {
        id: 'tc',
        name: 'tool',
        description: '',
        arguments: {},
        status: ToolCallStatus.Success,
        ...overrides,
    };
}

describe('selectDecisionToolCalls', () => {
    test('offers a share decision only for results that exist, not for interrupted calls', () => {
        const ran = call({id: 'ran', status: ToolCallStatus.Success});
        const interrupted = call({id: 'lost', status: ToolCallStatus.Error, interrupted: true});
        const failed = call({id: 'failed', status: ToolCallStatus.Error});

        const decisions = selectDecisionToolCalls([ran, interrupted, failed], 'result', true);

        expect(decisions.map((c) => c.id)).toEqual(['ran', 'failed']);
    });
});
