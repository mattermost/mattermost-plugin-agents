// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {getMCPServerTools} from '../../client';
import {AnimatedModalShell, MODAL_SHEET_CLASS} from '../animated_modal_shell';
import {useMCPConnectionEvents} from '../../hooks/use_mcp_connection_events';

import {TextItem, ItemLabel} from './item';
import {HeaderMapEditor} from './mcp_header_map_editor';
import MCPServerToolRow from './mcp_server_tool_row';
import {MCPServerConfig, MCPServerInfo, MCPToolConfig, MCPToolInfo} from './mcp_types';

// persistServer saves server and returns it as saved. With toolConfigsOnly,
// only the server's tool_configs are written to the saved server.
export type PersistMCPServer = (server: MCPServerConfig, toolConfigsOnly?: boolean) => Promise<MCPServerConfig>;

export type MCPServerWizardMode =
    | {kind: 'add'}
    | {kind: 'review'; server: MCPServerConfig};

type Props = {
    mode: MCPServerWizardMode;

    // Names of the other configured servers, which a new server must not reuse.
    existingNames: string[];
    onPersist: PersistMCPServer;
    onClose: () => void;
};

type Step = 'details' | 'skip' | 'connect' | 'review';

type Connection =
    | {status: 'connecting'}
    | {status: 'oauth'; oauthURL: string}
    | {status: 'error'; message: string};

const emptyServer: MCPServerConfig = {
    name: '',
    enabled: true,
    baseURL: '',
    headers: {},
    description: '',
    clientID: '',
    clientSecret: '',
};

// reviewToolConfigs gives every discovered tool an explicit policy: the one an
// admin already chose, else the server-computed suggestion. Stored entries for
// tools the server no longer lists are kept.
export const reviewToolConfigs = (server: MCPServerConfig, tools: MCPToolInfo[]): MCPToolConfig[] => {
    const stored = server.tool_configs ?? [];
    const discovered = new Set(tools.map((tool) => tool.name));
    const reviewed = tools.map((tool): MCPToolConfig => {
        const existing = stored.find((tc) => tc.name === tool.name);
        if (existing?.policy) {
            return existing;
        }
        return {
            ...existing,
            name: tool.name,
            policy: tool.suggestedPolicy || tool.defaultPolicy || 'ask',
            enabled: existing?.enabled ?? true,
        };
    });
    return [...reviewed, ...stored.filter((tc) => !discovered.has(tc.name))];
};

const errorMessage = (err: unknown, fallback: string) => (err instanceof Error && err.message ? err.message : fallback);

const MCPServerWizard = ({mode, existingNames, onPersist, onClose}: Props) => {
    const intl = useIntl();
    const [step, setStep] = useState<Step>(mode.kind === 'add' ? 'details' : 'connect');
    const [details, setDetails] = useState<MCPServerConfig>(mode.kind === 'add' ? emptyServer : mode.server);
    const [savedServer, setSavedServer] = useState<MCPServerConfig | null>(mode.kind === 'review' ? mode.server : null);
    const [connection, setConnection] = useState<Connection>({status: 'connecting'});
    const [discovered, setDiscovered] = useState<MCPServerInfo | null>(null);
    const [review, setReview] = useState<MCPServerConfig | null>(null);
    const [showErrors, setShowErrors] = useState(false);
    const [saving, setSaving] = useState(false);
    const [saveError, setSaveError] = useState<string | null>(null);

    // Responses from a superseded discovery are ignored.
    const discoveryRef = useRef(0);

    const discover = useCallback(async (server: MCPServerConfig) => {
        if (!server.id) {
            return;
        }
        const request = ++discoveryRef.current;
        setConnection({status: 'connecting'});
        try {
            const info: MCPServerInfo = await getMCPServerTools(server.id);
            if (request !== discoveryRef.current) {
                return;
            }
            if (info.error) {
                setConnection({status: 'error', message: info.error});
            } else if (info.needsOAuth && info.oauthURL) {
                setConnection({status: 'oauth', oauthURL: info.oauthURL});
            } else if (info.tools.length === 0) {
                setConnection({status: 'error', message: intl.formatMessage({defaultMessage: 'The server did not list any tools.'})});
            } else {
                setDiscovered(info);
                setReview({...server, tool_configs: reviewToolConfigs(server, info.tools)});
                setStep('review');
            }
        } catch (err) {
            if (request === discoveryRef.current) {
                setConnection({status: 'error', message: errorMessage(err, intl.formatMessage({defaultMessage: 'Could not list the server’s tools.'}))});
            }
        }
    }, [intl]);

    useEffect(() => {
        if (mode.kind === 'review') {
            discover(mode.server);
        }

        // Only the server the review opened with is discovered on mount.
    }, []); // eslint-disable-line react-hooks/exhaustive-deps

    useEffect(() => () => {
        discoveryRef.current++;
    }, []);

    // When the admin finishes signing in, the server is listed again.
    useMCPConnectionEvents(useCallback((event) => {
        if (step !== 'connect' || !savedServer || event.status !== 'connected') {
            return;
        }
        if (event.serverName === savedServer.name || event.serverOrigin === savedServer.baseURL) {
            discover(savedServer);
        }
    }, [step, savedServer, discover]));

    const trimmedName = details.name.trim();
    const trimmedURL = details.baseURL.trim();
    const nameError = (() => {
        if (!trimmedName) {
            return intl.formatMessage({defaultMessage: 'Enter a name for the server.'});
        }
        if (existingNames.includes(trimmedName) && trimmedName !== savedServer?.name) {
            return intl.formatMessage({defaultMessage: 'Another server already uses this name.'});
        }
        return '';
    })();
    const urlError = (/^https?:\/\/\S+$/i).test(trimmedURL) ? '' : intl.formatMessage({defaultMessage: 'Enter the server URL, starting with https:// or http://.'});
    const detailsValid = !nameError && !urlError;

    const saveDetails = async (): Promise<MCPServerConfig | null> => {
        const server: MCPServerConfig = {
            ...(savedServer ?? emptyServer),
            ...details,
            name: trimmedName,
            baseURL: trimmedURL,
            enabled: true,
        };
        setSaving(true);
        setSaveError(null);
        try {
            const saved = await onPersist(server);
            setSavedServer(saved);
            setDetails(saved);
            return saved;
        } catch (err) {
            setSaveError(errorMessage(err, intl.formatMessage({defaultMessage: 'Failed to save the server.'})));
            return null;
        } finally {
            setSaving(false);
        }
    };

    const handleConnect = async () => {
        setShowErrors(true);
        if (!detailsValid) {
            return;
        }
        const saved = await saveDetails();
        if (saved) {
            setStep('connect');
            discover(saved);
        }
    };

    const handleSkip = () => {
        setShowErrors(true);
        if (detailsValid) {
            setStep('skip');
        }
    };

    const handleSaveWithoutConnecting = async () => {
        if (await saveDetails()) {
            onClose();
        }
    };

    const handleAccept = async () => {
        if (!review || !savedServer) {
            return;
        }
        setSaving(true);
        setSaveError(null);
        try {
            await onPersist({...savedServer, tool_configs: review.tool_configs}, true);
            onClose();
        } catch (err) {
            setSaveError(errorMessage(err, intl.formatMessage({defaultMessage: 'Failed to save the tool policies.'})));
        } finally {
            setSaving(false);
        }
    };

    const serverName = savedServer?.name || trimmedName;
    const title = mode.kind === 'add' ? (
        <FormattedMessage defaultMessage='Add remote MCP server'/>
    ) : (
        <FormattedMessage
            defaultMessage='Review tools for {serverName}'
            values={{serverName}}
        />
    );

    const steps: Array<{key: Step; label: string}> = [
        {key: 'details', label: intl.formatMessage({defaultMessage: 'Server details'})},
        {key: 'connect', label: intl.formatMessage({defaultMessage: 'Connect'})},
        {key: 'review', label: intl.formatMessage({defaultMessage: 'Review tools'})},
    ];
    const currentStepIndex = steps.findIndex((s) => s.key === (step === 'skip' ? 'details' : step));

    const renderDetails = () => (
        <StepBody>
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Name'})}
                value={details.name}
                onChange={(e) => setDetails({...details, name: e.target.value})}
                error={showErrors ? nameError : undefined} // eslint-disable-line no-undefined
                helptext={intl.formatMessage({defaultMessage: 'Shown to users and agents. Users sign in to the server under this name.'})}
            />
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Server URL'})}
                value={details.baseURL}
                placeholder='https://mcp.example.com/mcp'
                onChange={(e) => setDetails({...details, baseURL: e.target.value})}
                error={showErrors ? urlError : undefined} // eslint-disable-line no-undefined
            />
            <TextItem
                label={intl.formatMessage({defaultMessage: 'Description'})}
                value={details.description ?? ''}
                multiline={true}
                onChange={(e) => setDetails({...details, description: e.target.value})}
                helptext={intl.formatMessage({defaultMessage: 'Optional. Tells agents what the server is for.'})}
            />
            <FieldGroup>
                <ItemLabel as='div'>
                    <FormattedMessage defaultMessage='Headers'/>
                </ItemLabel>
                <FieldHelp>
                    <FormattedMessage defaultMessage='Optional headers sent with every request, such as an API key.'/>
                </FieldHelp>
                <HeaderMapEditor
                    headers={details.headers ?? {}}
                    onChange={(headers) => setDetails({...details, headers})}
                />
            </FieldGroup>
            <TextItem
                label={intl.formatMessage({defaultMessage: 'OAuth client ID'})}
                value={details.clientID ?? ''}
                onChange={(e) => setDetails({...details, clientID: e.target.value})}
                helptext={intl.formatMessage({defaultMessage: 'Optional. Only needed when the server does not support dynamic client registration.'})}
            />
            <TextItem
                label={intl.formatMessage({defaultMessage: 'OAuth client secret'})}
                value={details.clientSecret ?? ''}
                type='password'
                onChange={(e) => setDetails({...details, clientSecret: e.target.value})}
            />
        </StepBody>
    );

    const renderSkip = () => (
        <StepBody>
            <SectionNotice
                type='warning'
                title={<FormattedMessage defaultMessage='Save without reviewing tools?'/>}
                description={(
                    <FormattedMessage
                        defaultMessage='{serverName} will be saved without connecting to it, so its tools are not reviewed. Until you review them, its tools use default policies: they ask for approval before every run, except where Mattermost has curated defaults for known servers. OAuth sign-in is also not tested. You can review the tools later from the Tools tab.'
                        values={{serverName}}
                    />
                )}
            />
        </StepBody>
    );

    const renderConnect = () => {
        switch (connection.status) {
        case 'connecting':
            return (
                <ConnectingRow role='status'>
                    <Spinner
                        size='24'
                        aria-hidden={true}
                    />
                    <FormattedMessage
                        defaultMessage='Connecting to {serverName} and listing its tools…'
                        values={{serverName}}
                    />
                </ConnectingRow>
            );
        case 'oauth':
            return (
                <SectionNotice
                    type='info'
                    title={(
                        <FormattedMessage
                            defaultMessage='Sign in to {serverName}'
                            values={{serverName}}
                        />
                    )}
                    description={<FormattedMessage defaultMessage='The server needs you to sign in before it lists its tools. Signing in here checks that OAuth works; it connects only your account, and each user signs in separately. This dialog continues when sign-in finishes.'/>}
                    primaryButtonLabel={<FormattedMessage defaultMessage='Sign in'/>}
                    onPrimaryAction={() => window.open(connection.oauthURL, '_blank', 'noopener,noreferrer')}
                    secondaryButtonLabel={<FormattedMessage defaultMessage='Check again'/>}
                    onSecondaryAction={() => savedServer && discover(savedServer)}
                />
            );
        case 'error':
            return (
                <SectionNotice
                    type='danger'
                    title={(
                        <FormattedMessage
                            defaultMessage='Could not connect to {serverName}'
                            values={{serverName}}
                        />
                    )}
                    description={connection.message}
                    primaryButtonLabel={<FormattedMessage defaultMessage='Try again'/>}
                    onPrimaryAction={() => savedServer && discover(savedServer)}
                    secondaryButtonLabel={mode.kind === 'add' ? <FormattedMessage defaultMessage='Edit details'/> : null}
                    onSecondaryAction={() => setStep('details')}
                />
            );
        default:
            return null;
        }
    };

    const renderReview = () => discovered && review && (
        <StepBody>
            <ReviewIntro>
                <FormattedMessage defaultMessage='Choose how each tool is approved. Policies are pre-set from what the server reports about each tool: tools it marks read-only are set to run automatically in DMs, and all other tools ask first. Check them before saving; nothing applies until you save.'/>
            </ReviewIntro>
            <MCPServerToolRow
                server={discovered}
                serverConfig={review}
                onServerConfigChange={setReview}
                reviewing={true}
            />
        </StepBody>
    );

    const footer = (() => {
        switch (step) {
        case 'details':
            return (
                <>
                    <Button
                        emphasis='tertiary'
                        onClick={onClose}
                    >
                        <FormattedMessage defaultMessage='Cancel'/>
                    </Button>
                    <Button
                        emphasis='secondary'
                        onClick={handleSkip}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Skip connecting'/>
                    </Button>
                    <Button
                        emphasis='primary'
                        onClick={handleConnect}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Save and connect'/>
                    </Button>
                </>
            );
        case 'skip':
            return (
                <>
                    <Button
                        emphasis='tertiary'
                        onClick={() => setStep('details')}
                    >
                        <FormattedMessage defaultMessage='Back'/>
                    </Button>
                    <Button
                        emphasis='primary'
                        onClick={handleSaveWithoutConnecting}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Save without connecting'/>
                    </Button>
                </>
            );
        case 'connect':
            return (
                <Button
                    emphasis='tertiary'
                    onClick={onClose}
                >
                    <FormattedMessage defaultMessage='Finish later'/>
                </Button>
            );
        case 'review':
            return (
                <>
                    <Button
                        emphasis='tertiary'
                        onClick={onClose}
                    >
                        <FormattedMessage defaultMessage='Finish later'/>
                    </Button>
                    <Button
                        emphasis='primary'
                        onClick={handleAccept}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Accept and save'/>
                    </Button>
                </>
            );
        default:
            return null;
        }
    })();

    return (
        <AnimatedModalShell
            show={true}
            onBackdropClick={onClose}
            zIndex={2000}
        >
            <Sheet className={MODAL_SHEET_CLASS}>
                <Modal
                    size='large'
                    title={title}
                    onClose={onClose}
                    closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                    footer={footer}
                    headerSlot={mode.kind === 'add' ? (
                        <Stepper aria-label={intl.formatMessage({defaultMessage: 'Steps'})}>
                            {steps.map((s, index) => (
                                <StepItem
                                    key={s.key}
                                    aria-current={index === currentStepIndex ? 'step' : undefined} // eslint-disable-line no-undefined
                                    $state={(() => {
                                        if (index === currentStepIndex) {
                                            return 'current';
                                        }
                                        return index < currentStepIndex ? 'done' : 'todo';
                                    })()}
                                >
                                    <StepNumber>{index + 1}</StepNumber>
                                    {s.label}
                                </StepItem>
                            ))}
                        </Stepper>
                    ) : null}
                >
                    <Body>
                        {saveError && (
                            <SectionNotice
                                type='danger'
                                title={saveError}
                            />
                        )}
                        {step === 'details' && renderDetails()}
                        {step === 'skip' && renderSkip()}
                        {step === 'connect' && renderConnect()}
                        {step === 'review' && renderReview()}
                    </Body>
                </Modal>
            </Sheet>
        </AnimatedModalShell>
    );
};

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - 32px);
    max-height: calc(100vh - 64px);
`;

const Body = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
`;

const StepBody = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
`;

const FieldGroup = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

const FieldHelp = styled.div`
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const ReviewIntro = styled.p`
    margin: 0;
    font-size: var(--font-size-100);
    line-height: var(--line-height-100);
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const ConnectingRow = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-s);
    padding: var(--spacing-l) 0;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const Stepper = styled.ol`
    display: flex;
    gap: var(--spacing-l);
    margin: var(--spacing-s) 0 0;
    padding: 0;
    list-style: none;
`;

const StepItem = styled.li<{$state: 'done' | 'current' | 'todo'}>`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    font-size: var(--font-size-75);
    font-weight: ${(props) => (props.$state === 'current' ? 'var(--font-weight-semibold)' : 'var(--font-weight-regular)')};
    color: ${(props) => (props.$state === 'todo' ? 'rgba(var(--center-channel-color-rgb), 0.56)' : 'var(--center-channel-color)')};
`;

const StepNumber = styled.span`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: var(--spacing-l);
    height: var(--spacing-l);
    border-radius: var(--radius-full);
    background: rgba(var(--center-channel-color-rgb), 0.08);
    font-size: var(--font-size-75);

    li[aria-current='step'] > & {
        background: var(--button-bg);
        color: var(--button-color);
    }
`;

export default MCPServerWizard;
