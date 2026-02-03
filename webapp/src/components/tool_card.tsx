// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon, CheckIcon, AlertCircleOutlineIcon, CloseCircleOutlineIcon} from '@mattermost/compass-icons/components';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';

import {ToolCall, ToolCallStatus} from './llmbot_post/llmbot_post';

import LoadingSpinner from './assets/loading_spinner';
import IconCheckCircle from './assets/icon_check_circle';

// Styled components based on the Figma design
const ToolCallCard = styled.div`
    display: flex;
    flex-direction: column;
    margin-bottom: 4px;
    padding: 0;
    border: none;
    background: transparent;
    box-shadow: none;
`;

const ToolCallHeader = styled.div<{isCollapsed: boolean}>`
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    user-select: none;
`;

const StyledChevronIcon = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.56);
	width: 16px;
    padding: 0 1px;
    display: flex;
    align-items: center;
    justify-content: center;
`;

const StatusIcon = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
	width: 12px;
    display: flex;
    align-items: center;
    justify-content: center;
`;

const ToolName = styled.span`
    font-size: 12px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    flex-grow: 1;
`;

const ToolCallArguments = styled.div`
    margin: 0;
    padding-left: 24px;

    // Style code blocks rendered by Mattermost
    pre {
        margin: 0;
    }
`;

const StatusContainer = styled.div`
    display: flex;
    align-items: center;
    font-size: 11px;
    line-height: 16px;
    gap: 8px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    margin-top: 16px;
`;

const ProcessingSpinnerContainer = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 12px;
    height: 12px;
`;

const ProcessingSpinner = styled(LoadingSpinner)`
    width: 12px;
    height: 12px;
`;

const SmallSpinner = styled(LoadingSpinner)`
    width: 12px;
    height: 12px;
`;

const SmallSuccessIcon = styled(CheckIcon)`
    color: var(--online-indicator);
    width: 12px;
    height: 12px;
`;

const SmallErrorIcon = styled(AlertCircleOutlineIcon)`
    color: var(--error-text);
    width: 12px;
    height: 12px;
`;

const SmallRejectedIcon = styled(CloseCircleOutlineIcon)`
    color: var(--dnd-indicator);
    width: 12px;
    height: 12px;
`;

const ResponseSuccessIcon = styled(IconCheckCircle)`
    color: var(--online-indicator);
    width: 12px;
    height: 12px;
`;

const ResponseErrorIcon = styled(AlertCircleOutlineIcon)`
    color: var(--error-text);
    width: 12px;
    height: 12px;
`;

const ResponseRejectedIcon = styled(CloseCircleOutlineIcon)`
    color: var(--dnd-indicator);
    width: 12px;
    height: 12px;
`;

const ButtonContainer = styled.div`
    display: flex;
    gap: 8px;
    margin-top: 4px;
    padding-left: 24px;
`;

const AcceptRejectButton = styled.button`
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

const ResponseLabel = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 11px;
    font-weight: 600;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    padding-top: 8px;
    padding-left: 24px;
`;

const ResultContainer = styled.div`
    margin: 0;
    padding-left: 24px;

    // Style code blocks rendered by Mattermost
    pre {
        margin: 0;
    }
`;

interface ToolCardProps {
    tool: ToolCall;
    isCollapsed: boolean;
    isProcessing: boolean;
    onToggleCollapse: () => void;
    onApprove?: () => void;
    onReject?: () => void;
}

const ToolCard: React.FC<ToolCardProps> = ({
    tool,
    isCollapsed,
    isProcessing,
    onToggleCollapse,
    onApprove,
    onReject,
}) => {
    const isPending = tool.status === ToolCallStatus.Pending;
    const isAccepted = tool.status === ToolCallStatus.Accepted;
    const isSuccess = tool.status === ToolCallStatus.Success;
    const isError = tool.status === ToolCallStatus.Error;
    const isRejected = tool.status === ToolCallStatus.Rejected;

    // Convert underscores to spaces and capitalize first letter of each word
    // (e.g., "create_post" -> "Create Post")
    const displayName = tool.name.
        replace(/_/g, ' ').
        split(' ').
        map((word) => word.charAt(0).toUpperCase() + word.slice(1)).
        join(' ');

    const siteURL = useSelector<GlobalState, string | undefined>((state) => state.entities.general.config.SiteURL);
    const team = useSelector((state: GlobalState) => state.entities.teams.currentTeamId);
    const allowUnsafeLinks = useSelector<GlobalState, boolean>((state: any) => state['plugins-' + manifest.id]?.allowUnsafeLinks ?? false);

    // @ts-ignore
    const {formatText, messageHtmlToComponent} = window.PostUtils;

    const markdownOptions = {
        singleline: false,
        mentionHighlight: false,
        atMentions: false,
        team,
        unsafeLinks: !allowUnsafeLinks,
        minimumHashtagLength: 1000000000,
        siteURL,
    };

    const messageHtmlToComponentOptions = {
        hasPluginTooltips: false,
        latex: false,
        inlinelatex: false,
    };

    // Render arguments as JSON code block
    const argumentsMarkdown = `\`\`\`json\n${JSON.stringify(tool.arguments, null, 2)}\n\`\`\``;
    const renderedArguments = useMemo(() => {
        return messageHtmlToComponent(
            formatText(argumentsMarkdown, markdownOptions),
            messageHtmlToComponentOptions,
        );
    }, [tool.arguments]);

    // Render result as code block - try to detect if it's JSON
    const resultMarkdown = useMemo(() => {
        if (!tool.result) {
            return '';
        }

        // Try to parse as JSON to determine if we should use json syntax highlighting
        try {
            JSON.parse(tool.result);
            return `\`\`\`json\n${tool.result}\n\`\`\``;
        } catch {
            // Not JSON, use plain code block
            return `\`\`\`\n${tool.result}\n\`\`\``;
        }
    }, [tool.result]);

    const renderedResult = useMemo(() => {
        if (!tool.result || !resultMarkdown) {
            return null;
        }
        return messageHtmlToComponent(
            formatText(resultMarkdown, markdownOptions),
            messageHtmlToComponentOptions,
        );
    }, [resultMarkdown]);

    return (
        <ToolCallCard>
            <ToolCallHeader
                isCollapsed={isCollapsed}
                onClick={onToggleCollapse}
            >
                <StyledChevronIcon>
                    {isCollapsed ? <ChevronRightIcon size={16}/> : <ChevronDownIcon size={16}/>}
                </StyledChevronIcon>
                <StatusIcon>
                    {isPending && !isProcessing && <SmallSpinner/>}
                    {(isAccepted || (isPending && isProcessing)) && <SmallSpinner/>}
                    {isSuccess && <SmallSuccessIcon size={16}/>}
                    {isError && <SmallErrorIcon size={16}/>}
                    {isRejected && <SmallRejectedIcon size={16}/>}
                </StatusIcon>
                <ToolName>{displayName}</ToolName>
            </ToolCallHeader>

            {!isCollapsed && (
                <>
                    <ToolCallArguments>{renderedArguments}</ToolCallArguments>

                    {(isSuccess || isError) && renderedResult && (
                        <>
                            <ResponseLabel>
                                {isSuccess && <ResponseSuccessIcon/>}
                                {isError && <ResponseErrorIcon/>}
                                <FormattedMessage
                                    id='ai.tool_call.response'
                                    defaultMessage='Response'
                                />
                            </ResponseLabel>
                            <ResultContainer>{renderedResult}</ResultContainer>
                        </>
                    )}

                    {isRejected && (
                        <StatusContainer>
                            <ResponseRejectedIcon/>
                            <FormattedMessage
                                id='ai.tool_call.status.rejected'
                                defaultMessage='Rejected'
                            />
                        </StatusContainer>
                    )}
                </>
            )}

            {isPending && (
                isProcessing ? (
                    <StatusContainer>
                        <ProcessingSpinnerContainer>
                            <ProcessingSpinner/>
                        </ProcessingSpinnerContainer>
                        <FormattedMessage
                            id='ai.tool_call.processing'
                            defaultMessage='Processing...'
                        />
                    </StatusContainer>
                ) : (
                    <ButtonContainer>
                        <AcceptRejectButton
                            onClick={onApprove}
                            disabled={isProcessing}
                        >
                            <FormattedMessage
                                id='ai.tool_call.approve'
                                defaultMessage='Accept'
                            />
                        </AcceptRejectButton>
                        <AcceptRejectButton
                            onClick={onReject}
                            disabled={isProcessing}
                        >
                            <FormattedMessage
                                id='ai.tool_call.reject'
                                defaultMessage='Reject'
                            />
                        </AcceptRejectButton>
                    </ButtonContainer>
                )
            )}
        </ToolCallCard>
    );
};

export default ToolCard;
