// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ToolApprovalSet from './tool_approval_set';
import {ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

const mockDoToolCall = jest.fn();
const mockInvalidateConversation = jest.fn();

jest.mock('@/client', () => ({
    doToolCall: (postID: string, toolIDs: string[], toolAnswers: Record<string, unknown>) =>
        mockDoToolCall(postID, toolIDs, toolAnswers),
    doToolResult: jest.fn(),
}));

jest.mock('@/hooks/use_conversation', () => ({
    invalidateConversation: (conversationID: string) => mockInvalidateConversation(conversationID),
}));

type MockRenderContext = {
    tool: ToolCall;
    onApprove?: () => void;
    onReject?: () => void;
    onSkip?: () => void;
    isAutoApproved?: boolean;
    awaitingRequester?: boolean;
    isCollapsed?: boolean;
};

// Mock the registry so these tests cover ToolApprovalSet's decision logic
// only; routing is covered by registry.test.tsx.
const mockRenderToolCall = jest.fn<null, [MockRenderContext]>(() => null);

jest.mock('./tool_renderers/registry', () => ({
    __esModule: true,
    renderToolCall: (ctx: MockRenderContext) => mockRenderToolCall(ctx),
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

function renderComponent(toolCalls: ToolCall[], approvalStage: ToolApprovalStage = 'call', canApprove = true) {
    return render(
        <IntlProvider locale='en'>
            <ToolApprovalSet
                postID='post_1'
                conversationID='conv_1'
                toolCalls={toolCalls}
                approvalStage={approvalStage}
                canApprove={canApprove}
                canExpand={true}
            />
        </IntlProvider>,
    );
}

function getToolCardProps(toolID: string): MockRenderContext {
    const match = mockRenderToolCall.mock.calls.find(([ctx]) => ctx.tool.id === toolID);
    expect(match).toBeDefined();
    return match![0] as MockRenderContext;
}

beforeEach(() => {
    mockRenderToolCall.mockClear();
    mockDoToolCall.mockReset();
    mockDoToolCall.mockImplementation(() => Promise.resolve());
    mockInvalidateConversation.mockClear();
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

    test.each([
        {name: 'a pending call', stage: 'call' as const, status: ToolCallStatus.Pending},
        {name: 'a result awaiting the share decision', stage: 'result' as const, status: ToolCallStatus.Success},
    ])('flags $name as awaiting the requester for an onlooker', ({stage, status}) => {
        renderComponent([makeTool({id: 'tool_waiting', status})], stage, false);

        expect(getToolCardProps('tool_waiting').awaitingRequester).toBe(true);
        expect(getToolCardProps('tool_waiting').onApprove).toBeUndefined();
    });

    test('does not flag an auto-approved call that already ran while another awaits approval', () => {
        renderComponent([
            makeTool({id: 'tool_auto', status: ToolCallStatus.AutoApproved}),
            makeTool({id: 'tool_pending', status: ToolCallStatus.Pending}),
        ], 'call', false);

        expect(getToolCardProps('tool_auto').awaitingRequester).toBe(false);
        expect(getToolCardProps('tool_pending').awaitingRequester).toBe(true);
    });

    test('flags a live redacted call, which carries an empty result, for an onlooker', () => {
        renderComponent([makeTool({id: 'tool_live', status: ToolCallStatus.Pending, result: ''})], 'call', false);

        expect(getToolCardProps('tool_live').awaitingRequester).toBe(true);
        expect(getToolCardProps('tool_live').isCollapsed).toBe(true);
    });

    test('expands an argument-free result for the requester before ownership loads', () => {
        renderComponent([makeTool({id: 'tool_done', status: ToolCallStatus.Success, result: 'channel info'})], 'result', false);

        expect(getToolCardProps('tool_done').isCollapsed).toBe(false);
        expect(getToolCardProps('tool_done').awaitingRequester).toBe(false);
    });

    test('flags a private result awaiting the share decision next to a shared auto-run result', () => {
        renderComponent([
            makeTool({id: 'tool_auto', status: ToolCallStatus.AutoApproved, result: 'shared output', decided: true}),
            makeTool({id: 'tool_manual', status: ToolCallStatus.Success, result: ''}),
        ], 'result', false);

        expect(getToolCardProps('tool_manual').awaitingRequester).toBe(true);
        expect(getToolCardProps('tool_manual').isCollapsed).toBe(true);
        expect(getToolCardProps('tool_auto').awaitingRequester).toBe(false);
    });

    test('flags an interrupted auto-run round as awaiting the requester for an onlooker', () => {
        renderComponent([makeTool({id: 'tool_auto_pending', status: ToolCallStatus.Pending, would_auto_execute: true})], 'call', false);

        expect(getToolCardProps('tool_auto_pending').awaitingRequester).toBe(true);
    });

    test('keeps an onlooker\'s pending cards collapsed by default', () => {
        renderComponent([makeTool({id: 'tool_pending', status: ToolCallStatus.Pending})], 'call', false);

        expect(getToolCardProps('tool_pending').isCollapsed).toBe(true);
    });

    test('expands and does not flag a pending call for the requester before ownership loads', () => {
        renderComponent([makeTool({id: 'tool_pending', status: ToolCallStatus.Pending, arguments: {channel: 'town-square'}})], 'call', false);

        expect(getToolCardProps('tool_pending').isCollapsed).toBe(false);
        expect(getToolCardProps('tool_pending').awaitingRequester).toBe(false);
    });

    test('does not flag calls as awaiting the requester for the requester', () => {
        renderComponent([makeTool({id: 'tool_pending', status: ToolCallStatus.Pending})], 'call', true);

        expect(getToolCardProps('tool_pending').awaitingRequester).toBe(false);
    });

    test('hides pending tools that passed the auto-execution policy', () => {
        renderComponent([
            makeTool({id: 'tool_marked', would_auto_execute: true}),
            makeTool({id: 'tool_manual'}),
        ]);

        expect(mockRenderToolCall.mock.calls.find(([ctx]) => ctx.tool.id === 'tool_marked')).toBeUndefined();

        const manualTool = getToolCardProps('tool_manual');
        expect(manualTool.onApprove).toEqual(expect.any(Function));
        expect(manualTool.onReject).toEqual(expect.any(Function));
    });

    test('renders live pending auto-executing tools without decision controls', () => {
        renderComponent([
            makeTool({id: 'tool_auto', would_auto_execute: true}),
        ], 'done');

        const autoTool = getToolCardProps('tool_auto');
        expect(autoTool.onApprove).toBeUndefined();
        expect(autoTool.onReject).toBeUndefined();
    });

    test('resumes an interrupted all-auto round with an empty accepted list', async () => {
        const {getByRole} = renderComponent([
            makeTool({id: 'tool_auto_a', would_auto_execute: true}),
            makeTool({id: 'tool_auto_b', would_auto_execute: true}),
        ]);

        expect(getToolCardProps('tool_auto_a').onApprove).toBeUndefined();
        expect(getToolCardProps('tool_auto_b').onReject).toBeUndefined();

        fireEvent.click(getByRole('button', {name: 'Run tools'}));

        await waitFor(() => {
            expect(mockDoToolCall).toHaveBeenCalledWith('post_1', [], {});
        });
        expect(mockInvalidateConversation).toHaveBeenCalledWith('conv_1');
    });

    test('does not offer resume to non-owners', () => {
        const {queryByRole} = renderComponent([
            makeTool({id: 'tool_auto', would_auto_execute: true}),
        ], 'call', false);

        expect(getToolCardProps('tool_auto').onApprove).toBeUndefined();
        expect(queryByRole('button', {name: 'Run tools'})).toBeNull();
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

    test('accepting a connect call submits it with no answer payload', async () => {
        renderComponent([
            makeTool({id: 'connect', name: 'ConnectMCPServer', user_interaction: 'connect', arguments: {server: 'Jira'}}),
        ]);

        getToolCardProps('connect').onApprove!();

        await waitFor(() => {
            expect(mockDoToolCall).toHaveBeenCalledWith('post_1', ['connect'], {});
        });
    });

    test('skipping a connect call submits it as rejected', async () => {
        renderComponent([
            makeTool({id: 'connect', name: 'ConnectMCPServer', user_interaction: 'connect', arguments: {server: 'Jira'}}),
        ]);

        getToolCardProps('connect').onSkip!();

        await waitFor(() => {
            expect(mockDoToolCall).toHaveBeenCalledWith('post_1', [], {});
        });
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
