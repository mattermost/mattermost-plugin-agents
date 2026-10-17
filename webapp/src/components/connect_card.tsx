// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {AlertCircleOutlineIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';
import {getUserMCPServers} from '@/utils/user_mcp_servers';

import {ToolCall, ToolCallStatus} from './tool_types';
import {
    InteractionCard,
    InteractionFooter,
    InteractionFooterButtons,
    InteractionStatusLine,
    InteractionTitle,
    ResolvedIcon,
    SkippedIcon,
} from './interaction_card_shell';

// Parsed shape of the ConnectMCPServer tool arguments. Mirrors the server-side
// ConnectMCPServer args ({"server": "<configured server name>"}).
export interface ConnectArgs {
    server: string;
}

// parseConnectArgs extracts the server name from tool call arguments. Returns
// null when the arguments are missing or malformed (e.g. redacted for
// non-requesters).
export function parseConnectArgs(args: ToolCall['arguments']): ConnectArgs | null {
    if (args == null || typeof args !== 'object' || Array.isArray(args)) {
        return null;
    }
    const server = (args as {[key: string]: unknown}).server;
    if (typeof server !== 'string' || server.trim() === '') {
        return null;
    }
    return {server};
}

// 'unexpected' covers statuses a connect call never reaches (it is never
// auto-executed); the card then shows only its title.
type ConnectCardState = 'interactive' | 'submitting' | 'waiting' | 'connected' | 'skipped' | 'failed' | 'unexpected';

function computeState(status: ToolCallStatus, canAnswer: boolean, busy: boolean): ConnectCardState {
    switch (status) {
    case ToolCallStatus.Pending:
        if (busy) {
            return 'submitting';
        }
        return canAnswer ? 'interactive' : 'waiting';

    // Accepted: the server is still verifying the connection; it records
    // Success only once the server's tools are available.
    case ToolCallStatus.Accepted:
        return 'submitting';
    case ToolCallStatus.Success:
        return 'connected';
    case ToolCallStatus.Rejected:
        return 'skipped';
    case ToolCallStatus.Error:
        return 'failed';
    case ToolCallStatus.AutoApproved:
        return 'unexpected';
    default: {
        const unreachable: never = status;
        return unreachable;
    }
    }
}

// The requester's connection status for the server, from the user's MCP
// server list.
type ServerLookup =
    | {kind: 'loading'}
    | {kind: 'error'}
    | {kind: 'missing'}
    | {kind: 'found'; authenticated: boolean; authURL?: string};

async function lookupServer(server: string): Promise<ServerLookup> {
    try {
        const response = await getUserMCPServers();
        const match = response.servers.find((s) => s.name === server);
        if (!match || (!match.authenticated && !match.authURL)) {
            return {kind: 'missing'};
        }
        return {kind: 'found', authenticated: match.authenticated, authURL: match.authURL};
    } catch {
        return {kind: 'error'};
    }
}

const ServerName = styled.span`
    font-weight: 600;
`;

const Explanation = styled.div`
    padding-left: 12px;
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    word-break: break-word;
`;

const FailedIcon = styled(AlertCircleOutlineIcon)`
    color: var(--error-text);
`;

interface ConnectCardProps {
    tool: ToolCall;

    // Null when the arguments are redacted (non-requesters).
    server: string | null;
    isProcessing: boolean;
    localDecision?: boolean;
    canAnswer: boolean;
    onAccept?: () => void;
    onSkip?: () => void;
}

const ConnectCard: React.FC<ConnectCardProps> = ({
    tool,
    server,
    isProcessing,
    localDecision,
    canAnswer,
    onAccept,
    onSkip,
}) => {
    const busy = isProcessing || localDecision != null;
    const state = computeState(tool.status, canAnswer && Boolean(server && onAccept && onSkip), busy);
    const interactive = state === 'interactive';

    const [lookup, setLookup] = useState<ServerLookup>({kind: 'loading'});
    const [connectStarted, setConnectStarted] = useState(false);

    // Refs so the websocket listener stays stable while reading fresh values.
    const connectStartedRef = useRef(false);
    const autoSubmittedRef = useRef(false);
    const interactiveRef = useRef(interactive);
    const onAcceptRef = useRef(onAccept);
    const lookupSeqRef = useRef(0);
    interactiveRef.current = interactive;
    onAcceptRef.current = onAccept;

    const refreshLookup = useCallback(async (): Promise<ServerLookup | null> => {
        if (!server) {
            return null;
        }
        const seq = ++lookupSeqRef.current;
        const next = await lookupServer(server);

        // A newer lookup (e.g. after a connection event) supersedes this one.
        if (seq !== lookupSeqRef.current) {
            return null;
        }
        setLookup(next);
        return next;
    }, [server]);

    useEffect(() => {
        if (interactive) {
            refreshLookup();
        }
    }, [interactive, refreshLookup]);

    useMCPConnectionEvents(useCallback(() => {
        if (!interactiveRef.current) {
            return;
        }
        refreshLookup().then((next) => {
            const authenticated = next?.kind === 'found' && next.authenticated;
            if (!authenticated || !connectStartedRef.current || autoSubmittedRef.current || !interactiveRef.current) {
                return;
            }
            autoSubmittedRef.current = true;
            onAcceptRef.current?.();
        });
    }, [refreshLookup]));

    const authenticated = lookup.kind === 'found' && lookup.authenticated;
    const authURL = lookup.kind === 'found' ? lookup.authURL : undefined; // eslint-disable-line no-undefined

    const handleConnect = () => {
        if (!authURL) {
            return;
        }
        connectStartedRef.current = true;
        setConnectStarted(true);
        window.open(authURL, '_blank', 'noopener,noreferrer');
    };

    const handleContinue = () => {
        autoSubmittedRef.current = true;
        onAccept?.();
    };

    // Continue is the manual path whenever Connect cannot finish the flow on
    // its own: already connected, connect started, or no usable link.
    const showConnect = !authenticated;
    const showContinue = authenticated || connectStarted || lookup.kind === 'missing' || lookup.kind === 'error';

    const renderTitle = () => {
        if (!server) {
            return (
                <FormattedMessage
                    id='ai.connect_mcp.title_unknown'
                    defaultMessage='Connect a tool provider'
                />
            );
        }
        return (
            <FormattedMessage
                id='ai.connect_mcp.title'
                defaultMessage='Connect <name>{server}</name>'
                values={{
                    server,
                    name: (chunks: React.ReactNode) => (
                        <ServerName
                            key='server'
                            data-testid='connect-mcp-server-name'
                        >
                            {chunks}
                        </ServerName>
                    ),
                }}
            />
        );
    };

    const renderExplanation = () => {
        if (!interactive) {
            return null;
        }
        switch (lookup.kind) {
        case 'loading':
            return (
                <Explanation>
                    <FormattedMessage
                        id='ai.connect_mcp.explanation'
                        defaultMessage='The agent needs {server} to continue with your request, but your account is not connected to it yet.'
                        values={{server}}
                    />
                </Explanation>
            );
        case 'error':
            return (
                <Explanation data-testid='connect-mcp-status-error'>
                    <FormattedMessage
                        id='ai.connect_mcp.status_error'
                        defaultMessage='Could not check your connection to {server}. You can continue if you have already connected it, or skip.'
                        values={{server}}
                    />
                </Explanation>
            );
        case 'missing':
            return (
                <Explanation>
                    <FormattedMessage
                        id='ai.connect_mcp.no_auth_url'
                        defaultMessage='A connection link for {server} is not available. Connect it from the tools menu, then continue.'
                        values={{server}}
                    />
                </Explanation>
            );
        case 'found':
            if (lookup.authenticated) {
                return (
                    <Explanation>
                        <FormattedMessage
                            id='ai.connect_mcp.already_connected'
                            defaultMessage='{server} is connected. Continue so the agent can use it for your request.'
                            values={{server}}
                        />
                    </Explanation>
                );
            }
            return (
                <Explanation>
                    {connectStarted ? (
                        <FormattedMessage
                            id='ai.connect_mcp.in_progress'
                            defaultMessage='Finish connecting {server} in the new window. The agent continues automatically once you are connected.'
                            values={{server}}
                        />
                    ) : (
                        <FormattedMessage
                            id='ai.connect_mcp.explanation'
                            defaultMessage='The agent needs {server} to continue with your request, but your account is not connected to it yet.'
                            values={{server}}
                        />
                    )}
                </Explanation>
            );
        default: {
            const unreachable: never = lookup;
            return unreachable;
        }
        }
    };

    const renderStatus = () => {
        switch (state) {
        case 'interactive':
        case 'unexpected':
            return null;
        case 'submitting':
            return (
                <InteractionStatusLine data-testid='connect-mcp-submitting'>
                    <Spinner
                        size='12'
                        aria-hidden={true}
                    />
                    <FormattedMessage
                        id='ai.connect_mcp.submitting'
                        defaultMessage='Submitting…'
                    />
                </InteractionStatusLine>
            );
        case 'waiting':
            return (
                <InteractionStatusLine data-testid='connect-mcp-waiting'>
                    {server ? (
                        <FormattedMessage
                            id='ai.connect_mcp.waiting_for_requester'
                            defaultMessage='Waiting for the requester to connect {server}'
                            values={{server}}
                        />
                    ) : (
                        <FormattedMessage
                            id='ai.connect_mcp.waiting_for_requester_unknown'
                            defaultMessage='Waiting for the requester to connect a tool provider'
                        />
                    )}
                </InteractionStatusLine>
            );
        case 'connected':
            return (
                <InteractionStatusLine data-testid='connect-mcp-connected'>
                    <ResolvedIcon size={16}/>
                    <FormattedMessage
                        id='ai.connect_mcp.connected'
                        defaultMessage='Connected'
                    />
                </InteractionStatusLine>
            );
        case 'skipped':
            return (
                <InteractionStatusLine data-testid='connect-mcp-skipped'>
                    <SkippedIcon size={16}/>
                    <FormattedMessage
                        id='ai.connect_mcp.skipped'
                        defaultMessage='Skipped'
                    />
                </InteractionStatusLine>
            );
        case 'failed':
            return (
                <InteractionStatusLine data-testid='connect-mcp-failed'>
                    <FailedIcon size={16}/>
                    <FormattedMessage
                        id='ai.connect_mcp.failed'
                        defaultMessage='Could not connect'
                    />
                </InteractionStatusLine>
            );
        default: {
            const unreachable: never = state;
            return unreachable;
        }
        }
    };

    return (
        <InteractionCard data-testid='connect-mcp-card'>
            <InteractionTitle>{renderTitle()}</InteractionTitle>
            {renderExplanation()}
            {interactive && (
                <InteractionFooter>
                    <InteractionFooterButtons>
                        <Button
                            emphasis='tertiary'
                            size='small'
                            onClick={onSkip}
                            data-testid='connect-mcp-skip-button'
                        >
                            <FormattedMessage
                                id='ai.connect_mcp.skip'
                                defaultMessage='Skip'
                            />
                        </Button>
                        {showConnect && (
                            <Button
                                emphasis={showContinue ? 'secondary' : 'primary'}
                                size='small'
                                disabled={!authURL}
                                onClick={handleConnect}
                                data-testid='connect-mcp-connect-button'
                            >
                                <FormattedMessage
                                    id='ai.connect_mcp.connect'
                                    defaultMessage='Connect'
                                />
                            </Button>
                        )}
                        {showContinue && (
                            <Button
                                emphasis='primary'
                                size='small'
                                onClick={handleContinue}
                                data-testid='connect-mcp-continue-button'
                            >
                                <FormattedMessage
                                    id='ai.connect_mcp.continue'
                                    defaultMessage='Continue'
                                />
                            </Button>
                        )}
                    </InteractionFooterButtons>
                </InteractionFooter>
            )}
            {renderStatus()}
        </InteractionCard>
    );
};

export default ConnectCard;
