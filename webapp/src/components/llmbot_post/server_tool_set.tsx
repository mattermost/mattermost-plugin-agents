// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {
    AlertCircleOutlineIcon,
    CheckIcon,
    ChevronDownIcon,
    ChevronRightIcon,
    CodeTagsIcon,
    LinkVariantIcon,
    MagnifyIcon,
} from '@mattermost/compass-icons/components';

import {
    ServerToolCodeInterpreter,
    ServerToolStatusError,
    ServerToolStatusInProgress,
    ServerToolUse,
    ServerToolWebFetch,
    ServerToolWebSearch,
} from '@/types/conversation';

import LoadingSpinner from '../assets/loading_spinner';

interface ServerToolSetProps {
    serverTools: ServerToolUse[];
}

// ServerToolSet renders provider-executed (server) tool activity: web
// searches, page fetches and sandbox code runs that happen on the provider's
// infrastructure during a response. Cards are informational — there is no
// approval flow — with a spinner while in progress and collapsible details
// for code/output.
const ServerToolSet: React.FC<ServerToolSetProps> = ({serverTools}) => {
    if (serverTools.length === 0) {
        return null;
    }
    return (
        <Container data-testid='server-tool-set'>
            {serverTools.map((tool) => (
                <ServerToolCard
                    key={tool.id}
                    tool={tool}
                />
            ))}
        </Container>
    );
};

const hostnameOf = (url?: string): string => {
    if (!url) {
        return '';
    }
    try {
        return new URL(url).hostname;
    } catch {
        return url;
    }
};

const ServerToolCard: React.FC<{tool: ServerToolUse}> = ({tool}) => {
    const intl = useIntl();
    const [expanded, setExpanded] = useState(false);

    const details = buildDetails(tool);
    const canExpand = details.length > 0;

    let icon = <CodeTagsIcon/>;
    let title = '';
    switch (tool.tool) {
    case ServerToolWebSearch:
        icon = <MagnifyIcon/>;
        title = tool.query ? intl.formatMessage({defaultMessage: 'Searched the web for "{query}"'}, {query: tool.query}) : intl.formatMessage({defaultMessage: 'Searched the web'});
        break;
    case ServerToolWebFetch:
        icon = <LinkVariantIcon/>;
        title = tool.url ? intl.formatMessage({defaultMessage: 'Fetched {host}'}, {host: hostnameOf(tool.url)}) : intl.formatMessage({defaultMessage: 'Fetched a web page'});
        break;
    case ServerToolCodeInterpreter:
        icon = <CodeTagsIcon/>;
        title = tool.sub_tool === 'text_editor' ? intl.formatMessage({defaultMessage: 'Edited files in the provider sandbox'}) : intl.formatMessage({defaultMessage: 'Ran code in the provider sandbox'});
        break;
    default:
        title = intl.formatMessage({defaultMessage: 'Used a provider tool'});
    }

    return (
        <Card data-testid={`server-tool-${tool.tool}`}>
            <Header
                $canExpand={canExpand}
                onClick={() => canExpand && setExpanded(!expanded)}
            >
                <ChevronWrapper $visible={canExpand}>
                    {expanded ? <ChevronDownIcon/> : <ChevronRightIcon/>}
                </ChevronWrapper>
                <ToolIcon>{icon}</ToolIcon>
                <Title>{title}</Title>
                <StatusIndicator status={tool.status}/>
            </Header>
            {expanded && details.map((detail) => (
                <Detail key={detail.label}>
                    <DetailLabel>{detail.label}</DetailLabel>
                    <DetailContent>{detail.content}</DetailContent>
                </Detail>
            ))}
        </Card>
    );
};

interface DetailEntry {
    label: string;
    content: string;
}

// buildDetails assembles the expandable sections of a card. Labels are the
// tool-domain terms themselves (URL, stdout, …) and intentionally untranslated.
function buildDetails(tool: ServerToolUse): DetailEntry[] {
    const details: DetailEntry[] = [];
    if (tool.tool === ServerToolWebFetch && tool.url) {
        details.push({label: 'URL', content: tool.url});
    }
    if (tool.title) {
        details.push({label: 'Title', content: tool.title});
    }
    if (tool.command) {
        details.push({label: tool.sub_tool === 'bash' ? '$' : 'Input', content: tool.command});
    }
    if (tool.output) {
        details.push({label: 'Output', content: tool.output});
    }
    if (tool.error_code) {
        details.push({label: 'Error', content: tool.error_code});
    }
    return details;
}

const StatusIndicator: React.FC<{status: ServerToolUse['status']}> = ({status}) => {
    switch (status) {
    case ServerToolStatusInProgress:
        return <SpinnerWrapper><SmallSpinner/></SpinnerWrapper>;
    case ServerToolStatusError:
        return <ErrorIcon/>;
    default:
        return <SuccessIcon/>;
    }
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin: 4px 0;
`;

const Card = styled.div`
    display: flex;
    flex-direction: column;
`;

const Header = styled.div<{$canExpand: boolean}>`
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: ${(props) => (props.$canExpand ? 'pointer' : 'default')};
    user-select: none;
`;

const ChevronWrapper = styled.div<{$visible: boolean}>`
    color: rgba(var(--center-channel-color-rgb), 0.56);
    width: 16px;
    display: flex;
    align-items: center;
    justify-content: center;
    visibility: ${(props) => (props.$visible ? 'visible' : 'hidden')};

    svg {
        width: 14px;
        height: 14px;
    }
`;

const ToolIcon = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    width: 14px;
    display: flex;
    align-items: center;
    justify-content: center;

    svg {
        width: 14px;
        height: 14px;
    }
`;

const Title = styled.span`
    font-size: 12px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    flex-grow: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const SpinnerWrapper = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 12px;
    height: 12px;
`;

const SmallSpinner = styled(LoadingSpinner)`
    width: 12px;
    height: 12px;
`;

const SuccessIcon = styled(CheckIcon)`
    color: var(--online-indicator);
    width: 12px;
    height: 12px;
`;

const ErrorIcon = styled(AlertCircleOutlineIcon)`
    color: var(--error-text);
    width: 12px;
    height: 12px;
`;

const Detail = styled.div`
    margin-left: 24px;
    padding: 4px 0;
`;

const DetailLabel = styled.div`
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const DetailContent = styled.pre`
    margin: 2px 0 0;
    padding: 6px 8px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
    font-size: 11px;
    line-height: 16px;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 240px;
    overflow-y: auto;
`;

export default ServerToolSet;
