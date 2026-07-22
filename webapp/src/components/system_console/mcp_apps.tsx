// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {Pill} from '../pill';

import {BooleanItem, ItemList, TextItem} from './item';

export type MCPAppsConfig = {
    enabled: boolean;
    sandboxURL: string;
    sandboxListenAddress: string;
    allowInsecureSameOriginSandbox: boolean;
};

export const defaultMCPAppsConfig: MCPAppsConfig = {
    enabled: false,
    sandboxURL: '',
    sandboxListenAddress: '',
    allowInsecureSameOriginSandbox: false,
};

type Props = {
    value: MCPAppsConfig;
    onChange: (value: MCPAppsConfig) => void;
};

const Horizontal = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
`;

const InsecureWarningBanner = styled.div`
    grid-column: 1 / -1;
    padding: 10px 12px;
    margin-bottom: 4px;
    background: rgba(var(--error-text-color-rgb), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--error-text-color-rgb), 0.16);
    color: var(--error-text);
    font-size: 14px;
`;

const MCPAppsSection = ({value, onChange}: Props) => {
    const intl = useIntl();

    return (
        <ItemList title={intl.formatMessage({defaultMessage: 'MCP Apps (Interactive Tool UIs)'})}>
            <BooleanItem
                label={intl.formatMessage({defaultMessage: 'Enable MCP Apps'})}
                value={value.enabled}
                onChange={(enabled) => onChange({...value, enabled})}
                helpText={intl.formatMessage({defaultMessage: 'Render interactive app UIs delivered by MCP servers (MCP Apps extension) inside tool results. Requires a sandbox origin below, or the insecure fallback.'})}
            />
            {value.enabled && (
                <>
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Sandbox Base URL'})}
                        placeholder='https://mm-apps.example.com'
                        value={value.sandboxURL}
                        onChange={(e) => onChange({...value, sandboxURL: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Externally reachable base URL on a different origin than this Mattermost server that reverse-proxies to the plugin\u2019s sandbox listener. Use a dedicated subdomain or a second port on the same hostname (e.g. https://mm.example.com:8443). Must not be this server\u2019s Site URL origin. Leave empty and enable the insecure fallback below for trials/dev. See the admin guide for proxy examples.'})}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Sandbox Listener Address'})}
                        placeholder=':8066'
                        value={value.sandboxListenAddress}
                        onChange={(e) => onChange({...value, sandboxListenAddress: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'host:port the plugin binds to serve sandbox content for the URL above. Default: :8066. Every node in a cluster binds this address.'})}
                    />
                    <BooleanItem
                        label={
                            <Horizontal>
                                <FormattedMessage defaultMessage='Allow insecure same-origin sandbox'/>
                                <Pill><FormattedMessage defaultMessage='NOT RECOMMENDED'/></Pill>
                            </Horizontal>
                        }
                        value={value.allowInsecureSameOriginSandbox}
                        onChange={(allowInsecureSameOriginSandbox) => onChange({...value, allowInsecureSameOriginSandbox})}
                        helpText={intl.formatMessage({defaultMessage: 'Serve app content from this Mattermost server\u2019s own origin. Requires Sandbox Base URL to be empty (do not point Sandbox Base URL at this server). This removes the browser origin isolation between third-party app content and Mattermost: a malicious app could access your Mattermost session. Only for trials and development. Enabling this is recorded in the server log.'})}
                    />
                    {value.allowInsecureSameOriginSandbox && !value.sandboxURL.trim() && (
                        <InsecureWarningBanner>
                            <FormattedMessage defaultMessage='Warning: MCP app content will run with the same browser origin as Mattermost. Configure a Sandbox Base URL to restore isolation.'/>
                        </InsecureWarningBanner>
                    )}
                </>
            )}
        </ItemList>
    );
};

export default MCPAppsSection;
