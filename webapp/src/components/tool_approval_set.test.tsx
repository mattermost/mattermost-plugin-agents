// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import type {ConversationResponse} from '@/types/conversation';
import type {DelegationStatus} from '@/types/delegation';

import ToolApprovalSet from './tool_approval_set';
import {ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

type MockToolCardProps = {
    tool: ToolCall;
    onApprove?: () => void;
    onReject?: () => void;
    isAutoApproved?: boolean;
};

const mockToolCard = jest.fn<null, [MockToolCardProps]>(() => null);
const getDelegationStatusMock = jest.fn<Promise<DelegationStatus>, [string]>();
const doToolCallMock = jest.fn<Promise<void>, [string, string[], Record<string, unknown>?]>();
const useConversationMock = jest.fn();

jest.mock('@/client', () => ({
    doToolCall: (...args: [string, string[], Record<string, unknown>?]) => doToolCallMock(...args),
    doToolResult: jest.fn(),
    getDelegationStatus: (id: string) => getDelegationStatusMock(id),
}));

jest.mock('@/hooks/use_conversation', () => ({
    invalidateConversation: jest.fn(),
    useConversation: (id: string) => useConversationMock(id),
}));

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
    getDelegationStatusMock.mockReset();
    getDelegationStatusMock.mockRejectedValue(new Error('not found'));
    doToolCallMock.mockReset();
    doToolCallMock.mockImplementation(async () => {});
    useConversationMock.mockReset();
    useConversationMock.mockReturnValue({conversation: null, loading: false, error: null});
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

    test('submits delegated approvals from the parent delegation card', async () => {
        const delegatedConversation: ConversationResponse = {
            id: 'delegation_conv',
            user_id: 'user_1',
            bot_id: 'subagent_bot',
            channel_id: 'subagent_dm',
            root_post_id: 'task_post',
            title: '',
            operation: 'delegation',
            turns: [{
                id: 'approval_turn',
                post_id: 'delegated_response_post',
                role: 'assistant',
                content: [{
                    type: 'tool_use',
                    id: 'nested_tool',
                    name: 'mattermost__get_channel_info',
                    input: {channel_id: 'town-square'},
                    status: 'pending',
                }],
                tokens_in: 0,
                tokens_out: 0,
                sequence: 1,
                approval_state: 'call',
            }],
        };
        useConversationMock.mockReturnValue({conversation: delegatedConversation, loading: false, error: null});
        getDelegationStatusMock.mockResolvedValue({
            delegation_id: 'delegation_conv',
            parent_tool_call_id: 'parent_tool',
            phase: 'waiting_on_you',
            task_post_id: 'task_post',
            permalink: '/_redirect/pl/task_post',
            target_agent_id: 'subagent_bot',
            target_agent_username: 'subagent',
            target_agent_displayname: 'Sub Agent',
            created_at: Date.now(),
        });

        const {getByTestId} = renderComponent([makeTool({
            id: 'parent_tool',
            name: 'mattermost__ask_agent',
            server_origin: 'embedded://mattermost',
            arguments: {agent: 'subagent', task: 'Inspect the channel'},
            status: ToolCallStatus.Accepted,
        })]);

        await waitFor(() => {
            getByTestId('delegation-embedded-approvals');
        });
        expect(useConversationMock).toHaveBeenCalledWith('delegation_conv');

        const nestedTool = getToolCardProps('nested_tool');
        await act(async () => {
            nestedTool.onApprove?.();
        });

        await waitFor(() => {
            expect(doToolCallMock).toHaveBeenCalledWith('delegated_response_post', ['nested_tool'], {});
        });
    });
});
