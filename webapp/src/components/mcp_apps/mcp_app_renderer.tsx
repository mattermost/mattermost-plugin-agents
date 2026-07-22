// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {
    AppBridge,
    AppFrame,
    type AppRendererProps,
    type McpUiHostContext,
} from '@mcp-ui/client';

import manifest from '@/manifest';

type AppBridgeInstance = {
    sendHostContextChange(params: Partial<McpUiHostContext>): Promise<void> | void;
    setHostContext(hostContext: McpUiHostContext): void;
    close(): Promise<void>;
    oncalltool?: NonNullable<AppRendererProps['onCallTool']>;
    onmessage?: NonNullable<AppRendererProps['onMessage']>;
    onopenlink?: NonNullable<AppRendererProps['onOpenLink']>;
    fallbackRequestHandler?: NonNullable<AppRendererProps['onFallbackRequest']>;
};
type HostContextChangedParams = Parameters<AppBridgeInstance['sendHostContextChange']>[0];
type AppBridgeConstructor = new (
    client: null,
    hostInfo: {name: string; version: string},
    capabilities: {openLinks: Record<string, never>},
    options: {hostContext: McpUiHostContext},
) => AppBridgeInstance;

// @mcp-ui/client@7.1.1's AppBridge runtime export is complete, but its
// re-exported declaration loses the class shape with this repo's TS resolver.
const AppBridgeBase = AppBridge as unknown as AppBridgeConstructor;

function logBridgeNotificationFailure(message: string, error: unknown) {
    // Bridge notification failures can race iframe setup/teardown. They are
    // non-fatal, but retain debug visibility for genuine transport failures.
    // eslint-disable-next-line no-console
    console.debug(message, error);
}

/**
 * AppBridge.setHostContext does not return the Promise created by
 * sendHostContextChange. Catch it at the virtual notification boundary so a
 * transport race cannot become an unhandled rejection.
 */
export class SafeAppBridge extends AppBridgeBase {
    override sendHostContextChange(params: HostContextChangedParams): void {
        const notification = super.sendHostContextChange(params);
        if (notification) {
            notification.catch((error: unknown) => {
                logBridgeNotificationFailure('[mcp-apps] host context notification failed', error);
            });
        }
    }
}

export type MCPAppRendererProps = {
    sandbox: AppRendererProps['sandbox'];
    html: string;
    toolInput: AppRendererProps['toolInput'];
    toolResult: AppRendererProps['toolResult'];
    hostContext: McpUiHostContext;
    onCallTool: NonNullable<AppRendererProps['onCallTool']>;
    onMessage: NonNullable<AppRendererProps['onMessage']>;
    onOpenLink: NonNullable<AppRendererProps['onOpenLink']>;
    onSizeChanged: NonNullable<AppRendererProps['onSizeChanged']>;
    onError: NonNullable<AppRendererProps['onError']>;
    onFallbackRequest: NonNullable<AppRendererProps['onFallbackRequest']>;
};

/**
 * Pre-fetched MCP Apps renderer.
 *
 * @mcp-ui/client@7.1.1's AppRenderer constructs AppBridge without its initial
 * hostContext, then calls setHostContext before AppFrame connects the bridge.
 * Use the library's public lower-level API so initial context participates in
 * ui/initialize and later context updates are sent only after initialization.
 */
const MCPAppRenderer: React.FC<MCPAppRendererProps> = (props) => {
    const handlersRef = useRef(props);
    handlersRef.current = props;

    const bridgeRef = useRef<SafeAppBridge | null>(null);
    if (!bridgeRef.current) {
        const bridge = new SafeAppBridge(
            null,
            {name: 'Mattermost', version: manifest.version},
            {openLinks: {}},
            {hostContext: props.hostContext},
        );
        bridge.oncalltool = (params, extra) => {
            return handlersRef.current.onCallTool(params, extra);
        };
        bridge.onmessage = (params, extra) => {
            return handlersRef.current.onMessage(params, extra);
        };
        bridge.onopenlink = (params, extra) => {
            return handlersRef.current.onOpenLink(params, extra);
        };
        bridge.fallbackRequestHandler = (request, extra) => {
            return handlersRef.current.onFallbackRequest(request, extra);
        };
        bridgeRef.current = bridge;
    }
    const appBridge = bridgeRef.current;
    const [initialized, setInitialized] = useState(false);

    const handleInitialized = useCallback(() => {
        setInitialized(true);
    }, []);

    useEffect(() => {
        if (initialized) {
            appBridge.setHostContext(props.hostContext);
        }
    }, [appBridge, initialized, props.hostContext]);

    useEffect(() => () => {
        appBridge.close().catch((error: unknown) => {
            logBridgeNotificationFailure('[mcp-apps] bridge close failed', error);
        });
    }, [appBridge]);

    return (
        <AppFrame
            html={props.html}
            sandbox={props.sandbox}
            appBridge={appBridge}
            toolInput={props.toolInput}
            toolResult={props.toolResult}
            onSizeChanged={props.onSizeChanged}
            onInitialized={handleInitialized}
            onError={props.onError}
        />
    );
};

export default MCPAppRenderer;
