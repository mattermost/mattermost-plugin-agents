// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import type {ConversationResponse} from '@/types/conversation';

import ToolApprovalSet from './tool_approval_set';
import {AskAnotherUserToolName, ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

const mockDoToolCall = jest.fn();
const mockDoAskUserCancel = jest.fn();
const mockGetProfilesByIds = jest.fn();
const mockInvalidateConversation = jest.fn();
const useConversationMock = jest.fn();
const mockDispatch = jest.fn();

jest.mock('@/client', () => ({
    doToolCall: (postID: string, toolIDs: string[], toolAnswers: Record<string, unknown>) =>
        mockDoToolCall(postID, toolIDs, toolAnswers),
    doToolResult: jest.fn(),
    doAskUserCancel: (postID: string, botUsername: string, body: {tool_use_id: string}) =>
        mockDoAskUserCancel(postID, botUsername, body),
    getProfilesByIds: (ids: string[]) => mockGetProfilesByIds(ids),
}));

jest.mock('@/hooks/use_conversation', () => ({
    invalidateConversation: (conversationID: string) => mockInvalidateConversation(conversationID),
    useConversation: (id: string) => useConversationMock(id),
}));

// The component resolves the anchor post's author (the bot) from redux to
// send botUsername on cancel requests. Profiles are reset per test so the
// uncached-bot case can be simulated.
const mockReduxState = {
    entities: {
        posts: {posts: {post_1: {id: 'post_1', user_id: 'bot_user_1'}}},
        users: {profiles: {} as Record<string, {id: string; username: string}>},
    },
};

jest.mock('react-redux', () => ({
    useSelector: (selector: (state: unknown) => unknown) => selector(mockReduxState),
    useDispatch: () => mockDispatch,
}));

type MockRenderContext = {
    tool: ToolCall;
    onApprove?: () => void;
    onReject?: () => void;
    isAutoApproved?: boolean;
    renderDelegatedApprovals?: (delegationID: string) => React.ReactNode;
    onCancelAsk?: () => void;
    askCancelState?: 'idle' | 'submitting' | 'error';
    askCancelDisabled?: boolean;
    postID?: string;
    requesterUserID?: string;
    appsEligible?: boolean;
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
    mockDoAskUserCancel.mockReset();
    mockDoAskUserCancel.mockResolvedValue({status: 'canceled'});
    mockGetProfilesByIds.mockReset();
    mockGetProfilesByIds.mockResolvedValue([{id: 'bot_user_1', username: 'agentbot'}]);
    mockInvalidateConversation.mockClear();
    useConversationMock.mockReset();
    useConversationMock.mockReturnValue({conversation: null, loading: false, error: null});
    mockDispatch.mockClear();
    mockReduxState.entities.users.profiles = {bot_user_1: {id: 'bot_user_1', username: 'agentbot'}};
});

// Latest render's props for a tool, so state transitions are observable.
function getLatestToolCardProps(toolID: string): MockRenderContext {
    const matches = mockRenderToolCall.mock.calls.filter(([props]) => props.tool.id === toolID);
    expect(matches.length).toBeGreaterThan(0);
    return matches[matches.length - 1][0] as MockRenderContext;
}

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

    test('forwards MCP Apps wiring to the renderer', () => {
        render(
            <IntlProvider locale='en'>
                <ToolApprovalSet
                    postID='post_1'
                    conversationID='conv_1'
                    toolCalls={[makeTool({id: 'tool_app', status: ToolCallStatus.Success})]}
                    approvalStage='done'
                    canApprove={false}
                    canExpand={false}
                    requesterUserID='requester_1'
                    appsEligible={true}
                />
            </IntlProvider>,
        );

        const props = getToolCardProps('tool_app');
        expect(props.postID).toBe('post_1');
        expect(props.requesterUserID).toBe('requester_1');
        expect(props.appsEligible).toBe(true);
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

        renderComponent([makeTool({
            id: 'parent_tool',
            name: 'mattermost__ask_agent',
            server_origin: 'embedded://mattermost',
            arguments: {agent: 'subagent', task: 'Inspect the channel'},
            status: ToolCallStatus.Accepted,
        })]);

        // The registry hands this hook to the delegation card, which renders
        // it once the sub-agent is waiting on the user.
        const {renderDelegatedApprovals} = getToolCardProps('parent_tool');
        expect(renderDelegatedApprovals).toBeDefined();

        const {getByTestId} = render(
            <IntlProvider locale='en'>
                {renderDelegatedApprovals!('delegation_conv')}
            </IntlProvider>,
        );

        await waitFor(() => {
            getByTestId('delegation-embedded-approvals');
        });
        expect(useConversationMock).toHaveBeenCalledWith('delegation_conv');

        const nestedTool = getToolCardProps('nested_tool');
        await act(async () => {
            nestedTool.onApprove?.();
        });

        await waitFor(() => {
            expect(mockDoToolCall).toHaveBeenCalledWith('delegated_response_post', ['nested_tool'], {});
        });
    });

    test('omits delegated approvals for viewers who cannot approve', () => {
        renderComponent([makeTool({
            id: 'parent_tool',
            name: 'mattermost__ask_agent',
            server_origin: 'embedded://mattermost',
            status: ToolCallStatus.Accepted,
        })], 'call', false);

        expect(getToolCardProps('parent_tool').renderDelegatedApprovals).toBeUndefined();
    });
});

describe('ToolApprovalSet AskAnotherUser cancel wiring', () => {
    const waitingAskTool = (id = 'tool_ask') => makeTool({
        id,
        name: AskAnotherUserToolName,
        status: ToolCallStatus.Waiting,
        deferred_result: true,
    });

    test('offers the cancel handler to the requester on a waiting AskAnotherUser call', () => {
        renderComponent([waitingAskTool()], 'done');

        const props = getLatestToolCardProps('tool_ask');
        expect(props.onCancelAsk).toEqual(expect.any(Function));
        expect(props.askCancelState).toBe('idle');
        expect(props.askCancelDisabled).toBe(false);
        expect(mockGetProfilesByIds).not.toHaveBeenCalled();
    });

    test('offers no cancel handler to observers', () => {
        renderComponent([waitingAskTool()], 'done', false);

        expect(getLatestToolCardProps('tool_ask').onCancelAsk).toBeUndefined();
    });

    test('offers no cancel handler for a waiting non-AskAnotherUser tool', () => {
        renderComponent([makeTool({id: 'tool_other', status: ToolCallStatus.Waiting})], 'done');

        expect(getLatestToolCardProps('tool_other').onCancelAsk).toBeUndefined();
    });

    test('offers no cancel handler for a pending (not yet waiting) AskAnotherUser call', () => {
        renderComponent([makeTool({id: 'tool_ask_pending', name: AskAnotherUserToolName, status: ToolCallStatus.Pending})]);

        const props = getLatestToolCardProps('tool_ask_pending');
        expect(props.onCancelAsk).toBeUndefined();
        expect(props.askCancelState).toBeUndefined();
    });

    test('disables the cancel control and hydrates the bot profile when it is not cached', async () => {
        mockReduxState.entities.users.profiles = {};
        renderComponent([waitingAskTool()], 'done');

        expect(getLatestToolCardProps('tool_ask').askCancelDisabled).toBe(true);

        await waitFor(() => {
            expect(mockGetProfilesByIds).toHaveBeenCalledWith(['bot_user_1']);
        });
        await waitFor(() => {
            expect(mockDispatch).toHaveBeenCalledWith({
                type: 'RECEIVED_PROFILES',
                data: {bot_user_1: {id: 'bot_user_1', username: 'agentbot'}},
            });
        });
    });

    test('cancel calls doAskUserCancel with the post, bot, and tool_use id, then refreshes', async () => {
        renderComponent([waitingAskTool()], 'done');

        await act(async () => {
            getLatestToolCardProps('tool_ask').onCancelAsk!();
        });

        expect(mockDoAskUserCancel).toHaveBeenCalledTimes(1);
        expect(mockDoAskUserCancel).toHaveBeenCalledWith('post_1', 'agentbot', {tool_use_id: 'tool_ask'});
        expect(mockInvalidateConversation).toHaveBeenCalledWith('conv_1');

        // Stays 'submitting' until the refetched conversation replaces the
        // waiting card — the control cannot be clicked twice.
        expect(getLatestToolCardProps('tool_ask').askCancelState).toBe('submitting');
    });

    test('a 409 (already resolved) refreshes silently without an error state', async () => {
        mockDoAskUserCancel.mockRejectedValue({status_code: 409});
        renderComponent([waitingAskTool()], 'done');

        await act(async () => {
            getLatestToolCardProps('tool_ask').onCancelAsk!();
        });

        await waitFor(() => {
            expect(mockInvalidateConversation).toHaveBeenCalledWith('conv_1');
        });
        expect(getLatestToolCardProps('tool_ask').askCancelState).not.toBe('error');
    });

    test('a non-409 failure flips the cancel state to error', async () => {
        mockDoAskUserCancel.mockRejectedValue({status_code: 500});
        renderComponent([waitingAskTool()], 'done');

        await act(async () => {
            getLatestToolCardProps('tool_ask').onCancelAsk!();
        });

        await waitFor(() => {
            expect(getLatestToolCardProps('tool_ask').askCancelState).toBe('error');
        });
        expect(mockInvalidateConversation).not.toHaveBeenCalled();
    });
});
