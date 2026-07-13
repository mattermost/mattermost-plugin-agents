// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent} from '@testing-library/react';
import {Provider} from 'react-redux';
import {IntlProvider} from 'react-intl';

import manifest from '@/manifest';

import {ToolCall, ToolCallStatus} from '../tool_types';

import {renderToolCall, ToolRenderContext} from './registry';

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/client', () => ({
    getChannelById: jest.fn(() => Promise.resolve({display_name: 'Fetched', team_display_name: 'T', type: 'O'})),
    getProfilesByUsernames: jest.fn(() => Promise.resolve([])),
    getProfilePictureUrl: jest.fn(() => 'avatar.png'),
}));

const state = {
    entities: {
        general: {config: {SiteURL: 'http://localhost:8065'}},
        teams: {currentTeamId: 'team1', teams: {team1: {id: 'team1', display_name: 'Eng'}}},
        channels: {channels: {chan1: {id: 'chan1', display_name: 'Town Square', team_id: 'team1', type: 'O'}}},
        users: {profiles: {}},
    },
    ['plugins-' + manifest.id]: {allowUnsafeLinks: false},
};

const store = {
    getState: () => state,
    subscribe: () => jest.fn(),
    dispatch: jest.fn(),
} as any;

beforeEach(() => {
    (window as any).PostUtils = {
        formatText: (t: string) => t,
        messageHtmlToComponent: (t: string) => <div>{t}</div>,
    };
});

function makeCtx(tool: ToolCall): ToolRenderContext {
    return {
        postID: 'post_1',
        tool,
        isCollapsed: false,
        isProcessing: false,
        onToggleCollapse: jest.fn(),
        canExpand: true,
        showArguments: true,
        showResults: false,
        approvalStage: 'call',
        isAutoApproved: false,
        canAnswer: true,
    };
}

function renderTool(tool: ToolCall) {
    return render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                {renderToolCall(makeCtx(tool))}
            </IntlProvider>
        </Provider>,
    );
}

function makeTool(overrides: Partial<ToolCall>): ToolCall {
    return {
        id: 'tc1',
        name: 'test_tool',
        description: '',
        status: ToolCallStatus.Pending,
        ...overrides,
    };
}

describe('renderToolCall routing', () => {
    test('routes a valid select question to QuestionCard', () => {
        renderTool(makeTool({
            name: 'AskUserQuestion',
            user_interaction: 'select',
            arguments: {question: 'Pick one', options: [{label: 'A'}, {label: 'B'}]},
        }));

        expect(screen.getByText('Pick one')).not.toBeNull();
    });

    test('a redacted select (null args) falls back to the generic card', () => {
        renderTool(makeTool({
            name: 'AskUserQuestion',
            user_interaction: 'select',
            arguments: undefined, // eslint-disable-line no-undefined
        }));

        // No question rendered; the generic card shows the tool name in the
        // header and no options.
        expect(screen.getByText('AskUserQuestion')).not.toBeNull();
        expect(screen.queryByText('Pick one')).toBeNull();
    });

    test('routes an embedded create_post to the rich CreatePostCard', () => {
        renderTool(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',
            arguments: {channel_id: 'chan1', channel_display_name: 'Town Square', team_display_name: 'Eng', message: 'Hello team'},
        }));

        // Rich card sections + the resolved channel chip + message text.
        expect(screen.getByText('Channel')).not.toBeNull();
        expect(screen.getByText('Message')).not.toBeNull();
        expect(screen.getByText('Hello team')).not.toBeNull();
        expect(screen.getByText(/Town Square/)).not.toBeNull();
    });

    test('routes embedded search_posts to a query + filters card', () => {
        renderTool(makeTool({
            name: 'mattermost__search_posts',
            mcp_bare_name: 'search_posts',
            server_origin: 'embedded://mattermost',
            arguments: {query: 'roadmap', from: 'sysadmin'},
        }));

        expect(screen.getByText('Query')).not.toBeNull();
        expect(screen.getByText('roadmap')).not.toBeNull();
        expect(screen.getByText('Filters')).not.toBeNull();
        expect(screen.getByText('From')).not.toBeNull();
        expect(screen.getByText('sysadmin')).not.toBeNull();
    });

    test('an unknown embedded tool falls back to the generic field list', () => {
        renderTool(makeTool({
            name: 'mattermost__unknown_tool',
            mcp_bare_name: 'unknown_tool',
            server_origin: 'embedded://mattermost',
            arguments: {foo: 'bar'},
        }));

        // Generic ToolCard: prettified name header + prettified field label.
        expect(screen.getByText('Unknown Tool')).not.toBeNull();
        expect(screen.getByText('Foo')).not.toBeNull();
        expect(screen.getByText('bar')).not.toBeNull();

        // Not a rich card: no "Channel"/"Query" section labels.
        expect(screen.queryByText('Channel')).toBeNull();
        expect(screen.queryByText('Query')).toBeNull();
    });

    test('a create_post-named tool from an EXTERNAL server does not get the rich card', () => {
        renderTool(makeTool({
            name: 'jira__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'https://mcp.atlassian.com',
            arguments: {message: 'hi', channel_id: 'chan1'},
        }));

        // Generic field list (Message as a prettified field label), not the
        // rich CreatePostCard (which would render a "Channel" section).
        expect(screen.queryByText('Channel')).toBeNull();
        expect(screen.getByText('Message')).not.toBeNull();
    });

    test('malformed embedded create_post args fall back to the generic card', () => {
        renderTool(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',

            // Missing the required message field.
            arguments: {channel_id: 'chan1'},
        }));

        // Falls back: generic field list shows the raw channel_id field, and
        // there is no rich "Message" section.
        expect(screen.getByText('Channel Id')).not.toBeNull();
        expect(screen.queryByText('Message')).toBeNull();
    });
});

describe('rich cards inherit the shell View raw affordance', () => {
    test('CreatePostCard exposes View raw showing the exact payload', () => {
        const args = {channel_id: 'chan1', channel_display_name: 'Town Square', team_display_name: 'Eng', message: 'Hello team'};
        const {container} = renderTool(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',
            arguments: args,
        }));

        const viewRaw = screen.getByText('View raw');
        fireEvent.click(viewRaw);

        const pre = container.querySelector('pre');
        expect(pre).not.toBeNull();
        expect(pre?.textContent).toBe(JSON.stringify(args, null, 2));
    });
});
