// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {AppRenderer} from '@mcp-ui/client';
import {LockIcon} from '@mattermost/compass-icons/components';
import {useSelector} from 'react-redux';

// eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {GlobalState} from '@mattermost/types/store';

import {useBotlist} from '@/bots';
import {
    AppResourceContents,
    AppResourceResponse,
    getMCPAppResource,
    MCPAppResourceError,
    MCPAppsBootstrap,
} from '@/client';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';
import manifest from '@/manifest';
import {ToolCall} from '@/components/tool_types';

import LoadingSpinner from '../assets/loading_spinner';

import {APP_DEFAULT_HEIGHT, clampAppHeight, maxAppHeight} from './app_sizing';
import {buildHostStyleVariables, resolveAppTheme} from './host_context';

interface MCPAppViewProps {
    postID: string;
    tool: ToolCall; // caller guarantees ui_meta?.resource_uri present + status ∈ {Success, AutoApproved}
    requesterUserID?: string; // conversation.user_id, for the D5 popover
}

type AppPhase =
    | {phase: 'loading'}
    | {phase: 'ready'; contents: AppResourceContents; response: AppResourceResponse}
    | {phase: 'auth_required'; authURL: string}
    | {phase: 'no_access'} // 403, or 401 again after a connect completed
    | {phase: 'unavailable'}; // 400/404/500/502/network/malformed

type CallToolResultShape = {
    content: Array<{type: 'text'; text: string}>;
    isError?: boolean;
};

const AppContainer = styled.div<{$height: number; $bordered: boolean}>`
    width: 100%;
    height: ${(p) => p.$height}px;
    margin-top: 8px;
    overflow: hidden;
    ${(p) => (p.$bordered ? `
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    ` : '')}

    /* AppFrame writes inline width/height on its iframe; the wrapper is the
       single source of truth for D9, so neutralize them. */
    iframe {
        width: 100% !important;
        height: 100% !important;
        border: none;
        display: block;
    }
`;

const StatusRow = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 8px;
    font-size: 11px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const SubtleNote = styled.div`
    margin-top: 8px;
    font-size: 11px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ConnectButton = styled.button`
    background: rgba(var(--button-bg-rgb), 0.08);
    color: var(--button-bg);
    border: none;
    padding: 4px 10px;
    height: 24px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    cursor: pointer;

    &:hover {
        background: rgba(var(--button-bg-rgb), 0.12);
    }

    &:active {
        background: rgba(var(--button-bg-rgb), 0.16);
    }
`;

const SmallSpinner = styled(LoadingSpinner)`
    width: 12px;
    height: 12px;
`;

const NoAccessIcon = styled(LockIcon)`
    width: 12px;
    height: 12px;
    flex-shrink: 0;
`;

const MCPAppView: React.FC<MCPAppViewProps> = ({postID, tool, requesterUserID}) => {
    const {formatMessage, locale} = useIntl();
    useBotlist();

    const mcpApps = useSelector((state: any) => state['plugins-' + manifest.id]?.mcpApps as MCPAppsBootstrap | undefined);
    const requesterUsername = useSelector((state: GlobalState) => (
        requesterUserID ? state.entities.users.profiles[requesterUserID]?.username : undefined // eslint-disable-line no-undefined
    ));

    const sandboxUrl = useMemo(() => {
        if (!mcpApps?.enabled || !mcpApps.sandboxURL) {
            return null;
        }
        try {
            return new URL(mcpApps.sandboxURL);
        } catch {
            return null;
        }
    }, [mcpApps?.enabled, mcpApps?.sandboxURL]);

    const [phase, setPhase] = useState<AppPhase>({phase: 'loading'});
    const [height, setHeight] = useState(APP_DEFAULT_HEIGHT);
    const cachedResponseRef = useRef<AppResourceResponse | null>(null);
    const connectAttemptedRef = useRef(false);

    const fetchResource = useCallback(async () => {
        try {
            const response = await getMCPAppResource(postID, tool.id);
            const contents = response.contents?.[0];
            if (!contents?.text) {
                setPhase({phase: 'unavailable'});
                return;
            }
            cachedResponseRef.current = response;
            setPhase({phase: 'ready', contents, response});
        } catch (err) {
            if (err instanceof MCPAppResourceError) {
                if (err.status === 401 && err.authURL) {
                    if (connectAttemptedRef.current) {
                        setPhase({phase: 'no_access'});
                    } else {
                        setPhase({phase: 'auth_required', authURL: err.authURL});
                    }
                    return;
                }
                if (err.status === 401 || err.status === 403) {
                    setPhase({phase: 'no_access'});
                    return;
                }
            }
            setPhase({phase: 'unavailable'});
        }
    }, [postID, tool.id]);

    useEffect(() => {
        let cancelled = false;
        if (!sandboxUrl) {
            return () => {
                cancelled = true;
            };
        }
        setPhase({phase: 'loading'});
        cachedResponseRef.current = null;
        (async () => {
            try {
                const response = await getMCPAppResource(postID, tool.id);
                if (cancelled) {
                    return;
                }
                const contents = response.contents?.[0];
                if (!contents?.text) {
                    setPhase({phase: 'unavailable'});
                    return;
                }
                cachedResponseRef.current = response;
                setPhase({phase: 'ready', contents, response});
            } catch (err) {
                if (cancelled) {
                    return;
                }
                if (err instanceof MCPAppResourceError) {
                    if (err.status === 401 && err.authURL) {
                        setPhase(connectAttemptedRef.current ? {phase: 'no_access'} : {phase: 'auth_required', authURL: err.authURL});
                        return;
                    }
                    if (err.status === 401 || err.status === 403) {
                        setPhase({phase: 'no_access'});
                        return;
                    }
                }
                setPhase({phase: 'unavailable'});
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [postID, tool.id, sandboxUrl]);

    useMCPConnectionEvents(useCallback((event) => {
        if (event.status !== 'connected') {
            return;
        }
        if (event.serverOrigin && tool.server_origin && event.serverOrigin !== tool.server_origin) {
            return;
        }
        fetchResource();
    }, [fetchResource, tool.server_origin]));

    const hostContext = useMemo(() => ({
        theme: resolveAppTheme(),
        styles: {variables: buildHostStyleVariables()},
        locale,
        platform: 'web' as const,
        containerDimensions: {maxHeight: maxAppHeight(window.innerHeight)},
    }), [locale]);

    const toolInput = useMemo(() => {
        if (tool.arguments != null && typeof tool.arguments === 'object' && !Array.isArray(tool.arguments)) {
            return tool.arguments as Record<string, unknown>;
        }
        return {};
    }, [tool.arguments]);

    const toolResult = useMemo((): CallToolResultShape | undefined => {
        if (tool.result == null) {
            return undefined; // eslint-disable-line no-undefined
        }
        return {content: [{type: 'text' as const, text: tool.result}]};
    }, [tool.result]);

    const onReadResource = useCallback(async ({uri}: {uri: string}) => {
        if (uri === tool.ui_meta?.resource_uri && cachedResponseRef.current) {
            return cachedResponseRef.current as any;
        }
        throw new Error('resource not available');
    }, [tool.ui_meta?.resource_uri]);

    const onCallTool = useCallback(async (): Promise<CallToolResultShape> => ({
        content: [{
            type: 'text',
            text: formatMessage({
                defaultMessage: 'This app is display-only in Mattermost right now. Interactive app actions will be supported in a future release.',
            }),
        }],
        isError: true,
    }), [formatMessage]);

    const onMessage = useCallback(async (params: unknown) => {
        // eslint-disable-next-line no-console
        console.debug('[mcp-apps] ui/message ignored (Phase 1)', params);
        return {};
    }, []);

    const onOpenLink = useCallback(async (params: {url: string}) => {
        try {
            const parsed = new URL(params.url);
            if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
                return {};
            }
            window.open(params.url, '_blank', 'noopener,noreferrer');
        } catch {
            // ignore invalid URLs
        }
        return {};
    }, []);

    const onSizeChanged = useCallback((params: {height?: number}) => {
        if (params.height != null) {
            setHeight(clampAppHeight(params.height));
        }
    }, []);

    const onRendererError = useCallback(() => {
        setPhase({phase: 'unavailable'});
    }, []);

    const onFallbackRequest = useCallback(async () => {
        throw new Error('method not supported');
    }, []);

    const handleConnect = useCallback((authURL: string) => {
        connectAttemptedRef.current = true;
        window.open(authURL, '_blank', 'noopener,noreferrer');
    }, []);

    if (!sandboxUrl) {
        return null;
    }

    if (phase.phase === 'loading') {
        return (
            <StatusRow data-testid='mcp-app-loading'>
                <SmallSpinner/>
                <FormattedMessage defaultMessage='Loading app…'/>
            </StatusRow>
        );
    }

    if (phase.phase === 'auth_required') {
        return (
            <StatusRow>
                <ConnectButton
                    type='button'
                    onClick={() => handleConnect(phase.authURL)}
                >
                    <FormattedMessage defaultMessage='Connect to view'/>
                </ConnectButton>
            </StatusRow>
        );
    }

    if (phase.phase === 'no_access') {
        const message = requesterUsername ? (
            <FormattedMessage
                defaultMessage="@{username} has access to this app, but you don't, so we can't render it for you."
                values={{username: requesterUsername}}
            />
        ) : (
            <FormattedMessage defaultMessage="The requester has access to this app, but you don't, so we can't render it for you."/>
        );
        return (
            <StatusRow data-testid='mcp-app-no-access'>
                <NoAccessIcon/>
                <OverlayTrigger
                    placement='top'
                    overlay={<Tooltip id='mcp-app-no-access-tooltip'>{message}</Tooltip>}
                >
                    <span>{message}</span>
                </OverlayTrigger>
            </StatusRow>
        );
    }

    if (phase.phase === 'unavailable') {
        return (
            <SubtleNote data-testid='mcp-app-unavailable'>
                <FormattedMessage defaultMessage="The interactive view for this tool couldn't be loaded."/>
            </SubtleNote>
        );
    }

    const {contents} = phase;
    const resourceUI = contents._meta?.ui; // eslint-disable-line no-underscore-dangle
    return (
        <AppContainer
            $height={height}
            $bordered={resourceUI?.prefersBorder !== false}
            data-testid='mcp-app-view'
        >
            <AppRenderer
                toolName={tool.name}
                sandbox={{url: sandboxUrl, csp: resourceUI?.csp}}
                toolResourceUri={tool.ui_meta!.resource_uri}
                onReadResource={onReadResource}
                toolInput={toolInput}
                toolResult={toolResult as any}
                hostContext={hostContext as any}
                onCallTool={onCallTool as any}
                onMessage={onMessage as any}
                onOpenLink={onOpenLink as any}
                onSizeChanged={onSizeChanged}
                onError={onRendererError}
                onFallbackRequest={onFallbackRequest as any}
            />
        </AppContainer>
    );
};

export default MCPAppView;
