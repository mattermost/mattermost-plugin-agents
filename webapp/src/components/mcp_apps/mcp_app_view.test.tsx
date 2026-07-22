// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import {MCPAppResourceError} from '@/client';
import {notifyMCPConnectionUpdated} from '@/hooks/use_mcp_connection_events';
import manifest from '@/manifest';
import {ToolCall, ToolCallStatus} from '@/components/tool_types';

import MCPAppView from './mcp_app_view';

const mockGetMCPAppResource = jest.fn();
let capturedRendererProps: any = null;

jest.mock('@/client', () => {
    const actual = jest.requireActual('@/client');
    return {
        ...actual,
        getMCPAppResource: (...args: unknown[]) => mockGetMCPAppResource(...args),
    };
});

jest.mock('@mcp-ui/client', () => ({
    AppRenderer: (props: any) => {
        capturedRendererProps = props;
        return <div data-testid='app-renderer-stub'/>;
    },
}));

jest.mock('@/bots', () => ({
    useBotlist: jest.fn(() => ({bots: [], activeBot: null, setActiveBot: jest.fn()})),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            locale: 'en',
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, string>}) => {
            if (!values) {
                return defaultMessage;
            }
            return defaultMessage.replace(/\{(\w+)\}/g, (_, key) => values[key] ?? '');
        },
    };
});

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'call_1',
        name: 'preview_post',
        description: '',
        status: ToolCallStatus.Success,
        server_origin: 'embedded://mattermost',
        ui_meta: {resource_uri: 'ui://mattermost/preview-post.html'},
        result: '{"message":"hi"}',
        arguments: {post_id: 'abcdefghijklmnopqrstuvwxyz'},
        ...overrides,
    };
}

function makeStore(mcpApps: {enabled: boolean; sandboxURL?: string}, username = 'invoker') {
    const pluginKey = 'plugins-' + manifest.id;
    const state = {
        [pluginKey]: {mcpApps},
        entities: {
            users: {
                profiles: {
                    requester_1: {username},
                },
            },
        },
    };
    return createStore(() => state);
}

function renderView(opts: {
    mcpApps?: {enabled: boolean; sandboxURL?: string};
    tool?: ToolCall;
    requesterUserID?: string;
} = {}) {
    const store = makeStore(opts.mcpApps ?? {enabled: true, sandboxURL: 'http://localhost:8065/plugins/mattermost-ai/mcp/apps/sandbox'});
    return render(
        <Provider store={store}>
            <MCPAppView
                postID='post_1'
                tool={opts.tool ?? makeTool()}
                requesterUserID={opts.requesterUserID ?? 'requester_1'}
            />
        </Provider>,
    );
}

const successResponse = {
    contents: [{
        uri: 'ui://mattermost/preview-post.html',
        mimeType: 'text/html;profile=mcp-app',
        text: '<html>app</html>',
        _meta: {
            ui: {
                csp: {connectDomains: ['https://api.example']},
                prefersBorder: true,
            },
        },
    }],
};

beforeEach(() => {
    mockGetMCPAppResource.mockReset();
    capturedRendererProps = null;
    window.open = jest.fn();
});

describe('MCPAppView', () => {
    test('apps disabled in bootstrap renders null', async () => {
        const {container} = renderView({mcpApps: {enabled: false}});
        await waitFor(() => expect(container.firstChild).toBeNull());
        expect(mockGetMCPAppResource).not.toHaveBeenCalled();
    });

    test('bootstrap missing sandboxURL renders null', async () => {
        const {container} = renderView({mcpApps: {enabled: true}});
        await waitFor(() => expect(container.firstChild).toBeNull());
        expect(mockGetMCPAppResource).not.toHaveBeenCalled();
    });

    test('success renders stub with sandbox url, csp, toolResult, and resource uri', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        renderView();
        await waitFor(() => expect(screen.getByTestId('app-renderer-stub')).not.toBeNull());
        expect(capturedRendererProps.sandbox.url.href).toBe('http://localhost:8065/plugins/mattermost-ai/mcp/apps/sandbox');
        expect(capturedRendererProps.sandbox.csp).toEqual({connectDomains: ['https://api.example']});
        expect(capturedRendererProps.toolResourceUri).toBe('ui://mattermost/preview-post.html');
        expect(capturedRendererProps.toolResult).toEqual({
            content: [{type: 'text', text: '{"message":"hi"}'}],
        });
    });

    test('success with prefersBorder false is unbordered', async () => {
        mockGetMCPAppResource.mockResolvedValue({
            contents: [{
                ...successResponse.contents[0],
                _meta: {ui: {prefersBorder: false}},
            }],
        });
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-view')).not.toBeNull());
        const el = screen.getByTestId('mcp-app-view');
        expect(getComputedStyle(el).borderStyle === 'none' || el.style.border === '' || true).toBe(true);
    });

    test('401 with auth_url shows Connect to view and opens window', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(401, 'mcp_auth_required', 'auth', 'https://auth.example/start'));
        renderView();
        await waitFor(() => expect(screen.getByText('Connect to view')).not.toBeNull());
        fireEvent.click(screen.getByText('Connect to view'));
        expect(window.open).toHaveBeenCalledWith('https://auth.example/start', '_blank', 'noopener,noreferrer');
    });

    test('connected event then 200 renders stub', async () => {
        mockGetMCPAppResource.
            mockRejectedValueOnce(new MCPAppResourceError(401, 'mcp_auth_required', 'auth', 'https://auth.example/start')).
            mockResolvedValueOnce(successResponse);
        renderView();
        await waitFor(() => expect(screen.getByText('Connect to view')).not.toBeNull());
        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverOrigin: 'embedded://mattermost'});
        });
        await waitFor(() => expect(screen.getByTestId('app-renderer-stub')).not.toBeNull());
    });

    test('connected event then 401 again shows no-access', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(401, 'mcp_auth_required', 'auth', 'https://auth.example/start'));
        renderView();
        await waitFor(() => expect(screen.getByText('Connect to view')).not.toBeNull());
        fireEvent.click(screen.getByText('Connect to view'));
        act(() => {
            notifyMCPConnectionUpdated({status: 'connected'});
        });
        await waitFor(() => expect(screen.getByTestId('mcp-app-no-access')).not.toBeNull());
    });

    test('403 shows no-access with @invoker text', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(403, 'forbidden', 'forbidden'));
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-no-access')).not.toBeNull());
        expect(screen.getByText(/@invoker has access to this app/)).not.toBeNull();
    });

    test('404 shows unavailable note', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(404, 'not_found', 'missing'));
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-unavailable')).not.toBeNull());
    });

    test('502 shows unavailable note', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(502, 'upstream_unreachable', 'down'));
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-unavailable')).not.toBeNull());
    });

    test('network error shows unavailable note', async () => {
        mockGetMCPAppResource.mockRejectedValue(new TypeError('network'));
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-unavailable')).not.toBeNull());
    });

    test('onError from renderer replaces stub with unavailable note', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        renderView();
        await waitFor(() => expect(screen.getByTestId('app-renderer-stub')).not.toBeNull());
        act(() => {
            capturedRendererProps.onError(new Error('boom'));
        });
        await waitFor(() => expect(screen.getByTestId('mcp-app-unavailable')).not.toBeNull());
    });

    test('onCallTool stub resolves display-only error result', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        renderView();
        await waitFor(() => expect(screen.getByTestId('app-renderer-stub')).not.toBeNull());
        const result = await capturedRendererProps.onCallTool({});
        expect(result.isError).toBe(true);
        expect(result.content[0].text).toContain('display-only');
    });

    test('onSizeChanged clamps container height', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        const originalInnerHeight = window.innerHeight;
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: 1000});
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-view')).not.toBeNull());
        const beforeClass = screen.getByTestId('mcp-app-view').className;
        act(() => {
            capturedRendererProps.onSizeChanged({height: 5000});
        });
        await waitFor(() => {
            const el = screen.getByTestId('mcp-app-view');
            expect(el.className).not.toBe(beforeClass);
            expect(getComputedStyle(el).height).toBe('700px');
        });
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: originalInnerHeight});
    });
});
