// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent, waitFor} from '@testing-library/react';
import {Provider} from 'react-redux';
import {IntlProvider} from 'react-intl';

import {getPost} from '@/client';

import {ToolCall, ToolCallStatus} from '../tool_types';

import {renderToolCall, ToolRenderContext} from './registry';

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/client', () => ({
    getPost: jest.fn(),
    getProfilesByIds: jest.fn(() => Promise.resolve([])),
}));

const postMessagePreviewMock = jest.fn<React.ReactElement, [unknown]>(() => <div>{'post-preview-box'}</div>);

jest.mock('@/mm_webapp', () => ({
    PostMessagePreview: (props: unknown) => postMessagePreviewMock(props),
}));

const mockGetPost = getPost as jest.Mock;

const state = {
    entities: {
        general: {config: {SiteURL: 'http://localhost:8065'}},
        teams: {currentTeamId: 'team1', teams: {team1: {id: 'team1', display_name: 'Eng', name: 'eng'}}},
        channels: {channels: {chan1: {id: 'chan1', display_name: 'Town Square', team_id: 'team1', type: 'O'}}},
        users: {profiles: {}},
        posts: {posts: {}},
    },
};

const store = {
    getState: () => state,
    subscribe: () => jest.fn(),
    dispatch: jest.fn(),
} as any;

beforeEach(() => {
    mockGetPost.mockReset();
    postMessagePreviewMock.mockClear();
});

function makeCtx(tool: ToolCall): ToolRenderContext {
    return {
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

        expect(screen.getByText('AskUserQuestion')).not.toBeNull();
        expect(screen.queryByText('Pick one')).toBeNull();
    });

    test('routes an embedded read_post to the post preview card', async () => {
        mockGetPost.mockResolvedValue({id: 'p1', user_id: 'u1', channel_id: 'chan1', message: 'previewed message'});

        renderTool(makeTool({
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            arguments: {post_id: 'p1'},
        }));

        await waitFor(() => expect(screen.getByText('post-preview-box')).not.toBeNull());
        expect(mockGetPost).toHaveBeenCalledWith('p1');
    });

    test('read_post falls back to the generic field list when the post fetch fails', async () => {
        mockGetPost.mockRejectedValue(new Error('gone'));

        renderTool(makeTool({
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            arguments: {post_id: 'p-missing'},
        }));

        await waitFor(() => expect(screen.getByText('Post Id')).not.toBeNull());
        expect(screen.getByText('p-missing')).not.toBeNull();
        expect(screen.queryByText('post-preview-box')).toBeNull();
    });

    test('an executed read_post renders generically — no preview, no post fetch', () => {
        renderTool(makeTool({
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            status: ToolCallStatus.Success,
            arguments: {post_id: 'p1'},
            result: 'post content',
        }));

        expect(mockGetPost).not.toHaveBeenCalled();
        expect(screen.queryByText('post-preview-box')).toBeNull();
        expect(screen.getByText('Post Id')).not.toBeNull();
    });

    test('a read_post-named tool from an EXTERNAL server does not get the preview card', () => {
        renderTool(makeTool({
            name: 'jira__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'https://mcp.atlassian.com',
            arguments: {post_id: 'p1'},
        }));

        expect(mockGetPost).not.toHaveBeenCalled();
        expect(screen.getByText('Post Id')).not.toBeNull();
    });

    test('an unknown embedded tool renders the generic field list', () => {
        renderTool(makeTool({
            name: 'mattermost__create_post',
            mcp_bare_name: 'create_post',
            server_origin: 'embedded://mattermost',
            arguments: {channel_id: 'chan1', message: 'hi'},
        }));

        expect(screen.getByText('Channel Id')).not.toBeNull();
        expect(screen.getByText('Message')).not.toBeNull();
        expect(screen.getByText('hi')).not.toBeNull();
    });
});

describe('shell features on routed cards', () => {
    test('the post preview card exposes View raw with the exact payload', async () => {
        mockGetPost.mockResolvedValue({id: 'p1', user_id: 'u1', channel_id: 'chan1', message: 'previewed message'});
        const args = {post_id: 'p1', include_thread: true};

        const {container} = renderTool(makeTool({
            name: 'mattermost__read_post',
            mcp_bare_name: 'read_post',
            server_origin: 'embedded://mattermost',
            arguments: args,
        }));

        await waitFor(() => expect(screen.getByText('post-preview-box')).not.toBeNull());
        fireEvent.click(screen.getByText('View raw'));

        const pre = container.querySelector('pre');
        expect(pre?.textContent).toBe(JSON.stringify(args, null, 2));
    });

    test('a JSON-object result renders as a labeled field list', () => {
        render(
            <Provider store={store}>
                <IntlProvider locale='en'>
                    {renderToolCall({
                        ...makeCtx(makeTool({
                            name: 'some_tool',
                            status: ToolCallStatus.Success,
                            arguments: {q: 'x'},
                            result: JSON.stringify({found_count: 3, summary: 'three results'}),
                        })),
                        showResults: true,
                    })}
                </IntlProvider>
            </Provider>,
        );

        expect(screen.getByText('Response')).not.toBeNull();
        expect(screen.getByText('Found Count')).not.toBeNull();
        expect(screen.getByText('3')).not.toBeNull();
        expect(screen.getByText('Summary')).not.toBeNull();
        expect(screen.getByText('three results')).not.toBeNull();
    });

    test('a plain-text result renders as text, not markdown', () => {
        const result = 'Channel: Town Square\n\n![img](http://evil.example/x.png)';
        const {container} = render(
            <Provider store={store}>
                <IntlProvider locale='en'>
                    {renderToolCall({
                        ...makeCtx(makeTool({
                            name: 'some_tool',
                            status: ToolCallStatus.Success,
                            arguments: {q: 'x'},
                            result,
                        })),
                        showResults: true,
                    })}
                </IntlProvider>
            </Provider>,
        );

        expect(screen.getByText(/Town Square/)).not.toBeNull();
        expect(screen.getByText(/!\[img\]/)).not.toBeNull();
        expect(container.querySelector('img')).toBeNull();
    });
});
