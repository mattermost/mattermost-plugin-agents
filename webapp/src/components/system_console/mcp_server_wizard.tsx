// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Button} from '@mattermost/compass-ui/components/button';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

import {getMCPServerTools, getPluginConfig} from '../../client';
import {useIsLicensedFor} from '@/license';
import ConsolePolicySection from '../access_control/console_policy_section';
import {AnimatedModalShell, MODAL_SHEET_CLASS} from '../animated_modal_shell';
import {useMCPConnectionEvents} from '../../hooks/use_mcp_connection_events';

import {LicenseChip} from './enterprise_chip';
import {BooleanItem, TextItem, ItemLabel} from './item';
import {HeaderMapEditor} from './mcp_header_map_editor';
import MCPServerDescription from './mcp_server_description';
import MCPServerToolRow from './mcp_server_tool_row';
import {MCPServerConfig, MCPServerInfo, MCPToolConfig, MCPToolInfo, PersistMCPServerScope} from './mcp_types';

// persistServer saves server within scope and returns it as saved.
export type PersistMCPServer = (server: MCPServerConfig, scope?: PersistMCPServerScope) => Promise<MCPServerConfig>;

export type MCPServerWizardMode =
    | {kind: 'add'}
    | {kind: 'edit'; server: MCPServerConfig}
    | {kind: 'review'; server: MCPServerConfig};

// connectionSettings is what determines how the server is reached and as
// whom; auto-fill connects with the saved settings, so it waits for these to
// be saved.
export const connectionSettings = (server: MCPServerConfig) => JSON.stringify([
    server.baseURL || '',
    server.headers || {},
    server.clientID || '',
    server.clientSecret || '',
]);

// serverOrigin returns a server URL's origin (scheme, host, and port), or ''
// when the URL cannot be parsed.
const serverOrigin = (url: string): string => {
    try {
        const origin = new URL(url).origin;
        return origin === 'null' ? '' : origin.toLowerCase();
    } catch {
        return '';
    }
};

// originChanged mirrors the server's rule for removing saved sign-ins: a URL
// with another scheme, host, or port is another server; a path change is not.
export const originChanged = (before: string, after: string): boolean => {
    const a = serverOrigin(before);
    const b = serverOrigin(after);
    if (!a || !b) {
        return before !== after;
    }
    return a !== b;
};

// A ReauthReason is an edit after which users' existing sign-ins to the server
// stop working:
//   - name: OAuth grants are stored under the server's name; saving a rename
//     removes them.
//   - url: saving a move to another origin removes the server's grants, which
//     were issued for the old server. A path-only change keeps them.
//   - clientID: refreshes must use the client a grant was issued to, so a
//     grant from another client cannot be renewed once its token expires.
// The client secret, headers, and service account headers only reconnect.
export type ReauthReason = 'name' | 'url' | 'clientID';

// after is the server exactly as it will be saved. Values are compared as the
// server compares them, untrimmed: a stored name with stray spaces that the
// save trims still moves its sign-ins.
export const reauthReasons = (before: MCPServerConfig, after: MCPServerConfig): ReauthReason[] => {
    const reasons: ReauthReason[] = [];
    if (before.name !== after.name) {
        reasons.push('name');
    }
    if (originChanged(before.baseURL, after.baseURL)) {
        reasons.push('url');
    }
    if ((before.clientID ?? '') !== (after.clientID ?? '')) {
        reasons.push('clientID');
    }
    return reasons;
};

type Props = {
    mode: MCPServerWizardMode;

    // Names of the other configured servers, which a new server must not reuse.
    existingNames: string[];
    onPersist: PersistMCPServer;
    onClose: () => void;
};

type Step = 'loading' | 'details' | 'skip' | 'reauth' | 'connect' | 'review';

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

const initialStep = (mode: MCPServerWizardMode): Step => {
    switch (mode.kind) {
    case 'add':
        return 'details';
    case 'edit':
        return 'loading';
    default:
        return 'connect';
    }
};

const MCPServerWizard = ({mode, existingNames, onPersist, onClose}: Props) => {
    const intl = useIntl();
    const remoteMcpLicensed = useIsLicensedFor('remote_mcp');
    const serviceAccountLicensed = useIsLicensedFor('mcp_service_account');
    const [step, setStep] = useState<Step>(initialStep(mode));
    const [details, setDetails] = useState<MCPServerConfig>(mode.kind === 'add' ? emptyServer : mode.server);
    const [savedServer, setSavedServer] = useState<MCPServerConfig | null>(mode.kind === 'add' ? null : mode.server);
    const [loadError, setLoadError] = useState<string | null>(null);
    const [connection, setConnection] = useState<Connection>({status: 'connecting'});
    const [discovered, setDiscovered] = useState<MCPServerInfo | null>(null);
    const [review, setReview] = useState<MCPServerConfig | null>(null);
    const [showErrors, setShowErrors] = useState(false);

    // Set once an edit has ended sign-ins, until the admin reconnects.
    const [reconnecting, setReconnecting] = useState(false);
    const [saving, setSaving] = useState(false);
    const [saveError, setSaveError] = useState<string | null>(null);

    // Closing while a save is in flight would leave its outcome unseen.
    const requestClose = () => {
        if (!saving) {
            onClose();
        }
    };

    // Responses from a superseded discovery are ignored.
    const discoveryRef = useRef(0);

    const discover = useCallback(async (server: MCPServerConfig) => {
        if (!server.id) {
            return;
        }
        const request = ++discoveryRef.current;
        setConnection({status: 'connecting'});
        try {
            // The review starts from the server as it is saved now, not this
            // page's copy, so tool policies another admin saved since the page
            // loaded are not overwritten.
            const [info, pluginConfig]: [MCPServerInfo, any] = await Promise.all([getMCPServerTools(server.id), getPluginConfig()]);
            if (request !== discoveryRef.current) {
                return;
            }
            const stored: MCPServerConfig | undefined = pluginConfig?.mcp?.servers?.find((s: MCPServerConfig) => s.id === server.id);
            if (!stored) {
                setConnection({status: 'error', message: intl.formatMessage({defaultMessage: 'The server is no longer configured. Reload the page.'})});
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
                setReview({...stored, tool_configs: reviewToolConfigs(stored, info.tools)});
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

        // An edit starts from the server as it is saved now, not this page's
        // copy, and saves only what the admin changed on top of it.
        if (mode.kind === 'edit') {
            let cancelled = false;
            getPluginConfig().then((pluginConfig: any) => {
                if (cancelled) {
                    return;
                }
                const stored: MCPServerConfig | undefined = pluginConfig?.mcp?.servers?.find((s: MCPServerConfig) => s.id === mode.server.id);
                if (!stored) {
                    setLoadError(intl.formatMessage({defaultMessage: 'The server is no longer configured. Reload the page.'}));
                    return;
                }
                setSavedServer(stored);
                setDetails(stored);
                setStep('details');
            }).catch(() => {
                if (!cancelled) {
                    setLoadError(intl.formatMessage({defaultMessage: 'Could not load the server’s settings.'}));
                }
            });
            return () => {
                cancelled = true;
            };
        }
        return undefined; // eslint-disable-line no-undefined

        // Only the server the dialog opened with is loaded on mount.
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

    // editedServer is the server as the details step has it, without its tool
    // policies, which a settings save keeps as saved. A server moved to
    // another origin drops the old server's tool policies; they are reviewed again.
    const editedServer = (): MCPServerConfig => {
        const {tool_configs: toolConfigs, ...settings} = {
            ...(savedServer ?? emptyServer),
            ...details,
            name: trimmedName,
            baseURL: trimmedURL,
            clientID: (details.clientID ?? '').trim(),
        };
        if (savedServer && originChanged(savedServer.baseURL, trimmedURL)) {
            return {...settings, tool_configs: []};
        }
        return mode.kind === 'add' && toolConfigs ? {...settings, tool_configs: toolConfigs} : settings;
    };

    const saveDetails = async (): Promise<MCPServerConfig | null> => {
        const server: MCPServerConfig = mode.kind === 'add' ? {...editedServer(), enabled: true} : editedServer();
        setSaving(true);
        setSaveError(null);
        try {
            const saved = await onPersist(server, mode.kind === 'add' ? 'server' : 'settings');
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

    const reasons = savedServer && mode.kind === 'edit' ? reauthReasons(savedServer, editedServer()) : [];

    // An edit that ends users' sign-ins is confirmed first, then the admin
    // signs in again and reviews the tools; any other edit just saves.
    const handleEditSave = async () => {
        setShowErrors(true);
        if (!detailsValid) {
            return;
        }
        if (reasons.length > 0) {
            setStep('reauth');
            return;
        }
        const saved = await saveDetails();
        if (!saved) {
            return;
        }

        // A fix made after a failed reconnect still has to connect and review.
        if (reconnecting && saved.enabled) {
            setStep('connect');
            discover(saved);
            return;
        }
        onClose();
    };

    const handleReauthConfirm = async () => {
        const saved = await saveDetails();
        if (!saved) {
            return;
        }
        if (!saved.enabled) {
            // A disabled server is not connected to; it is reviewed once enabled.
            onClose();
            return;
        }
        setReconnecting(true);
        setStep('connect');
        discover(saved);
    };

    const handleAccept = async () => {
        if (!review || !savedServer) {
            return;
        }
        setSaving(true);
        setSaveError(null);
        try {
            await onPersist({...savedServer, tool_configs: review.tool_configs}, 'tool_configs');
            onClose();
        } catch (err) {
            setSaveError(errorMessage(err, intl.formatMessage({defaultMessage: 'Failed to save the tool policies.'})));
        } finally {
            setSaving(false);
        }
    };

    const serverName = savedServer?.name || trimmedName;
    const title = (() => {
        switch (mode.kind) {
        case 'add':
            return <FormattedMessage defaultMessage='Add remote MCP server'/>;
        case 'edit':
            return (
                <FormattedMessage
                    defaultMessage='Edit {serverName}'
                    values={{serverName: savedServer?.name || mode.server.name}}
                />
            );
        default:
            return (
                <FormattedMessage
                    defaultMessage='Review tools for {serverName}'
                    values={{serverName}}
                />
            );
        }
    })();

    const steps: Array<{key: Step; label: string}> = [
        {key: 'details', label: intl.formatMessage({defaultMessage: 'Server details'})},
        {key: 'connect', label: intl.formatMessage({defaultMessage: 'Connect'})},
        {key: 'review', label: intl.formatMessage({defaultMessage: 'Review tools'})},
    ];
    const currentStepIndex = steps.findIndex((s) => s.key === (step === 'skip' ? 'details' : step));

    // Auto-fill connects to the server with its saved settings, so it waits
    // until the server is saved and its connection settings are unchanged.
    const connectionSaved = Boolean(savedServer) && connectionSettings(savedServer as MCPServerConfig) === connectionSettings(details);

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
            {mode.kind === 'edit' && (
                <BooleanItem
                    label={intl.formatMessage({defaultMessage: 'Enable server'})}
                    value={details.enabled}
                    disableTrue={!remoteMcpLicensed}
                    extra={!remoteMcpLicensed && (
                        <LicenseChip capability='remote_mcp'/>
                    )}
                    onChange={(enabled) => setDetails({...details, enabled})}
                    helpText={intl.formatMessage({defaultMessage: 'Disabled servers keep their settings, but no agent can use their tools.'})}
                />
            )}
            <MCPServerDescription
                serverID={savedServer?.id}
                description={details.description ?? ''}
                connectionSaved={connectionSaved}
                onChange={(description) => setDetails({...details, description})}
                onSuggestion={(description) => {
                    // The suggestion came from the saved connection settings; it
                    // is dropped if they were edited meanwhile.
                    const requestedWith = connectionSettings(details);
                    setDetails((current) => (connectionSettings(current) === requestedWith ? {...current, description} : current));
                }}
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
            <FieldGroup>
                <FieldTitleRow>
                    <ItemLabel as='div'>
                        <FormattedMessage defaultMessage='Service Account Authentication'/>
                    </ItemLabel>
                    {!serviceAccountLicensed && (
                        <LicenseChip capability='mcp_service_account'/>
                    )}
                </FieldTitleRow>
                <FieldHelp>
                    <FormattedMessage defaultMessage='Sent only when an agent uses service account authentication. Put the header name and value in separate fields — for example name Authorization and value Bearer token or Basic credentials, or a custom name like X-API-KEY. Do not repeat the header name in the value. Agents using service accounts can only access servers with at least one header configured here.'/>
                </FieldHelp>
                <HeaderMapEditor
                    headers={details.serviceAccountHeaders ?? {}}
                    onChange={(serviceAccountHeaders) => setDetails({...details, serviceAccountHeaders})}
                    namePlaceholder={intl.formatMessage({defaultMessage: 'Header name (e.g. Authorization)'})}
                    valuePlaceholder={intl.formatMessage({defaultMessage: 'Header value (e.g. Bearer token)'})}
                    disableAdd={!serviceAccountLicensed}
                    disableEdit={!serviceAccountLicensed}
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
            {mode.kind === 'edit' && savedServer?.id && (
                <ConsolePolicySection
                    resourceType='mcp'
                    resourceId={savedServer.id}
                    resourceDisplayName={savedServer.name}
                />
            )}
        </StepBody>
    );

    const reasonText: Record<ReauthReason, string> = {
        name: intl.formatMessage({defaultMessage: 'Renaming the server: sign-ins are saved under its name and are removed, so every user has to sign in again.'}),
        url: intl.formatMessage({defaultMessage: 'Moving the server to another host: sign-ins issued for the old server are removed, so every user has to sign in again. Its tool policies are cleared and reviewed again.'}),
        clientID: intl.formatMessage({defaultMessage: 'Changing the OAuth client ID: sign-ins issued to the old client cannot be renewed, so every user has to sign in again.'}),
    };

    const renderReauth = () => (
        <StepBody>
            <SectionNotice
                type='warning'
                title={(
                    <FormattedMessage
                        defaultMessage='Users will have to sign in to {serverName} again'
                        values={{serverName: trimmedName}}
                    />
                )}
                description={(
                    <ReasonList>
                        {reasons.map((reason) => (
                            <li key={reason}>{reasonText[reason]}</li>
                        ))}
                    </ReasonList>
                )}
            />
            <ReviewIntro>
                <FormattedMessage defaultMessage='After saving, you sign in to the server to check that it works, then review its tools.'/>
            </ReviewIntro>
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
                    secondaryButtonLabel={mode.kind === 'review' ? null : <FormattedMessage defaultMessage='Edit details'/>}
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
        case 'loading':
            return (
                <Button
                    emphasis='tertiary'
                    onClick={requestClose}
                >
                    <FormattedMessage defaultMessage='Cancel'/>
                </Button>
            );
        case 'details':
            if (mode.kind === 'edit') {
                return (
                    <>
                        <Button
                            emphasis='tertiary'
                            onClick={requestClose}
                            disabled={saving}
                        >
                            <FormattedMessage defaultMessage='Cancel'/>
                        </Button>
                        <Button
                            emphasis='primary'
                            onClick={handleEditSave}
                            disabled={saving}
                        >
                            <FormattedMessage defaultMessage='Save'/>
                        </Button>
                    </>
                );
            }
            return (
                <>
                    <Button
                        emphasis='tertiary'
                        onClick={requestClose}
                        disabled={saving}
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
                        disabled={saving}
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
        case 'reauth':
            return (
                <>
                    <Button
                        emphasis='tertiary'
                        onClick={() => setStep('details')}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Back'/>
                    </Button>
                    <Button
                        emphasis='primary'
                        destructive={true}
                        onClick={handleReauthConfirm}
                        disabled={saving}
                    >
                        <FormattedMessage defaultMessage='Save and sign in again'/>
                    </Button>
                </>
            );
        case 'connect':
            return (
                <Button
                    emphasis='tertiary'
                    onClick={requestClose}
                    disabled={saving}
                >
                    <FormattedMessage defaultMessage='Finish later'/>
                </Button>
            );
        case 'review':
            return (
                <>
                    <Button
                        emphasis='tertiary'
                        onClick={requestClose}
                        disabled={saving}
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
            onBackdropClick={requestClose}
            zIndex={2000}
        >
            <Sheet className={MODAL_SHEET_CLASS}>
                <Modal
                    size='large'
                    title={title}
                    onClose={requestClose}
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
                        {loadError && (
                            <SectionNotice
                                type='danger'
                                title={loadError}
                            />
                        )}
                        {step === 'loading' && !loadError && (
                            <ConnectingRow role='status'>
                                <Spinner
                                    size='24'
                                    aria-hidden={true}
                                />
                                <FormattedMessage defaultMessage='Loading the server’s settings…'/>
                            </ConnectingRow>
                        )}
                        {step === 'details' && renderDetails()}
                        {step === 'skip' && renderSkip()}
                        {step === 'reauth' && renderReauth()}
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

const FieldTitleRow = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
`;

const ReasonList = styled.ul`
    margin: 0;
    padding-left: var(--spacing-l);
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
