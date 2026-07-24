// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import Panel from '../panel';
import {BooleanItem, ItemList, SelectionItem, SelectionItemOption, TextItem} from '../item';

export type WebSearchGoogleConfig = {
    apiKey: string;
    searchEngineId: string;
    resultLimit: number;
    apiURL: string;
};

export type WebSearchBraveConfig = {
    apiKey: string;
    resultLimit: number;
    apiURL: string;
};

export type WebSearchSearXNGConfig = {
    baseURL: string;
    resultLimit: number;
};

export type WebSearchConfig = {
    enabled: boolean;
    provider: string;
    google: WebSearchGoogleConfig;
    brave: WebSearchBraveConfig;
    searxng: WebSearchSearXNGConfig;
    domainDenylist: string[] | null; // server sends nil Go slice as JSON null
};

type Props = {
    value: WebSearchConfig;
    onChange: (config: WebSearchConfig) => void;
};

const DEFAULT_GOOGLE_CONFIG = {apiKey: '', searchEngineId: '', resultLimit: 5, apiURL: ''};
const DEFAULT_BRAVE_CONFIG = {apiKey: '', resultLimit: 5, apiURL: ''};
const DEFAULT_SEARXNG_CONFIG = {baseURL: '', resultLimit: 5};

const WebSearchPanel = ({value, onChange}: Props) => {
    const intl = useIntl();

    // Provide defaults for missing config objects
    const google = value.google || DEFAULT_GOOGLE_CONFIG;
    const brave = value.brave || DEFAULT_BRAVE_CONFIG;
    const searxng = value.searxng || DEFAULT_SEARXNG_CONFIG;
    const domainDenylist = value.domainDenylist || [];

    const handleUpdate = (patch: Partial<WebSearchConfig>) => {
        onChange({...value, ...patch});
    };

    const handleGoogleUpdate = (patch: Partial<WebSearchGoogleConfig>) => {
        handleUpdate({google: {...google, ...patch}});
    };

    const handleBraveUpdate = (patch: Partial<WebSearchBraveConfig>) => {
        handleUpdate({brave: {...brave, ...patch}});
    };

    const handleSearxngUpdate = (patch: Partial<WebSearchSearXNGConfig>) => {
        handleUpdate({searxng: {...searxng, ...patch}});
    };

    return (
        <Panel
            title={<FormattedMessage defaultMessage='Web Search'/>}
            subtitle={intl.formatMessage({defaultMessage: 'Configure built-in web search for agents that do not have native web search capabilities. NOTE: If your agent is configured to use native tool web search, that will be used instead of this web search.'})}
        >
            <ItemList>
                <BooleanItem
                    label={intl.formatMessage({defaultMessage: 'Enable Web Search'})}
                    value={value.enabled}
                    onChange={(enabled) => handleUpdate({enabled})}
                    helpText={intl.formatMessage({defaultMessage: 'Allow agents to call Mattermost\'s built-in web search tool. If your LLM already provides native web search support, leave this disabled.'})}
                />
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'Provider'})}
                    value={value.provider}
                    onChange={(e) => handleUpdate({provider: e.target.value})}
                    disabled={!value.enabled}
                >
                    <SelectionItemOption value='google'>{'Google Custom Search'}</SelectionItemOption>
                    <SelectionItemOption value='brave'>{'Brave Search'}</SelectionItemOption>
                    <SelectionItemOption value='searxng'>{'SearXNG (self-hosted)'}</SelectionItemOption>
                </SelectionItem>
                {value.provider === 'google' && (
                    <>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Google API Key'})}
                            type='password'
                            value={google.apiKey}
                            onChange={(e) => handleGoogleUpdate({apiKey: e.target.value})}
                            disabled={!value.enabled}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Search Engine ID'})}
                            value={google.searchEngineId}
                            onChange={(e) => handleGoogleUpdate({searchEngineId: e.target.value})}
                            disabled={!value.enabled}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Result Limit'})}
                            type='number'
                            value={google.resultLimit.toString()}
                            onChange={(e) => {
                                const parsed = parseInt(e.target.value, 10);
                                handleGoogleUpdate({resultLimit: Number.isNaN(parsed) ? 5 : parsed});
                            }}
                            disabled={!value.enabled}
                        />
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'API URL (optional)'})}
                            value={google.apiURL}
                            onChange={(e) => handleGoogleUpdate({apiURL: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: 'Override the default Google Custom Search endpoint if necessary.'})}
                            disabled={!value.enabled}
                        />
                    </>
                )}
                {value.provider === 'brave' && (
                    <>
                        <TextItem
                            label={intl.formatMessage({defaultMessage: 'Brave API Key'})}
                            type='password'
                            value={brave.apiKey}
                            onChange={(e) => handleBraveUpdate({apiKey: e.target.value})}
                            helptext={intl.formatMessage({defaultMessage: "Brave Search API Key. Ensure you subscribe to Brave's Pro AI plan when using this feature. Using Brave's regular Search API (non-AI tier) violates Brave's Terms of Service and may result in account suspension."})}
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
                            helptext={intl.formatMessage({defaultMessage: 'Override the default Brave Search endpoint if necessary.'})}
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
                            helptext={intl.formatMessage({defaultMessage: 'Root URL of your SearXNG instance (e.g. https://searx.example.com). The instance must allow the json format (search.formats in its settings). If the instance is on a private/reserved address, add it to the Mattermost AllowedUntrustedInternalConnections setting.'})}
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
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Domain Denylist (optional)'})}
                    value={domainDenylist.join(', ')}
                    onChange={(e) => {
                        const domains = e.target.value.split(',').map((d) => d.trim()).filter((d) => d !== '');
                        handleUpdate({domainDenylist: domains});
                    }}
                    helptext={intl.formatMessage({defaultMessage: 'Comma-separated list of domains to exclude from search results (e.g., example.com, spam-site.org). Results from these domains will be filtered out and the LLM will never see them.'})}
                    disabled={!value.enabled}
                />
            </ItemList>
        </Panel>
    );
};

export default WebSearchPanel;
