// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon, CheckIcon, AlertCircleOutlineIcon, CloseCircleOutlineIcon, GlobeIcon, LockIcon} from '@mattermost/compass-icons/components';
import {useSelector} from 'react-redux';

// eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {GlobalState} from '@mattermost/types/store';

import manifest from '@/manifest';

import {ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';

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

const ToolCallHeader = styled.div<{isCollapsed: boolean; $canExpand: boolean}>`
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: ${(props) => (props.$canExpand ? 'pointer' : 'default')};
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

const ResultDecisionButton = styled.button<{variant: 'primary' | 'secondary'}>`
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    height: 24px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    cursor: pointer;

    border: 1px solid ${(props) => (props.variant === 'primary' ? 'var(--button-bg)' : 'rgba(var(--button-bg-rgb), 0.16)')};
    background: ${(props) => (props.variant === 'primary' ? 'var(--button-bg)' : 'rgba(var(--button-bg-rgb), 0.08)')};
    color: ${(props) => (props.variant === 'primary' ? 'var(--button-color)' : 'var(--button-bg)')};

    &:hover {
        background: ${(props) => (props.variant === 'primary' ? 'rgba(var(--button-bg-rgb), 0.88)' : 'rgba(var(--button-bg-rgb), 0.12)')};
    }

    &:active {
        background: ${(props) => (props.variant === 'primary' ? 'rgba(var(--button-bg-rgb), 0.92)' : 'rgba(var(--button-bg-rgb), 0.16)')};
    }
`;

const ResultReviewCallout = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-top: 12px;
    margin-bottom: 12px;
    margin-left: 24px;
    padding: 12px;
    border-radius: 8px;
    border: 1px solid rgba(var(--error-text-color-rgb), 0.16);
    background-color: rgba(var(--error-text-color-rgb), 0.04);
`;

const ResultReviewHeader = styled.div`
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const ResultReviewHelpButton = styled.button`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    border: none;
    background: transparent;
    cursor: pointer;
    color: rgba(var(--center-channel-color-rgb), 0.56);

    &:hover {
        color: rgba(var(--center-channel-color-rgb), 0.72);
    }
`;

const ResultReviewBody = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const TooltipTitle = styled.div`
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    margin-bottom: 4px;
`;

const TooltipBody = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    max-width: 320px;
    opacity: 0.88;
`;

const ShareVisibilityTooltip = styled(Tooltip)`
    .tooltip-arrow {
        display: none;
    }

    .tooltip-inner {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        padding: 2px 8px;
        border-radius: 10px;
        max-width: none;

        font-size: 11px;
        font-weight: 600;
        line-height: 16px;

        color: var(--error-text);
        background-color: var(--center-channel-bg);
        border: 1px solid rgba(var(--error-text-color-rgb), 0.24);
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
    localDecision?: boolean;
    onToggleCollapse: () => void;
    onApprove?: () => void;
    onReject?: () => void;
    canExpand: boolean;
    showArguments: boolean;
    showResults: boolean;
    approvalStage?: ToolApprovalStage;
}

const ToolCard: React.FC<ToolCardProps> = ({
    tool,
    isCollapsed,
    isProcessing,
    localDecision,
    onToggleCollapse,
    onApprove,
    onReject,
    canExpand,
    showArguments,
    showResults,
    approvalStage = 'call',
}) => {
    const {formatMessage} = useIntl();

    const isPending = tool.status === ToolCallStatus.Pending;
    const isAccepted = tool.status === ToolCallStatus.Accepted;
    const isSuccess = tool.status === ToolCallStatus.Success;
    const isError = tool.status === ToolCallStatus.Error;
    const isRejected = tool.status === ToolCallStatus.Rejected;
    const showDecisionButtons = Boolean(onApprove && onReject);
    const showProcessingSpinner = isProcessing || isPending || isAccepted;
    const isResultApprovalStage = approvalStage === 'result';
    const showResultReviewCallout = !isCollapsed && showDecisionButtons && isResultApprovalStage;

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

    const renderedArguments = useMemo(() => {
        if (!showArguments) {
            return null;
        }

        const argumentsValue = tool.arguments ?? {};
        const argumentsMarkdown = `\`\`\`json\n${JSON.stringify(argumentsValue, null, 2)}\n\`\`\``;
        return messageHtmlToComponent(
            formatText(argumentsMarkdown, markdownOptions),
            messageHtmlToComponentOptions,
        );
    }, [showArguments, tool.arguments]);

    const hasLocalDecision = localDecision != null;

    const renderDecisionButtons = () => {
        if (hasLocalDecision) {
            return (
                <StatusContainer>
                    {localDecision ? <SmallSuccessIcon size={16}/> : <SmallRejectedIcon size={16}/>}
                    {localDecision ? (
                        <FormattedMessage
                            id='ai.tool_call.status.accepted'
                            defaultMessage='Accepted'
                        />
                    ) : (
                        <FormattedMessage
                            id='ai.tool_call.status.rejected'
                            defaultMessage='Rejected'
                        />
                    )}
                </StatusContainer>
            );
        }

        if (isProcessing) {
            return (
                <StatusContainer>
                    <ProcessingSpinnerContainer>
                        <ProcessingSpinner/>
                    </ProcessingSpinnerContainer>
                    <FormattedMessage
                        id='ai.tool_call.processing'
                        defaultMessage='Processing...'
                    />
                </StatusContainer>
            );
        }

        return (
            <ButtonContainer>
                {isResultApprovalStage ? (
                    <>
                        <OverlayTrigger
                            placement='top'
                            overlay={
                                <ShareVisibilityTooltip>
                                    <GlobeIcon size={14}/>
                                    <FormattedMessage
                                        id='ai.tool_call.visible_to_channel'
                                        defaultMessage='Visible to channel'
                                    />
                                </ShareVisibilityTooltip>
                            }
                        >
                            <span>
                                <ResultDecisionButton
                                    variant='primary'
                                    onClick={onApprove}
                                    disabled={isProcessing}
                                >
                                    <GlobeIcon size={14}/>
                                    <FormattedMessage
                                        id='ai.tool_call.share'
                                        defaultMessage='Share'
                                    />
                                </ResultDecisionButton>
                            </span>
                        </OverlayTrigger>
                        <ResultDecisionButton
                            variant='secondary'
                            onClick={onReject}
                            disabled={isProcessing}
                        >
                            <LockIcon size={14}/>
                            <FormattedMessage
                                id='ai.tool_call.keep_private'
                                defaultMessage='Keep private'
                            />
                        </ResultDecisionButton>
                    </>
                ) : (
                    <>
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
                    </>
                )}
            </ButtonContainer>
        );
    };

    const renderedResult = useMemo(() => {
        if (!showResults || !tool.result) {
            return null;
        }

        // Render result as code block - try to detect if it's JSON
        const resultMarkdown = (() => {
            try {
                JSON.parse(tool.result as string);
                return `\`\`\`json\n${tool.result}\n\`\`\``;
            } catch {
                return `\`\`\`\n${tool.result}\n\`\`\``;
            }
        })();

        return messageHtmlToComponent(
            formatText(resultMarkdown, markdownOptions),
            messageHtmlToComponentOptions,
        );
    }, [showResults, tool.result]);

    return (
        <ToolCallCard>
            <ToolCallHeader
                isCollapsed={isCollapsed}
                $canExpand={canExpand}
                onClick={canExpand ? onToggleCollapse : undefined} // eslint-disable-line no-undefined
            >
                {canExpand && (
                    <StyledChevronIcon>
                        {isCollapsed ? <ChevronRightIcon size={16}/> : <ChevronDownIcon size={16}/>}
                    </StyledChevronIcon>
                )}
                <StatusIcon>
                    {showProcessingSpinner && <SmallSpinner/>}
                    {!showProcessingSpinner && isSuccess && <SmallSuccessIcon size={16}/>}
                    {!showProcessingSpinner && isError && <SmallErrorIcon size={16}/>}
                    {!showProcessingSpinner && isRejected && <SmallRejectedIcon size={16}/>}
                </StatusIcon>
                <ToolName>{displayName}</ToolName>
            </ToolCallHeader>

            {!isCollapsed && (
                <>
                    {renderedArguments && <ToolCallArguments>{renderedArguments}</ToolCallArguments>}

                    {showResults && (isSuccess || isError) && renderedResult && (
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

                    {showResultReviewCallout && (
                        <ResultReviewCallout>
                            <ResultReviewHeader>
                                <FormattedMessage
                                    id='ai.tool_call.review_tool_response'
                                    defaultMessage='Review tool response'
                                />
                                <OverlayTrigger
                                    placement='top'
                                    overlay={
                                        <Tooltip>
                                            <TooltipTitle>
                                                <FormattedMessage
                                                    id='ai.tool_call.tooltip.why_second_step'
                                                    defaultMessage='Why is there a second approval step?'
                                                />
                                            </TooltipTitle>
                                            <TooltipBody>
                                                <FormattedMessage
                                                    id='ai.tool_call.tooltip.approval_body'
                                                    defaultMessage='This step controls whether Agents can use the tool response when generating the next message in the channel. If you reject, the response stays private and won’t be used in the channel reply.'
                                                />
                                            </TooltipBody>
                                        </Tooltip>
                                    }
                                >
                                    <ResultReviewHelpButton
                                        type='button'
                                        aria-label={formatMessage({id: 'ai.tool_call.learn_more', defaultMessage: 'Learn more'})}
                                    >
                                        <AlertCircleOutlineIcon size={16}/>
                                    </ResultReviewHelpButton>
                                </OverlayTrigger>
                            </ResultReviewHeader>
                            <ResultReviewBody>
                                <FormattedMessage
                                    id='ai.tool_call.approval_warning'
                                    defaultMessage='Approving lets Agents use this response in its next message. That message will be visible to everyone in the channel—only approve results you’re comfortable sharing.'
                                />
                            </ResultReviewBody>
                        </ResultReviewCallout>
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

            {showDecisionButtons && renderDecisionButtons()}
        </ToolCallCard>
    );
};

export default ToolCard;
