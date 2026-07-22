// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {AppRenderer, type AppRendererProps} from '@mcp-ui/client';
import {LockIcon} from '@mattermost/compass-icons/components';
import {useSelector} from 'react-redux';

// eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {GlobalState} from '@mattermost/types/store';

import {useBotlist} from '@/bots';
import {MCPAppsBootstrap} from '@/client';
import manifest from '@/manifest';
import {ToolCall} from '@/components/tool_types';

import LoadingSpinner from '../assets/loading_spinner';

import {prefersAppBorder} from './app_border';
import {APP_DEFAULT_HEIGHT, clampAppHeight, maxAppHeight} from './app_sizing';
import {buildHostStyleVariables, resolveAppTheme} from './host_context';
import {useMCPAppResource} from './use_mcp_app_resource';

interface MCPAppViewProps {
    postID: string;
    tool: ToolCall; // caller guarantees ui_meta?.resource_uri present + status ∈ {Success, AutoApproved}
    requesterUserID?: string; // conversation.user_id, for the D5 popover
}

type OnCallTool = NonNullable<AppRendererProps['onCallTool']>;
type OnMessage = NonNullable<AppRendererProps['onMessage']>;
type OnOpenLink = NonNullable<AppRendererProps['onOpenLink']>;
type OnFallbackRequest = NonNullable<AppRendererProps['onFallbackRequest']>;
type HostContext = NonNullable<AppRendererProps['hostContext']>;
type ToolResult = NonNullable<AppRendererProps['toolResult']>;

const AppContainer = styled.div<{$height: number; $bordered: boolean}>`
    width: 100%;
    height: ${(p) => p.$height}px;
    margin-top: 8px;
    overflow: hidden;
    ${(p) => (p.$bordered ? `
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    ` : '')}

    /* Container-authoritative sizing: constrain the lib iframe without
       fighting its inline width/height via !important. */
    iframe {
        min-width: 100%;
        max-width: 100%;
        min-height: 100%;
        max-height: 100%;
        border: none;
        display: block;
        box-sizing: border-box;
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

const displayOnlyFailure = (text: string): ToolResult => ({
    content: [{type: 'text', text}],
    isError: true,
});

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

    const {phase, retry, markConnectAttempted, setUnavailable} = useMCPAppResource({
        postID,
        toolCallID: tool.id,
        serverOrigin: tool.server_origin,
        enabled: Boolean(sandboxUrl),
    });

    const [viewportHeight, setViewportHeight] = useState(() => window.innerHeight);
    const [height, setHeight] = useState(() => clampAppHeight(APP_DEFAULT_HEIGHT));

    useEffect(() => {
        const onResize = () => {
            const next = window.innerHeight;
            setViewportHeight(next);
            setHeight((prev) => clampAppHeight(prev, next));
        };
        window.addEventListener('resize', onResize);
        return () => window.removeEventListener('resize', onResize);
    }, []);

    const hostContext = useMemo((): HostContext => ({
        theme: resolveAppTheme(),
        styles: {variables: buildHostStyleVariables()},
        locale,
        platform: 'web',
        containerDimensions: {maxHeight: maxAppHeight(viewportHeight)},
    }), [locale, viewportHeight]);

    const toolInput = useMemo(() => {
        if (tool.arguments != null && typeof tool.arguments === 'object' && !Array.isArray(tool.arguments)) {
            return tool.arguments as Record<string, unknown>;
        }
        return {};
    }, [tool.arguments]);

    const toolResult = useMemo((): ToolResult | undefined => {
        if (tool.result == null) {
            return undefined; // eslint-disable-line no-undefined
        }
        return {content: [{type: 'text', text: tool.result}]};
    }, [tool.result]);

    const onCallTool = useCallback<OnCallTool>(async () => displayOnlyFailure(formatMessage({
        defaultMessage: 'This app is display-only in Mattermost right now. Interactive app actions will be supported in a future release.',
    })), [formatMessage]);

    const onMessage = useCallback<OnMessage>(async (params) => {
        // eslint-disable-next-line no-console
        console.debug('[mcp-apps] ui/message ignored (Phase 1)', params);
        return {isError: true};
    }, []);

    const onOpenLink = useCallback<OnOpenLink>(async (params) => {
        try {
            const parsed = new URL(params.url);
            if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
                return {isError: true};
            }
            const opened = window.open(params.url, '_blank', 'noopener,noreferrer');
            if (!opened) {
                return {isError: true};
            }
            return {};
        } catch {
            return {isError: true};
        }
    }, []);

    const onSizeChanged = useCallback((params: {height?: number}) => {
        if (params.height != null) {
            setHeight(clampAppHeight(params.height));
        }
    }, []);

    const onFallbackRequest = useCallback<OnFallbackRequest>(async () => {
        throw new Error('method not supported');
    }, []);

    const handleConnect = useCallback((authURL: string) => {
        markConnectAttempted();
        window.open(authURL, '_blank', 'noopener,noreferrer');
    }, [markConnectAttempted]);

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
            <StatusRow data-testid='mcp-app-auth-required'>
                {!phase.connectAttempted && (
                    <ConnectButton
                        type='button'
                        onClick={() => handleConnect(phase.authURL)}
                    >
                        <FormattedMessage defaultMessage='Connect to view'/>
                    </ConnectButton>
                )}
                {phase.connectAttempted && (
                    <ConnectButton
                        type='button'
                        data-testid='mcp-app-try-again'
                        onClick={retry}
                    >
                        <FormattedMessage defaultMessage='Try again'/>
                    </ConnectButton>
                )}
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
    // Resource-declared permissions are typed on the wire but not mapped to
    // SandboxConfig.permissions / iframe allow in display-only V1 (Phase 2).
    return (
        <AppContainer
            $height={height}
            $bordered={prefersAppBorder(resourceUI?.prefersBorder)}
            data-testid='mcp-app-view'
        >
            <AppRenderer
                toolName={tool.name}
                sandbox={{url: sandboxUrl, csp: resourceUI?.csp}}
                html={contents.text}
                toolInput={toolInput}
                toolResult={toolResult}
                hostContext={hostContext}
                onCallTool={onCallTool}
                onMessage={onMessage}
                onOpenLink={onOpenLink}
                onSizeChanged={onSizeChanged}
                onError={setUnavailable}
                onFallbackRequest={onFallbackRequest}
            />
        </AppContainer>
    );
};

export default MCPAppView;
