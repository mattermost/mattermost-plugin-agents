// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ToolCard from './tool_card';
import {ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'tool_1',
        name: 'create_jira_issue',
        description: '',
        status: ToolCallStatus.Pending,
        ...overrides,
    };
}

function renderComponent(
    tool: ToolCall,
    extra: {
        showResults?: boolean;
        onApprove?: () => void;
        onReject?: () => void;
        approvalStage?: ToolApprovalStage;
    } = {},
) {
    return render(
        <IntlProvider locale='en'>
            <ToolCard
                tool={tool}
                isCollapsed={false}
                isProcessing={false}
                onToggleCollapse={jest.fn()}
                canExpand={false}
                showArguments={true}
                showResults={extra.showResults ?? false}
                {...extra}
            />
        </IntlProvider>,
    );
}

describe('ToolCard argument rendering', () => {
    test('shows the no-parameters message for explicit empty object arguments', () => {
        renderComponent(makeTool({arguments: {}}));

        expect(screen.getByText(/No parameters required/)).not.toBeNull();
    });

    test('renders no arguments section for hidden (redacted) arguments', () => {
        renderComponent(makeTool({}));

        expect(screen.queryByText(/No parameters required/)).toBeNull();
        expect(screen.queryByText('View raw')).toBeNull();
    });

    test('renders a readable field list for non-empty arguments (not a JSON blob)', () => {
        renderComponent(makeTool({arguments: {channel_id: 'c1', message: 'hi'}}));

        expect(screen.getByText('Channel Id')).not.toBeNull();
        expect(screen.getByText('c1')).not.toBeNull();
        expect(screen.getByText('Message')).not.toBeNull();
        expect(screen.getByText('hi')).not.toBeNull();

        // The required raw-inspection affordance is present.
        expect(screen.getByText('View raw')).not.toBeNull();
    });
});

describe('ToolCard result rendering', () => {
    test('renders a plain-text result without markdown side effects', () => {
        const {container} = renderComponent(
            makeTool({
                status: ToolCallStatus.Success,
                arguments: {q: 'x'},
                result: 'line one\n![img](http://evil.example/x.png)',
            }),
            {showResults: true},
        );

        expect(screen.getByText('Response')).not.toBeNull();
        expect(screen.getByText(/line one/)).not.toBeNull();
        expect(container.querySelector('img')).toBeNull();
    });

    test('renders a JSON-object result as a labeled field list', () => {
        renderComponent(
            makeTool({
                status: ToolCallStatus.Success,
                arguments: {q: 'x'},
                result: JSON.stringify({total: 2, note: 'ok'}),
            }),
            {showResults: true},
        );

        expect(screen.getByText('Total')).not.toBeNull();
        expect(screen.getByText('2')).not.toBeNull();
        expect(screen.getByText('Note')).not.toBeNull();
        expect(screen.getByText('ok')).not.toBeNull();
    });
});

describe('ToolCard pending state', () => {
    test('shows a spinner without buttons for a live auto-executing tool', () => {
        const {container} = renderComponent(
            makeTool({would_auto_execute: true}),
            {approvalStage: 'done'},
        );

        expect(container.querySelector('svg')).not.toBeNull();
        expect(screen.queryByRole('button', {name: 'Accept'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Reject'})).toBeNull();
    });

    test('never shows accept or reject for a policy-approved pending tool', () => {
        renderComponent(
            makeTool({would_auto_execute: true}),
            {
                approvalStage: 'call',
                onApprove: jest.fn(),
                onReject: jest.fn(),
            },
        );

        expect(screen.queryByRole('button', {name: 'Accept'})).toBeNull();
        expect(screen.queryByRole('button', {name: 'Reject'})).toBeNull();
    });
});
