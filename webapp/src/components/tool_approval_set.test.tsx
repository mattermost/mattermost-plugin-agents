// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ToolApprovalSet from './tool_approval_set';
import {ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

type MockToolCardProps = {
    tool: ToolCall;
    onApprove?: () => void;
    onReject?: () => void;
    isAutoApproved?: boolean;
};

const mockToolCard = jest.fn<null, [MockToolCardProps]>(() => null);

jest.mock('./tool_card', () => ({
    __esModule: true,
    default: (props: MockToolCardProps) => {
        return mockToolCard(props);
    },
}));

function makeTool(overrides: Partial<ToolCall>): ToolCall {
    return {
        id: 'tool_1',
        name: 'test_tool',
        description: '',
        status: ToolCallStatus.Pending,
        ...overrides,
    };
}

function renderComponent(toolCalls: ToolCall[], approvalStage: ToolApprovalStage = 'call') {
    return render(
        <IntlProvider locale='en'>
            <ToolApprovalSet
                postID='post_1'
                conversationID='conv_1'
                toolCalls={toolCalls}
                approvalStage={approvalStage}
                canApprove={true}
                canExpand={true}
                showArguments={true}
                showResults={true}
            />
        </IntlProvider>,
    );
}

function getToolCardProps(toolID: string): MockToolCardProps {
    const match = mockToolCard.mock.calls.find(([props]) => props.tool.id === toolID);
    expect(match).toBeDefined();
    return match![0] as MockToolCardProps;
}

beforeEach(() => {
    mockToolCard.mockClear();
});

describe('ToolApprovalSet', () => {
    test('keeps call-stage decisions available for pending tools in mixed auto-approved responses', () => {
        renderComponent([
            makeTool({id: 'tool_auto', status: ToolCallStatus.AutoApproved}),
            makeTool({id: 'tool_pending', status: ToolCallStatus.Pending}),
        ]);

        const pendingTool = getToolCardProps('tool_pending');
        expect(pendingTool.onApprove).toEqual(expect.any(Function));
        expect(pendingTool.onReject).toEqual(expect.any(Function));

        const autoApprovedTool = getToolCardProps('tool_auto');
        expect(autoApprovedTool.onApprove).toBeUndefined();
        expect(autoApprovedTool.onReject).toBeUndefined();
    });

    test('marks only auto-approved tools with the auto-approved badge prop', () => {
        renderComponent([
            makeTool({id: 'tool_auto', status: ToolCallStatus.AutoApproved}),
            makeTool({id: 'tool_pending', status: ToolCallStatus.Pending}),
        ]);

        expect(getToolCardProps('tool_auto').isAutoApproved).toBe(true);
        expect(getToolCardProps('tool_pending').isAutoApproved).toBe(false);
    });

    test('hides pending tools that passed the auto-execution policy', () => {
        renderComponent([
            makeTool({id: 'tool_marked', would_auto_execute: true}),
            makeTool({id: 'tool_manual'}),
        ]);

        expect(mockToolCard.mock.calls.find(([props]) => props.tool.id === 'tool_marked')).toBeUndefined();

        const manualTool = getToolCardProps('tool_manual');
        expect(manualTool.onApprove).toEqual(expect.any(Function));
        expect(manualTool.onReject).toEqual(expect.any(Function));
    });

    test('excludes already-decided results from share decisions', () => {
        renderComponent([
            makeTool({id: 'tool_decided', status: ToolCallStatus.Success, decided: true}),
            makeTool({id: 'tool_undecided', status: ToolCallStatus.Success}),
        ], 'result');

        const decidedTool = getToolCardProps('tool_decided');
        expect(decidedTool.onApprove).toBeUndefined();
        expect(decidedTool.onReject).toBeUndefined();

        const undecidedTool = getToolCardProps('tool_undecided');
        expect(undecidedTool.onApprove).toEqual(expect.any(Function));
        expect(undecidedTool.onReject).toEqual(expect.any(Function));
    });

    test('status bar counts only approval-type decisions, not questions', () => {
        // The question has no arguments (redacted shape), so it falls back to
        // the mocked tool card; only the count behavior is under test.
        const {getByText} = renderComponent([
            makeTool({id: 'question', user_interaction: 'select'}),
            makeTool({id: 'tool_a'}),
            makeTool({id: 'tool_b'}),
        ]);

        getByText('2 tools need decisions');
    });
});
