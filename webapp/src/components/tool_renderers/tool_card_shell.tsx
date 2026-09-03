// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon, CheckIcon, AlertCircleOutlineIcon, CloseCircleOutlineIcon, GlobeIcon, LockIcon} from '@mattermost/compass-icons/components';

// eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {toolDisplayName} from '@/utils/tool_identity';

import {ToolApprovalStage, ToolCall, ToolCallStatus} from '../tool_types';
import {ToolArgumentsRaw, ToolResultBody, hasInspectableArguments} from '../tool_arguments';

import LoadingSpinner from '../assets/loading_spinner';
import IconCheckCircle from '../assets/icon_check_circle';

// Bordered card container; border/radius/shadow match QuestionCard.
const ToolCallCard = styled.div`
    display: flex;
    flex-direction: column;
    margin-bottom: 4px;
    padding: 12px 16px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    background: var(--center-channel-bg);
    box-shadow: 0 2px 3px rgba(0, 0, 0, 0.08);
`;

const ToolCallHeader = styled.div<{$canExpand: boolean}>`
    display: flex;
    align-items: center;
    gap: 10px;
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
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    flex-grow: 1;

    // MCP-supplied titles can be arbitrarily long; keep the header on one line.
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
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

const AutoApprovedBadge = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 0 6px;
    height: 18px;
    border-radius: 9px;
    background: rgba(var(--online-indicator-rgb), 0.12);
    font-size: 10px;
    font-weight: 600;
    line-height: 14px;
    color: var(--online-indicator);
    white-space: nowrap;
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
    margin-top: 12px;
`;

// Accept renders as the filled primary action, Reject as the tinted secondary
// (matching the design's confirm-button pair).
const AcceptRejectButton = styled.button<{$primary?: boolean}>`
    background: ${(props) => (props.$primary ? 'var(--button-bg)' : 'rgba(var(--button-bg-rgb), 0.08)')};
    color: ${(props) => (props.$primary ? 'var(--button-color)' : 'var(--button-bg)')};
    border: none;
    padding: 6px 16px;
	height: 32px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    cursor: pointer;

    &:hover {
        background: ${(props) => (props.$primary ? 'rgba(var(--button-bg-rgb), 0.88)' : 'rgba(var(--button-bg-rgb), 0.12)')};
    }

    &:active {
        background: ${(props) => (props.$primary ? 'rgba(var(--button-bg-rgb), 0.92)' : 'rgba(var(--button-bg-rgb), 0.16)')};
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
    font-size: 12px;
    font-weight: 600;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    padding-top: 12px;
`;

const ResultContainer = styled.div`
    margin: 0;
`;

const RawToggleRow = styled.div`
    display: flex;
    margin-top: 10px;
`;

const RawToggleButton = styled.button`
    padding: 0;
    border: none;
    background: none;
    cursor: pointer;
    font-size: 11px;
    font-weight: 600;
    line-height: 16px;
    color: var(--link-color);

    &:hover {
        text-decoration: underline;
    }
`;

export interface ToolCardShellProps {
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
    isAutoApproved?: boolean;

    // The arguments body: the generic field list or a rich card's rendering.
    children?: React.ReactNode;
}

// Props for a card component: everything the shell takes except the body.
export type RichCardProps = Omit<ToolCardShellProps, 'children'>;

/**
 * ToolCardShell renders the shared approval chrome for a tool call: expandable
 * header, arguments body (children) with a "View raw" toggle, result section,
 * result-review callout, and decision buttons. Cards supply only the arguments
 * body, so the approval flow and payload inspection stay in one place.
 */
const ToolCardShell: React.FC<ToolCardShellProps> = ({
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
    isAutoApproved = false,
    children,
}) => {
    const {formatMessage} = useIntl();
    const [showRaw, setShowRaw] = useState(false);

    const isPending = tool.status === ToolCallStatus.Pending;
    const isAccepted = tool.status === ToolCallStatus.Accepted;
    const isSuccess = tool.status === ToolCallStatus.Success || tool.status === ToolCallStatus.AutoApproved;
    const isError = tool.status === ToolCallStatus.Error;
    const isRejected = tool.status === ToolCallStatus.Rejected;
    const isResultApprovalStage = approvalStage === 'result';
    const showDecisionButtons = Boolean(onApprove && onReject) &&
        (isResultApprovalStage ||
            (approvalStage === 'call' && isPending && !tool.would_auto_execute));
    const showProcessingSpinner = isProcessing || isPending || isAccepted;
    const showResultReviewCallout = !isCollapsed && showDecisionButtons && isResultApprovalStage;

    const displayName = toolDisplayName(tool);

    const canShowRaw = showArguments && hasInspectableArguments(tool.arguments);

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
                            $primary={true}
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

    const showResultBody = showResults && Boolean(tool.result);

    return (
        <ToolCallCard>
            <ToolCallHeader
                $canExpand={canExpand}
                onClick={canExpand ? onToggleCollapse : undefined} // eslint-disable-line no-undefined
                role={canExpand ? 'button' : undefined} // eslint-disable-line no-undefined
                tabIndex={canExpand ? 0 : undefined} // eslint-disable-line no-undefined
                aria-expanded={canExpand ? !isCollapsed : undefined} // eslint-disable-line no-undefined
                onKeyDown={canExpand ? (e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault();
                        onToggleCollapse();
                    }
                } : undefined} // eslint-disable-line no-undefined
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
                <ToolName title={displayName}>{displayName}</ToolName>
                {(tool.status === ToolCallStatus.AutoApproved || isAutoApproved) && (
                    <AutoApprovedBadge>
                        <FormattedMessage
                            id='ai.tool_call.auto_approved'
                            defaultMessage='Auto-approved'
                        />
                    </AutoApprovedBadge>
                )}
            </ToolCallHeader>

            {!isCollapsed && (
                <>
                    {showArguments && (showRaw ? <ToolArgumentsRaw arguments={tool.arguments}/> : children)}

                    {canShowRaw && (
                        <RawToggleRow>
                            <RawToggleButton
                                type='button'
                                onClick={() => setShowRaw((prev) => !prev)}
                            >
                                {showRaw ? (
                                    <FormattedMessage
                                        id='ai.tool_call.hide_raw'
                                        defaultMessage='Hide raw'
                                    />
                                ) : (
                                    <FormattedMessage
                                        id='ai.tool_call.view_raw'
                                        defaultMessage='View raw'
                                    />
                                )}
                            </RawToggleButton>
                        </RawToggleRow>
                    )}

                    {showResultBody && (isSuccess || isError) && (
                        <>
                            <ResponseLabel>
                                {isSuccess && <ResponseSuccessIcon/>}
                                {isError && <ResponseErrorIcon/>}
                                <FormattedMessage
                                    id='ai.tool_call.response'
                                    defaultMessage='Response'
                                />
                            </ResponseLabel>
                            <ResultContainer>
                                <ToolResultBody result={tool.result as string}/>
                            </ResultContainer>
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

export default ToolCardShell;
