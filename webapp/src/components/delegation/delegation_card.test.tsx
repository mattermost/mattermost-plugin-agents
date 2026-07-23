// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {notifyDelegationUpdate} from '@/hooks/use_delegation_updates';
import type {DelegationStatus} from '@/types/delegation';

import {ToolCall, ToolCallStatus} from '../tool_types';

import DelegationCard, {isAskAgentToolCall, parseAskAgentArgs} from './delegation_card';

const getDelegationStatusMock = jest.fn<Promise<DelegationStatus>, [string]>();

jest.mock('@/client', () => ({
    getDelegationStatus: (id: string) => getDelegationStatusMock(id),
}));

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'toolcall_1',
        name: 'mattermost__ask_agent',
        description: '',
        server_origin: 'embedded://mattermost',
        arguments: {agent: 'projects', task: 'What shipped last sprint?'},
        status: ToolCallStatus.Pending,
        ...overrides,
    };
}

function renderCard(tool: ToolCall, extra: Partial<React.ComponentProps<typeof DelegationCard>> = {}) {
    return render(
        <IntlProvider locale='en'>
            <DelegationCard
                tool={tool}
                approvalStage='call'
                isProcessing={false}
                canApprove={true}
                onApprove={jest.fn()}
                onReject={jest.fn()}
                {...extra}
            />
        </IntlProvider>,
    );
}

beforeEach(() => {
    getDelegationStatusMock.mockReset();
    getDelegationStatusMock.mockRejectedValue(new Error('not found'));
});

describe('isAskAgentToolCall', () => {
    const cases: Array<{name: string; tool: ToolCall; want: boolean}> = [
        {name: 'embedded ask_agent', tool: makeTool(), want: true},
        {name: 'redacted payload without origin', tool: makeTool({server_origin: undefined}), want: true}, // eslint-disable-line no-undefined
        {name: 'other embedded tool', tool: makeTool({name: 'mattermost__search_posts'}), want: false},
        {name: 'remote tool named ask_agent', tool: makeTool({server_origin: 'https://other.example.com'}), want: false},
    ];

    for (const tc of cases) {
        it(tc.name, () => {
            expect(isAskAgentToolCall(tc.tool)).toBe(tc.want);
        });
    }
});

describe('parseAskAgentArgs', () => {
    it('parses agent and task', () => {
        expect(parseAskAgentArgs({agent: 'projects', task: 'do it'})).toEqual({agent: 'projects', task: 'do it'});
    });

    it('returns null for redacted arguments', () => {
        expect(parseAskAgentArgs(null)).toBeNull();
    });
});

describe('DelegationCard', () => {
    it('renders pending approval with accept/reject', () => {
        renderCard(makeTool());
        expect(screen.getByText('Waiting for your approval to delegate this task')).not.toBeNull();
        expect(screen.getByText('Accept')).not.toBeNull();
        expect(screen.getByText('Reject')).not.toBeNull();
        expect(screen.getByText('Delegating to @projects')).not.toBeNull();
        expect(screen.getByText('What shipped last sprint?')).not.toBeNull();
    });

    it('reconciles the in-flight phase from the status endpoint', async () => {
        getDelegationStatusMock.mockResolvedValue({
            delegation_id: 'conv1',
            parent_tool_call_id: 'toolcall_1',
            phase: 'waiting_on_you',
            task_post_id: 'post1',
            permalink: 'http://localhost:8065/_redirect/pl/post1',
            target_agent_id: 'bot1',
            target_agent_username: 'projects',
            target_agent_displayname: 'Projects Agent',
            created_at: Date.now(),
        });

        renderCard(makeTool({status: ToolCallStatus.Accepted}));

        await waitFor(() => {
            expect(screen.getByText('Waiting on you')).not.toBeNull();
        });
        expect(screen.getByText('View conversation')).not.toBeNull();
        expect(screen.queryByText('Accept')).toBeNull();
    });

    it('renders delegated approval content inline while waiting on the user', async () => {
        getDelegationStatusMock.mockResolvedValue({
            delegation_id: 'conv1',
            parent_tool_call_id: 'toolcall_1',
            phase: 'waiting_on_you',
            task_post_id: 'post1',
            permalink: 'http://localhost:8065/_redirect/pl/post1',
            target_agent_id: 'bot1',
            target_agent_username: 'projects',
            target_agent_displayname: 'Projects Agent',
            created_at: Date.now(),
        });

        renderCard(makeTool({status: ToolCallStatus.Accepted}), {
            renderPendingApprovals: (delegationID) => (
                <div data-testid='inline-approval'>{delegationID}</div>
            ),
        });

        await waitFor(() => {
            expect(screen.getByTestId('inline-approval').textContent).toBe('conv1');
        });
        expect(screen.getByText('— respond below to continue.')).not.toBeNull();
    });

    it('updates phases from live delegation events', async () => {
        renderCard(makeTool({status: ToolCallStatus.Accepted}));

        notifyDelegationUpdate({
            delegation_id: 'conv1',
            parent_tool_call_id: 'toolcall_1',
            phase: 'running',
            activity: 'using_tools',
            tools: 'search_posts',
            permalink: 'http://localhost:8065/_redirect/pl/post1',
            target_agent_username: 'projects',
            target_agent_displayname: 'Projects Agent',
        });

        await waitFor(() => {
            expect(screen.getByText('Using search_posts…')).not.toBeNull();
        });
        expect(screen.getByText('Delegating to Projects Agent')).not.toBeNull();
    });

    it('ignores events for other delegations', async () => {
        renderCard(makeTool({status: ToolCallStatus.Accepted}));

        notifyDelegationUpdate({
            delegation_id: 'conv2',
            parent_tool_call_id: 'other_toolcall',
            phase: 'completed',
        });

        await waitFor(() => {
            expect(screen.queryByText('Completed')).toBeNull();
        });
    });

    it('renders completed with a collapsed answer preview', () => {
        renderCard(makeTool({
            status: ToolCallStatus.Success,
            result: 'Answer from Projects Agent (@projects)\n\nThe realtime sync engine shipped.',
        }));

        expect(screen.getByText('Completed')).not.toBeNull();
        expect(screen.getByText('Answer')).not.toBeNull();
        expect(screen.queryByText('Accept')).toBeNull();
    });

    it('renders only the rejected state for a rejected call', () => {
        renderCard(makeTool({status: ToolCallStatus.Rejected}));

        expect(screen.getByText('Rejected')).not.toBeNull();
        expect(screen.queryByText('Starting delegation…')).toBeNull();
        expect(screen.queryByText('Waiting for your approval to delegate this task')).toBeNull();
        expect(screen.queryByText('Accept')).toBeNull();
    });

    it('local decision replaces the approval prompt', () => {
        renderCard(makeTool(), {localDecision: true});

        expect(screen.getByText('Accepted')).not.toBeNull();
        expect(screen.queryByText('Waiting for your approval to delegate this task')).toBeNull();
        expect(screen.queryByText('Accept')).toBeNull();
        expect(screen.queryByText('Reject')).toBeNull();
    });

    it('expands a truncated task via a keyboard-accessible button', () => {
        const longTask = 'x'.repeat(400);
        renderCard(makeTool({arguments: {agent: 'projects', task: longTask}}));

        const toggle = screen.getByRole('button', {expanded: false});
        expect(toggle.textContent).toContain('…');
        fireEvent.click(toggle);
        expect(screen.getByRole('button', {expanded: true}).textContent).toContain(longTask);
    });

    it('renders failure detail from the tool result', () => {
        renderCard(makeTool({
            status: ToolCallStatus.Error,
            result: 'Error: the user does not have access to this agent',
        }));

        expect(screen.getByText('Failed')).not.toBeNull();
        expect(screen.getByTestId('delegation-error-detail').textContent).toContain('does not have access');
    });

    it('offers share decision in the channel result stage', () => {
        renderCard(
            makeTool({status: ToolCallStatus.Success, result: 'the answer'}),
            {approvalStage: 'result'},
        );

        expect(screen.getByText('Share')).not.toBeNull();
        expect(screen.getByText('Keep private')).not.toBeNull();
    });
});
