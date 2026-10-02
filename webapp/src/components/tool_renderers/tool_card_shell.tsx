// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {ChevronDownIcon, ChevronRightIcon, CheckIcon, AlertCircleOutlineIcon, CloseCircleOutlineIcon, GlobeIcon, HelpCircleOutlineIcon, LockIcon} from '@mattermost/compass-icons/components';

// eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tag} from '@mattermost/compass-ui/components/tag';

import {toolDisplayName} from '@/utils/tool_identity';

import {ToolApprovalStage, ToolCall, ToolCallStatus} from '../tool_types';
import {ToolArgumentsRaw, ToolResultBody, hasInspectableArguments} from '../tool_arguments';
import ToolStatusIcon from '../tool_status_icon';

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

const SmallSuccessIcon = styled(CheckIcon)`
    color: var(--online-indicator);
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
    margin-top: 12px;
`;

const ResultReviewCallout = styled(SectionNotice)`
    margin-top: 12px;
`;

const ResultReviewTitle = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 4px;
`;

// Native button with a bare svg: the notice title is a <p>, so compass
// IconButton (which renders a <div> icon) cannot nest here.
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
    const isSuccess = tool.status === ToolCallStatus.Success || tool.status === ToolCallStatus.AutoApproved;
    const isError = tool.status === ToolCallStatus.Error;
    const isRejected = tool.status === ToolCallStatus.Rejected;
    const isResultApprovalStage = approvalStage === 'result';
    const showDecisionButtons = Boolean(onApprove && onReject) &&
        (isResultApprovalStage ||
            (approvalStage === 'call' && isPending && !tool.would_auto_execute));
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
                    <Spinner
                        size='12'
                        aria-hidden={true}
                    />
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
                                <Button
                                    emphasis='primary'
                                    size='x-small'
                                    leadingIcon={<Icon glyph={<GlobeIcon/>}/>}
                                    onClick={onApprove}
                                    disabled={isProcessing}
                                >
                                    <FormattedMessage
                                        id='ai.tool_call.share'
                                        defaultMessage='Share'
                                    />
                                </Button>
                            </span>
                        </OverlayTrigger>
                        <Button
                            emphasis='tertiary'
                            size='x-small'
                            leadingIcon={<Icon glyph={<LockIcon/>}/>}
                            onClick={onReject}
                            disabled={isProcessing}
                        >
                            <FormattedMessage
                                id='ai.tool_call.keep_private'
                                defaultMessage='Keep private'
                            />
                        </Button>
                    </>
                ) : (
                    <>
                        <Button
                            emphasis='primary'
                            size='small'
                            onClick={onApprove}
                            disabled={isProcessing}
                        >
                            <FormattedMessage
                                id='ai.tool_call.approve'
                                defaultMessage='Accept'
                            />
                        </Button>
                        <Button
                            emphasis='tertiary'
                            size='small'
                            onClick={onReject}
                            disabled={isProcessing}
                        >
                            <FormattedMessage
                                id='ai.tool_call.reject'
                                defaultMessage='Reject'
                            />
                        </Button>
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
                <ToolStatusIcon
                    status={tool.status}
                    isProcessing={isProcessing}
                />
                <ToolName title={displayName}>{displayName}</ToolName>
                {(tool.status === ToolCallStatus.AutoApproved || isAutoApproved) && (
                    <Tag
                        type='success'
                        size='x-small'
                        label={
                            <FormattedMessage
                                id='ai.tool_call.auto_approved'
                                defaultMessage='Auto-approved'
                            />
                        }
                    />
                )}
            </ToolCallHeader>

            {!isCollapsed && (
                <>
                    {showArguments && (showRaw ? <ToolArgumentsRaw arguments={tool.arguments}/> : children)}

                    {canShowRaw && (
                        <RawToggleRow>
                            <Button
                                emphasis='link'
                                size='x-small'
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
                            </Button>
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
                        <ResultReviewCallout
                            type='warning'
                            title={
                                <ResultReviewTitle>
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
                                            <HelpCircleOutlineIcon size={16}/>
                                        </ResultReviewHelpButton>
                                    </OverlayTrigger>
                                </ResultReviewTitle>
                            }
                            description={
                                <FormattedMessage
                                    id='ai.tool_call.approval_warning'
                                    defaultMessage='Approving lets Agents use this response in its next message. That message will be visible to everyone in the channel—only approve results you’re comfortable sharing.'
                                />
                            }
                        />
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
