// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render} from '@testing-library/react';
import type {AppFrameProps} from '@mcp-ui/client';

import MCPAppRenderer, {type MCPAppRendererProps, SafeAppBridge} from './mcp_app_renderer';

let capturedFrameProps: AppFrameProps | null = null;

jest.mock('@mcp-ui/client', () => {
    const actual = jest.requireActual('@mcp-ui/client');
    return {
        ...actual,
        AppFrame: (props: AppFrameProps) => {
            capturedFrameProps = props;
            return null;
        },
    };
});

function makeRendererProps(hostContext: MCPAppRendererProps['hostContext']): MCPAppRendererProps {
    return {
        sandbox: {url: new URL('http://sandbox.example/sandbox.html')},
        html: '<html>app</html>',
        toolInput: {},
        toolResult: {content: []},
        hostContext,
        onCallTool: async () => ({content: []}),
        onMessage: async () => ({}),
        onOpenLink: async () => ({}),
        onSizeChanged: jest.fn(),
        onError: jest.fn(),
        onFallbackRequest: async () => ({}),
    };
}

describe('SafeAppBridge', () => {
    test('catches a deferred host-context notification rejection', async () => {
        const bridge = new SafeAppBridge(
            null,
            {name: 'Mattermost', version: 'test'},
            {openLinks: {}},
            {hostContext: {theme: 'light'}},
        );
        const debug = jest.spyOn(console, 'debug').mockImplementation(() => null); // eslint-disable-line no-console
        const unhandled: unknown[] = [];
        const onUnhandledRejection = (reason: unknown) => unhandled.push(reason);
        process.on('unhandledRejection', onUnhandledRejection);

        try {
            // With no transport connected, the SDK notification Promise rejects
            // with "Not connected" after setHostContext has already returned.
            bridge.setHostContext({theme: 'dark'});
            await new Promise((resolve) => setTimeout(resolve, 0));

            expect(debug).toHaveBeenCalledWith(
                '[mcp-apps] host context notification failed',
                expect.objectContaining({message: 'Not connected'}),
            );
            expect(unhandled).toEqual([]);
        } finally {
            process.off('unhandledRejection', onUnhandledRejection);
            debug.mockRestore();
        }
    });
});

describe('MCPAppRenderer', () => {
    test('defers changed host context until the app initializes', async () => {
        capturedFrameProps = null;
        const setHostContext = jest.spyOn(SafeAppBridge.prototype, 'setHostContext');
        const debug = jest.spyOn(console, 'debug').mockImplementation(() => null); // eslint-disable-line no-console
        const initial = makeRendererProps({theme: 'light', containerDimensions: {maxHeight: 700}});
        const {rerender} = render(React.createElement(MCPAppRenderer, initial));

        const changed = makeRendererProps({theme: 'light', containerDimensions: {maxHeight: 400}});
        rerender(React.createElement(MCPAppRenderer, changed));
        expect(setHostContext).not.toHaveBeenCalled();

        await act(async () => {
            capturedFrameProps!.onInitialized?.({});
            await Promise.resolve();
        });

        expect(setHostContext).toHaveBeenCalledTimes(1);
        expect(setHostContext).toHaveBeenCalledWith(changed.hostContext);
        setHostContext.mockRestore();
        debug.mockRestore();
    });
});
