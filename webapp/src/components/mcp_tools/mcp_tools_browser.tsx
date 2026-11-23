// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {SearchInput} from '@mattermost/compass-ui/components/search-input';
import {Spinner} from '@mattermost/compass-ui/components/spinner';

export type MCPToolsBrowserProps = {
    title?: React.ReactNode;
    summary?: React.ReactNode;
    actions?: React.ReactNode;
    beforeSearch?: React.ReactNode;
    searchQuery: string;
    onSearchChange: (value: string) => void;
    showSearch: boolean;
    notices?: React.ReactNode;
    loading?: boolean;
    loadingLabel?: React.ReactNode;
    emptyState?: React.ReactNode;
    noSearchResults?: boolean;
    children?: React.ReactNode;
};

const MCPToolsBrowser = ({
    title,
    summary,
    actions,
    beforeSearch,
    searchQuery,
    onSearchChange,
    showSearch,
    notices,
    loading = false,
    loadingLabel,
    emptyState,
    noSearchResults = false,
    children,
}: MCPToolsBrowserProps) => {
    const intl = useIntl();
    const hasHeader = Boolean(title || summary || actions);

    return (
        <Container>
            {beforeSearch}
            {hasHeader && (
                <Header>
                    <HeaderInfo>
                        {title && <Title>{title}</Title>}
                        {summary && <Summary>{summary}</Summary>}
                    </HeaderInfo>
                    {actions && <ButtonGroup>{actions}</ButtonGroup>}
                </Header>
            )}

            <Content>
                {showSearch && (
                    <SearchInput
                        placeholder={intl.formatMessage({defaultMessage: 'Search servers and tools...'})}
                        value={searchQuery}
                        onChange={(e) => onSearchChange(e.target.value)}
                        onClear={() => onSearchChange('')}
                        clearLabel={intl.formatMessage({defaultMessage: 'Clear search'})}
                    />
                )}

                {notices}

                {loading && (
                    <LoadingState>
                        <Spinner
                            size='24'
                            aria-label={typeof loadingLabel === 'string' ? loadingLabel : intl.formatMessage({defaultMessage: 'Loading tools...'})}
                        />
                        {loadingLabel}
                    </LoadingState>
                )}

                {!loading && emptyState}

                {!loading && !emptyState && noSearchResults && (
                    <EmptyState
                        title={<FormattedMessage defaultMessage='No matching MCP servers or tools'/>}
                        action={{
                            emphasis: 'secondary',
                            onClick: () => onSearchChange(''),
                            children: <FormattedMessage defaultMessage='Clear search'/>,
                        }}
                    />
                )}

                {!loading && !emptyState && !noSearchResults && children && (
                    <ServersList>
                        {children}
                    </ServersList>
                )}
            </Content>
        </Container>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
`;

const Header = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--spacing-l);
`;

const HeaderInfo = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxs);
`;

const Title = styled.h3`
    margin: 0;
    font-size: var(--font-size-300);
    font-weight: var(--font-weight-semibold);
    color: var(--center-channel-color);
`;

const Summary = styled.div`
    font-size: var(--font-size-100);
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    gap: var(--spacing-xxxs);
`;

const ButtonGroup = styled.div`
    display: flex;
    gap: var(--spacing-xs);
    align-items: center;
    flex-shrink: 0;
`;

const Content = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-l);
`;

const LoadingState = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    gap: var(--spacing-xs);
    padding: var(--spacing-xxxl);
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ServersList = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
`;

export default MCPToolsBrowser;
