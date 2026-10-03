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

import {APP_DEFAULT_HEIGHT, clampAppHeight} from './app_sizing';
import type {MCPAppRendererProps} from './mcp_app_renderer';
import MCPAppView from './mcp_app_view';

const mockGetMCPAppResource = jest.fn();
let capturedRendererProps: MCPAppRendererProps | null = null;

jest.mock('@/client', () => {
    const actual = jest.requireActual('@/client');
    return {
        ...actual,
        getMCPAppResource: (...args: unknown[]) => mockGetMCPAppResource(...args),
    };
});

jest.mock('./mcp_app_renderer', () => ({
    __esModule: true,
    default: (props: MCPAppRendererProps) => {
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
    postID?: string;
} = {}) {
    const store = makeStore(opts.mcpApps ?? {enabled: true, sandboxURL: 'http://localhost:8065/plugins/mattermost-ai/mcp/apps/sandbox'});
    return render(
        <Provider store={store}>
            <MCPAppView
                postID={opts.postID ?? 'post_1'}
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
    window.open = jest.fn(() => ({closed: false} as Window));
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

    test('success renders stub with sandbox url, csp, html, and toolResult', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        renderView();
        await waitFor(() => expect(screen.getByTestId('app-renderer-stub')).not.toBeNull());
        expect(capturedRendererProps!.sandbox.url.href).toBe('http://localhost:8065/plugins/mattermost-ai/mcp/apps/sandbox');
        expect(capturedRendererProps!.sandbox.csp).toEqual({connectDomains: ['https://api.example']});
        expect(capturedRendererProps!.html).toBe('<html>app</html>');
        expect(capturedRendererProps!.toolResult).toEqual({
            content: [{type: 'text', text: '{"message":"hi"}'}],
        });
    });

    test('prefersBorder true and false produce different container classNames', async () => {
        mockGetMCPAppResource.mockResolvedValue({
            contents: [{
                ...successResponse.contents[0],
                _meta: {ui: {prefersBorder: false}},
            }],
        });
        const first = renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-view')).not.toBeNull());
        const unborderedClass = screen.getByTestId('mcp-app-view').className;
        first.unmount();

        mockGetMCPAppResource.mockResolvedValue(successResponse);
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-view')).not.toBeNull());
        const borderedClass = screen.getByTestId('mcp-app-view').className;
        expect(borderedClass).not.toBe(unborderedClass);
    });

    test('401 with auth_url shows Connect to view and opens window', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(401, 'mcp_auth_required', 'auth', 'https://auth.example/start'));
        renderView();
        await waitFor(() => expect(screen.getByText('Connect to view')).not.toBeNull());
        fireEvent.click(screen.getByText('Connect to view'));
        expect(window.open).toHaveBeenCalledWith('https://auth.example/start', '_blank', 'noopener,noreferrer');
        await waitFor(() => expect(screen.getByTestId('mcp-app-try-again')).not.toBeNull());
    });

    test('Try again after connect attempt with 401 shows no-access', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(401, 'mcp_auth_required', 'auth', 'https://auth.example/start'));
        renderView();
        await waitFor(() => expect(screen.getByText('Connect to view')).not.toBeNull());
        fireEvent.click(screen.getByText('Connect to view'));
        await waitFor(() => expect(screen.getByTestId('mcp-app-try-again')).not.toBeNull());
        fireEvent.click(screen.getByTestId('mcp-app-try-again'));
        await waitFor(() => expect(screen.getByTestId('mcp-app-no-access')).not.toBeNull());
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

    test('connected event for other server origin does not refetch', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(401, 'mcp_auth_required', 'auth', 'https://auth.example/start'));
        renderView();
        await waitFor(() => expect(screen.getByText('Connect to view')).not.toBeNull());
        const callsBefore = mockGetMCPAppResource.mock.calls.length;
        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverOrigin: 'https://other.example/mcp'});
        });
        await act(async () => {
            await Promise.resolve();
        });
        expect(mockGetMCPAppResource.mock.calls.length).toBe(callsBefore);
        expect(screen.getByText('Connect to view')).not.toBeNull();
    });

    test('connected event then 401 after connect attempt shows no-access', async () => {
        mockGetMCPAppResource.mockRejectedValue(new MCPAppResourceError(401, 'mcp_auth_required', 'auth', 'https://auth.example/start'));
        renderView();
        await waitFor(() => expect(screen.getByText('Connect to view')).not.toBeNull());
        fireEvent.click(screen.getByText('Connect to view'));
        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverOrigin: 'embedded://mattermost'});
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
            capturedRendererProps!.onError?.(new Error('boom'));
        });
        await waitFor(() => expect(screen.getByTestId('mcp-app-unavailable')).not.toBeNull());
    });

    test('onCallTool stub resolves display-only error result', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        renderView();
        await waitFor(() => expect(screen.getByTestId('app-renderer-stub')).not.toBeNull());
        const result = await capturedRendererProps!.onCallTool!({name: 'x', arguments: {}}, {} as never);
        expect(result.isError).toBe(true);
        expect(result.content?.[0]).toMatchObject({type: 'text', text: expect.stringContaining('display-only')});
    });

    test('onMessage and failed onOpenLink return isError', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        renderView();
        await waitFor(() => expect(screen.getByTestId('app-renderer-stub')).not.toBeNull());
        await expect(capturedRendererProps!.onMessage!({role: 'user', content: []}, {} as never)).resolves.toEqual({isError: true});
        await expect(capturedRendererProps!.onOpenLink!({url: 'ftp://example.com/file'}, {} as never)).resolves.toEqual({isError: true});
        (window.open as jest.Mock).mockReturnValueOnce(null);
        await expect(capturedRendererProps!.onOpenLink!({url: 'https://example.com'}, {} as never)).resolves.toEqual({isError: true});
    });

    test('onSizeChanged clamps container height', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        const originalInnerHeight = window.innerHeight;
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: 1000});
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-view')).not.toBeNull());
        const beforeClass = screen.getByTestId('mcp-app-view').className;
        act(() => {
            capturedRendererProps!.onSizeChanged?.({height: 5000});
        });
        await waitFor(() => {
            const el = screen.getByTestId('mcp-app-view');
            expect(el.className).not.toBe(beforeClass);
            expect(getComputedStyle(el).height).toBe('700px');
        });
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: originalInnerHeight});
    });

    test('default height is clamped on a short viewport', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        const originalInnerHeight = window.innerHeight;
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: 200});
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-view')).not.toBeNull());
        expect(getComputedStyle(screen.getByTestId('mcp-app-view')).height).toBe(`${clampAppHeight(APP_DEFAULT_HEIGHT, 200)}px`);
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: originalInnerHeight});
    });

    test('viewport resize re-clamps container height', async () => {
        mockGetMCPAppResource.mockResolvedValue(successResponse);
        const originalInnerHeight = window.innerHeight;
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: 1000});
        renderView();
        await waitFor(() => expect(screen.getByTestId('mcp-app-view')).not.toBeNull());
        act(() => {
            capturedRendererProps!.onSizeChanged?.({height: 5000});
        });
        await waitFor(() => expect(getComputedStyle(screen.getByTestId('mcp-app-view')).height).toBe('700px'));
        act(() => {
            Object.defineProperty(window, 'innerHeight', {configurable: true, value: 400});
            window.dispatchEvent(new Event('resize'));
        });
        await waitFor(() => {
            expect(getComputedStyle(screen.getByTestId('mcp-app-view')).height).toBe(`${clampAppHeight(700, 400)}px`);
        });
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: originalInnerHeight});
    });

    test('stale slower response loses to newer request', async () => {
        let resolveFirst!: (value: typeof successResponse) => void;
        let resolveSecond!: (value: typeof successResponse) => void;
        const first = new Promise<typeof successResponse>((resolve) => {
            resolveFirst = resolve;
        });
        const second = new Promise<typeof successResponse>((resolve) => {
            resolveSecond = resolve;
        });
        mockGetMCPAppResource.
            mockReturnValueOnce(first).
            mockReturnValueOnce(second);

        renderView();
        await waitFor(() => expect(mockGetMCPAppResource).toHaveBeenCalledTimes(1));
        act(() => {
            notifyMCPConnectionUpdated({status: 'connected', serverOrigin: 'embedded://mattermost'});
        });
        await waitFor(() => expect(mockGetMCPAppResource).toHaveBeenCalledTimes(2));

        const secondHTML = {contents: [{...successResponse.contents[0], text: '<html>second</html>'}]};
        await act(async () => {
            resolveSecond(secondHTML);
            await Promise.resolve();
        });
        await waitFor(() => expect(capturedRendererProps?.html).toBe('<html>second</html>'));

        await act(async () => {
            resolveFirst(successResponse);
            await Promise.resolve();
        });
        expect(capturedRendererProps?.html).toBe('<html>second</html>');
    });

    test('unmount discards in-flight load', async () => {
        let resolveLoad!: (value: typeof successResponse) => void;
        mockGetMCPAppResource.mockReturnValue(new Promise((resolve) => {
            resolveLoad = resolve;
        }));
        const {unmount} = renderView();
        await waitFor(() => expect(mockGetMCPAppResource).toHaveBeenCalled());
        unmount();
        await act(async () => {
            resolveLoad(successResponse);
            await Promise.resolve();
        });
        expect(screen.queryByTestId('app-renderer-stub')).toBeNull();
    });

    test('postID change resets loader and ignores prior response', async () => {
        let resolveFirst!: (value: typeof successResponse) => void;
        mockGetMCPAppResource.
            mockReturnValueOnce(new Promise((resolve) => {
                resolveFirst = resolve;
            })).
            mockResolvedValueOnce({contents: [{...successResponse.contents[0], text: '<html>new-post</html>'}]});

        const store = makeStore({enabled: true, sandboxURL: 'http://localhost:8065/plugins/mattermost-ai/mcp/apps/sandbox'});
        const {rerender} = render(
            <Provider store={store}>
                <MCPAppView
                    postID='post_1'
                    tool={makeTool()}
                    requesterUserID='requester_1'
                />
            </Provider>,
        );
        await waitFor(() => expect(mockGetMCPAppResource).toHaveBeenCalledTimes(1));

        rerender(
            <Provider store={store}>
                <MCPAppView
                    postID='post_2'
                    tool={makeTool({id: 'call_2'})}
                    requesterUserID='requester_1'
                />
            </Provider>,
        );
        await waitFor(() => expect(mockGetMCPAppResource).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(capturedRendererProps?.html).toBe('<html>new-post</html>'));

        await act(async () => {
            resolveFirst(successResponse);
            await Promise.resolve();
        });
        expect(capturedRendererProps?.html).toBe('<html>new-post</html>');
    });
});
