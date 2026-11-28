// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import {AdminPanel} from '@mattermost/compass-ui/components/admin-panel';

import {useIsLicensedFor} from '@/license';

import {BooleanItem, ItemList, SelectionItem, TextItem} from '../item';
import {LicenseChip} from '../enterprise_chip';

export type WebSearchBraveConfig = {
    apiKey: string;
    resultLimit: number;
    apiURL: string;
};

export type WebSearchSearXNGConfig = {
    baseURL: string;
    resultLimit: number;
};

export type WebSearchSerplyConfig = {
    apiKey: string;
    resultLimit: number;
    apiURL: string;
};

export type WebSearchConfig = {
    enabled: boolean;
    provider: string;
    brave: WebSearchBraveConfig;
    searxng: WebSearchSearXNGConfig;
    serply: WebSearchSerplyConfig;
    domainDenylist: string[] | null; // server sends nil Go slice as JSON null
};

type Props = {
    value: WebSearchConfig;
    onChange: (config: WebSearchConfig) => void;
};

const DEFAULT_BRAVE_CONFIG = {apiKey: '', resultLimit: 5, apiURL: ''};
const DEFAULT_SEARXNG_CONFIG = {baseURL: '', resultLimit: 5};
const DEFAULT_SERPLY_CONFIG = {apiKey: '', resultLimit: 5, apiURL: ''};

const WebSearchPanel = ({value, onChange}: Props) => {
    const intl = useIntl();
    const sovereignSearchLicensed = useIsLicensedFor('sovereign_web_search');

    // Provide defaults for missing config objects
    const brave = value.brave || DEFAULT_BRAVE_CONFIG;
    const searxng = value.searxng || DEFAULT_SEARXNG_CONFIG;
    const serply = value.serply || DEFAULT_SERPLY_CONFIG;
    const domainDenylist = value.domainDenylist || [];

    const handleUpdate = (patch: Partial<WebSearchConfig>) => {
        onChange({...value, ...patch});
    };

    const handleBraveUpdate = (patch: Partial<WebSearchBraveConfig>) => {
        handleUpdate({brave: {...brave, ...patch}});
    };

    const handleSearxngUpdate = (patch: Partial<WebSearchSearXNGConfig>) => {
        handleUpdate({searxng: {...searxng, ...patch}});
    };

    const handleSerplyUpdate = (patch: Partial<WebSearchSerplyConfig>) => {
        handleUpdate({serply: {...serply, ...patch}});
    };

    return (
        <AdminPanel
            title={<FormattedMessage defaultMessage='Web Search'/>}
            subtitle={intl.formatMessage({defaultMessage: 'Built-in web search for agents without provider-native search. Agents with native web search enabled use that instead.'})}
        >
            <ItemList>
                <BooleanItem
                    label={intl.formatMessage({defaultMessage: 'Enable Web Search'})}
                    value={value.enabled}
                    disableTrue={!sovereignSearchLicensed}
                    extra={!sovereignSearchLicensed && (
                        <LicenseChip capability='sovereign_web_search'/>
                    )}
                    onChange={(enabled) => handleUpdate({enabled})}
                    helpText={intl.formatMessage({defaultMessage: 'Lets agents use Mattermost\'s built-in web search tool. Leave off if your LLM provides native web search.'})}
                />
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'Provider'})}
                    value={value.provider}
                    onChange={(provider) => handleUpdate({provider})}
                    disabled={!value.enabled}
                    error={value.provider === 'google' ? intl.formatMessage({defaultMessage: 'Google Custom Search is no longer supported. Choose another provider.'}) : ''}
                    options={[
                        {value: 'brave', label: 'Brave Search'},
                        {value: 'searxng', label: intl.formatMessage({defaultMessage: 'SearXNG (self-hosted)'})},
                        {value: 'serply', label: 'Serply'},
                    ]}
                />
                {value.provider === 'brave' && (
                    <>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Brave API Key'})}
                            type='password'
                            value={brave.apiKey}
                            onChange={(e) => handleBraveUpdate({apiKey: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: "Requires Brave's Pro AI plan."})}
                            disabled={!value.enabled}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Brave Result Limit'})}
                            type='number'
                            value={brave.resultLimit.toString()}
                            onChange={(e) => {
                                const parsed = parseInt(e.target.value, 10);
                                handleBraveUpdate({resultLimit: Number.isNaN(parsed) ? 5 : parsed});
                            }}
                            disabled={!value.enabled}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Brave API URL (optional)'})}
                            value={brave.apiURL}
                            onChange={(e) => handleBraveUpdate({apiURL: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'Override the default Brave Search endpoint.'})}
                            disabled={!value.enabled}
                        />
                    </>
                )}
                {value.provider === 'searxng' && (
                    <>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'SearXNG Base URL'})}
                            value={searxng.baseURL}
                            onChange={(e) => handleSearxngUpdate({baseURL: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'SearXNG root URL (e.g., https://searx.example.com). Must enable json in search.formats. Add private addresses to AllowedUntrustedInternalConnections.'})}
                            disabled={!value.enabled}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'SearXNG Result Limit'})}
                            type='number'
                            value={searxng.resultLimit.toString()}
                            onChange={(e) => {
                                const parsed = parseInt(e.target.value, 10);
                                handleSearxngUpdate({resultLimit: Number.isNaN(parsed) ? 5 : parsed});
                            }}
                            disabled={!value.enabled}
                        />
                    </>
                )}
                {value.provider === 'serply' && (
                    <>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Serply API Key'})}
                            type='password'
                            value={serply.apiKey}
                            onChange={(e) => handleSerplyUpdate({apiKey: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'Serply API key, sent in the X-Api-Key header. Serply returns Google web results.'})}
                            disabled={!value.enabled}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Serply Result Limit'})}
                            type='number'
                            value={serply.resultLimit.toString()}
                            onChange={(e) => {
                                const parsed = parseInt(e.target.value, 10);
                                handleSerplyUpdate({resultLimit: Number.isNaN(parsed) ? 5 : parsed});
                            }}
                            disabled={!value.enabled}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Serply API URL (optional)'})}
                            value={serply.apiURL}
                            onChange={(e) => handleSerplyUpdate({apiURL: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'Override the default Serply search endpoint if necessary.'})}
                            disabled={!value.enabled}
                        />
                    </>
                )}
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Domain Denylist (optional)'})}
                    value={domainDenylist.join(', ')}
                    onChange={(e) => {
                        const domains = e.target.value.split(',').map((d) => d.trim()).filter((d) => d !== '');
                        handleUpdate({domainDenylist: domains});
                    }}
                    helptext={intl.formatMessage({defaultMessage: 'Comma-separated domains excluded from search results before the LLM sees them (e.g., example.com, spam-site.org).'})}
                    disabled={!value.enabled}
                />
            </ItemList>
        </AdminPanel>
    );
};

export default WebSearchPanel;
