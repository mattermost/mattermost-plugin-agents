// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {
    AlertCircleOutlineIcon,
    CheckCircleIcon,
    ChevronDownIcon,
    ChevronRightIcon,
    CloseCircleOutlineIcon,
    GlobeIcon,
    LockIcon,
} from '@mattermost/compass-icons/components';

import {getDelegationStatus} from '@/client';
import {useDelegationUpdates} from '@/hooks/use_delegation_updates';
import type {DelegationPhase, DelegationStatus, DelegationUpdate} from '@/types/delegation';
import {stripWirePrefix} from '@/utils/tool_names';

import LoadingSpinner from '../assets/loading_spinner';
import {ToolApprovalStage, ToolCall, ToolCallStatus} from '../tool_types';

// AskAgentToolName is the bare name of the embedded delegation tool.
export const AskAgentToolName = 'ask_agent';
const EmbeddedServerOrigin = 'embedded://mattermost';

// isAskAgentToolCall reports whether a tool call is the embedded delegation
// tool. Redacted payloads (non-requesters) may omit server_origin, so accept
// a bare name match in that case.
export function isAskAgentToolCall(tool: ToolCall): boolean {
    if (stripWirePrefix(tool.name) !== AskAgentToolName) {
        return false;
    }
    return !tool.server_origin || tool.server_origin === EmbeddedServerOrigin;
}

type AskAgentArgs = {
    agent: string;
    task: string;
}

export function parseAskAgentArgs(argumentsValue: ToolCall['arguments']): AskAgentArgs | null {
    if (argumentsValue == null || typeof argumentsValue !== 'object' || Array.isArray(argumentsValue)) {
        return null;
    }
    const record = argumentsValue as Record<string, unknown>;
    const agent = typeof record.agent === 'string' ? record.agent : '';
    const task = typeof record.task === 'string' ? record.task : '';
    if (!agent && !task) {
        return null;
    }
    return {agent, task};
}

interface DelegationCardProps {
    tool: ToolCall;
    approvalStage: ToolApprovalStage;
    isProcessing: boolean;
    localDecision?: boolean;
    canApprove: boolean;
    onApprove?: () => void;
    onReject?: () => void;
}

// deriveTerminalPhase maps a persisted tool call status to a card phase, or
// null when the delegation is (potentially) still in flight.
function deriveTerminalPhase(status: ToolCallStatus, result?: string): DelegationPhase | null {
    switch (status) {
    case ToolCallStatus.Success:
    case ToolCallStatus.AutoApproved:
        return 'completed';
    case ToolCallStatus.Error:
        return result && result.includes('timed out') ? 'timed_out' : 'failed';
    default:
        return null;
    }
}

const DelegationCard: React.FC<DelegationCardProps> = ({
    tool,
    approvalStage,
    isProcessing,
    localDecision,
    canApprove,
    onApprove,
    onReject,
}) => {
    const args = parseAskAgentArgs(tool.arguments);

    const [liveUpdate, setLiveUpdate] = useState<DelegationUpdate | null>(null);
    const [reconciled, setReconciled] = useState<DelegationStatus | null>(null);
    const [answerExpanded, setAnswerExpanded] = useState(false);
    const [taskExpanded, setTaskExpanded] = useState(false);
    const [elapsedSeconds, setElapsedSeconds] = useState(0);

    const onUpdate = useCallback((event: DelegationUpdate) => {
        setLiveUpdate(event);
    }, []);
    useDelegationUpdates(tool.id, onUpdate);

    const terminalPhase = deriveTerminalPhase(tool.status, tool.result);
    const isPendingApproval = tool.status === ToolCallStatus.Pending && !tool.would_auto_execute;
    const isRejected = tool.status === ToolCallStatus.Rejected;

    // In-flight: accepted (persisted before async execution) or pending
    // while executing. Reconcile the precise phase from the server so the
    // card survives reloads without depending on event delivery.
    const inFlight = !terminalPhase && !isPendingApproval && !isRejected;
    useEffect(() => {
        if (!inFlight || liveUpdate) {
            return undefined; // eslint-disable-line no-undefined
        }
        let canceled = false;
        getDelegationStatus(tool.id).then((status) => {
            if (!canceled) {
                setReconciled(status);
            }
        }).catch(() => {
            // Not found (old/expired record or not the requester): render
            // the generic in-flight state.
        });
        return () => {
            canceled = true;
        };
    }, [inFlight, liveUpdate, tool.id]);

    // Elapsed timer while in flight.
    const startedAtRef = useRef<number>(0);
    useEffect(() => {
        if (!inFlight) {
            return undefined; // eslint-disable-line no-undefined
        }
        if (startedAtRef.current === 0) {
            startedAtRef.current = reconciled?.created_at || Date.now();
        }
        const tick = () => setElapsedSeconds(Math.max(0, Math.floor((Date.now() - startedAtRef.current) / 1000)));
        tick();
        const interval = setInterval(tick, 1000);
        return () => clearInterval(interval);
    }, [inFlight, reconciled?.created_at]);

    const phase: DelegationPhase = useMemo(() => {
        if (isPendingApproval) {
            return 'awaiting_approval';
        }
        if (terminalPhase) {
            return terminalPhase;
        }
        if (liveUpdate) {
            return liveUpdate.phase;
        }
        if (reconciled) {
            return reconciled.phase;
        }
        return 'starting';
    }, [isPendingApproval, terminalPhase, liveUpdate, reconciled]);

    const agentDisplayName = liveUpdate?.target_agent_displayname || reconciled?.target_agent_displayname || '';
    const agentUsername = liveUpdate?.target_agent_username || reconciled?.target_agent_username || (args?.agent ?? '').replace(/^@/, '');
    const agentID = liveUpdate?.target_agent_id || reconciled?.target_agent_id || '';
    const permalink = liveUpdate?.permalink || reconciled?.permalink || '';

    const task = args?.task ?? '';
    const taskNeedsClamp = task.length > 280;
    const taskText = taskExpanded || !taskNeedsClamp ? task : `${task.slice(0, 280)}…`;

    const showAcceptReject = canApprove && approvalStage === 'call' && isPendingApproval && Boolean(onApprove && onReject) && localDecision == null;
    const showShareDecision = canApprove && approvalStage === 'result' && Boolean(onApprove && onReject) && localDecision == null && !tool.decided &&
        (tool.status === ToolCallStatus.Success || tool.status === ToolCallStatus.Error);

    const formatElapsed = (seconds: number): string => {
        const mins = Math.floor(seconds / 60);
        const secs = seconds % 60;
        if (mins === 0) {
            return `${secs}s`;
        }
        return `${mins}m ${secs.toString().padStart(2, '0')}s`;
    };

    return (
        <Card data-testid='delegation-card'>
            <Header>
                {agentID ? (
                    <Avatar
                        src={`/api/v4/users/${agentID}/image`}
                        alt={agentUsername}
                    />
                ) : (
                    <AvatarFallback/>
                )}
                <HeaderText>
                    <Title>
                        <FormattedMessage
                            defaultMessage='Delegating to {agent}'
                            values={{agent: agentDisplayName || (agentUsername ? `@${agentUsername}` : '')}}
                        />
                    </Title>
                    {task !== '' && (
                        <Task
                            onClick={taskNeedsClamp ? () => setTaskExpanded(!taskExpanded) : undefined} // eslint-disable-line no-undefined
                            $clickable={taskNeedsClamp}
                        >
                            {taskText}
                        </Task>
                    )}
                </HeaderText>
            </Header>

            <StatusLine data-testid='delegation-status'>
                {(phase === 'starting' || phase === 'running') && (
                    <>
                        <SpinnerHolder><LoadingSpinner/></SpinnerHolder>
                        {phase === 'starting' && (
                            <FormattedMessage defaultMessage='Starting delegation…'/>
                        )}
                        {phase === 'running' && liveUpdate?.activity === 'using_tools' && (
                            <FormattedMessage
                                defaultMessage='Using {tools}…'
                                values={{tools: liveUpdate?.tools || ''}}
                            />
                        )}
                        {phase === 'running' && liveUpdate?.activity !== 'using_tools' && (
                            <FormattedMessage defaultMessage='Working on the task…'/>
                        )}
                        {elapsedSeconds > 0 && <Elapsed>{formatElapsed(elapsedSeconds)}</Elapsed>}
                    </>
                )}
                {phase === 'awaiting_approval' && (
                    <>
                        <NeutralIcon><AlertCircleOutlineIcon size={14}/></NeutralIcon>
                        <FormattedMessage defaultMessage='Waiting for your approval to delegate this task'/>
                    </>
                )}
                {phase === 'waiting_on_you' && (
                    <>
                        <WarnIcon><AlertCircleOutlineIcon size={14}/></WarnIcon>
                        <strong>
                            <FormattedMessage defaultMessage='Waiting on you'/>
                        </strong>
                        <FormattedMessage defaultMessage='— the agent needs your input in the delegation conversation.'/>
                        {elapsedSeconds > 0 && <Elapsed>{formatElapsed(elapsedSeconds)}</Elapsed>}
                    </>
                )}
                {phase === 'completed' && (
                    <>
                        <SuccessIcon><CheckCircleIcon size={14}/></SuccessIcon>
                        <FormattedMessage defaultMessage='Completed'/>
                    </>
                )}
                {phase === 'failed' && (
                    <>
                        <ErrorIcon><AlertCircleOutlineIcon size={14}/></ErrorIcon>
                        <FormattedMessage defaultMessage='Failed'/>
                    </>
                )}
                {phase === 'timed_out' && (
                    <>
                        <ErrorIcon><AlertCircleOutlineIcon size={14}/></ErrorIcon>
                        <FormattedMessage defaultMessage='Timed out'/>
                    </>
                )}
                {isRejected && (
                    <>
                        <RejectedIcon><CloseCircleOutlineIcon size={14}/></RejectedIcon>
                        <FormattedMessage defaultMessage='Rejected'/>
                    </>
                )}
            </StatusLine>

            {permalink !== '' && (
                <ConversationLink
                    href={permalink}
                    rel='noreferrer'
                    data-testid='delegation-view-conversation'
                >
                    <FormattedMessage defaultMessage='View conversation'/>
                </ConversationLink>
            )}

            {phase === 'completed' && Boolean(tool.result) && (
                <AnswerSection>
                    <AnswerToggle onClick={() => setAnswerExpanded(!answerExpanded)}>
                        {answerExpanded ? <ChevronDownIcon size={14}/> : <ChevronRightIcon size={14}/>}
                        <FormattedMessage defaultMessage='Answer'/>
                    </AnswerToggle>
                    {answerExpanded ? (
                        <AnswerBody>{tool.result}</AnswerBody>
                    ) : (
                        <AnswerPreview>{(tool.result || '').slice(0, 200)}{(tool.result || '').length > 200 ? '…' : ''}</AnswerPreview>
                    )}
                </AnswerSection>
            )}
            {(phase === 'failed' || phase === 'timed_out') && Boolean(tool.result) && (
                <AnswerPreview data-testid='delegation-error-detail'>{tool.result}</AnswerPreview>
            )}

            {localDecision != null && (
                <StatusLine>
                    {localDecision ? <SuccessIcon><CheckCircleIcon size={14}/></SuccessIcon> : <RejectedIcon><CloseCircleOutlineIcon size={14}/></RejectedIcon>}
                    {localDecision ? (
                        <FormattedMessage defaultMessage='Accepted'/>
                    ) : (
                        <FormattedMessage defaultMessage='Rejected'/>
                    )}
                </StatusLine>
            )}
            {isProcessing && localDecision == null && !inFlight && (
                <StatusLine>
                    <SpinnerHolder><LoadingSpinner/></SpinnerHolder>
                    <FormattedMessage defaultMessage='Processing...'/>
                </StatusLine>
            )}

            {showAcceptReject && !isProcessing && (
                <ButtonRow>
                    <PrimaryButton onClick={onApprove}>
                        <FormattedMessage defaultMessage='Accept'/>
                    </PrimaryButton>
                    <SecondaryButton onClick={onReject}>
                        <FormattedMessage defaultMessage='Reject'/>
                    </SecondaryButton>
                </ButtonRow>
            )}
            {showShareDecision && !isProcessing && (
                <ButtonRow>
                    <PrimaryButton onClick={onApprove}>
                        <GlobeIcon size={14}/>
                        <FormattedMessage defaultMessage='Share'/>
                    </PrimaryButton>
                    <SecondaryButton onClick={onReject}>
                        <LockIcon size={14}/>
                        <FormattedMessage defaultMessage='Keep private'/>
                    </SecondaryButton>
                </ButtonRow>
            )}
        </Card>
    );
};

const Card = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px;
    margin-bottom: 4px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 8px;
    background: rgba(var(--center-channel-color-rgb), 0.02);
`;

const Header = styled.div`
    display: flex;
    align-items: flex-start;
    gap: 10px;
`;

const Avatar = styled.img`
    width: 32px;
    height: 32px;
    border-radius: 50%;
    flex-shrink: 0;
`;

const AvatarFallback = styled.div`
    width: 32px;
    height: 32px;
    border-radius: 50%;
    flex-shrink: 0;
    background: rgba(var(--center-channel-color-rgb), 0.12);
`;

const HeaderText = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
`;

const Title = styled.div`
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const Task = styled.div<{$clickable: boolean}>`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    white-space: pre-wrap;
    word-break: break-word;
    cursor: ${(props) => (props.$clickable ? 'pointer' : 'default')};
`;

const StatusLine = styled.div`
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const SpinnerHolder = styled.span`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 14px;
    height: 14px;
`;

const Elapsed = styled.span`
    margin-left: auto;
    font-size: 11px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-variant-numeric: tabular-nums;
`;

const SuccessIcon = styled.span`
    display: inline-flex;
    color: var(--online-indicator);
`;

const ErrorIcon = styled.span`
    display: inline-flex;
    color: var(--error-text);
`;

const WarnIcon = styled.span`
    display: inline-flex;
    color: var(--away-indicator);
`;

const NeutralIcon = styled.span`
    display: inline-flex;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const RejectedIcon = styled.span`
    display: inline-flex;
    color: var(--dnd-indicator);
`;

const ConversationLink = styled.a`
    font-size: 12px;
    font-weight: 600;
    width: fit-content;
`;

const AnswerSection = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
`;

const AnswerToggle = styled.button`
    display: inline-flex;
    align-items: center;
    gap: 4px;
    width: fit-content;
    padding: 0;
    border: none;
    background: transparent;
    cursor: pointer;
    font-size: 12px;
    font-weight: 600;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const AnswerPreview = styled.div`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    white-space: pre-wrap;
    word-break: break-word;
`;

const AnswerBody = styled.div`
    font-size: 13px;
    line-height: 18px;
    color: var(--center-channel-color);
    white-space: pre-wrap;
    word-break: break-word;
    padding: 8px;
    border-radius: 4px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
`;

const ButtonRow = styled.div`
    display: flex;
    gap: 8px;
`;

const PrimaryButton = styled.button`
    display: inline-flex;
    align-items: center;
    gap: 6px;
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
`;

const SecondaryButton = styled(PrimaryButton)`
    background: transparent;
    border: 1px solid rgba(var(--button-bg-rgb), 0.16);
`;

export default DelegationCard;
