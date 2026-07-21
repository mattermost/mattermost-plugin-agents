// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
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
    const {formatMessage} = useIntl();
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
    // while executing. Reconcile from the server so the card survives
    // reloads without depending on event delivery — both for the precise
    // in-flight phase and for terminal cards' agent identity + permalink.
    const inFlight = !terminalPhase && !isPendingApproval && !isRejected;
    const wantsReconcile = inFlight || terminalPhase != null;
    useEffect(() => {
        if (!wantsReconcile || liveUpdate) {
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
    }, [wantsReconcile, liveUpdate, tool.id]);

    // Elapsed timer while in flight. The reconciled server-side creation
    // time always wins over the locally observed start, so a reloaded
    // long-running delegation keeps its true elapsed time.
    const startedAtRef = useRef<number>(0);
    useEffect(() => {
        if (!inFlight) {
            return undefined; // eslint-disable-line no-undefined
        }
        if (reconciled?.created_at) {
            startedAtRef.current = reconciled.created_at;
        } else if (startedAtRef.current === 0) {
            startedAtRef.current = Date.now();
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

    // Exactly one visual state renders on the status line: a persisted
    // rejection and a just-made local decision take precedence over any
    // (stale) phase such as the approval prompt.
    const statusVariant: 'rejected' | 'local_decision' | 'phase' = (() => {
        if (isRejected) {
            return 'rejected';
        }
        if (localDecision != null) {
            return 'local_decision';
        }
        return 'phase';
    })();

    const formatElapsed = (seconds: number): string => {
        const mins = Math.floor(seconds / 60);
        const secs = seconds % 60;
        if (mins === 0) {
            return formatMessage({id: 'ai.delegation.elapsed_seconds', defaultMessage: '{seconds}s'}, {seconds: secs});
        }
        return formatMessage(
            {id: 'ai.delegation.elapsed_minutes_seconds', defaultMessage: '{minutes}m {seconds}s'},
            {minutes: mins, seconds: secs.toString().padStart(2, '0')},
        );
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
                            id='ai.delegation.delegating_to'
                            defaultMessage='Delegating to {agent}'
                            values={{agent: agentDisplayName || (agentUsername ? `@${agentUsername}` : '')}}
                        />
                    </Title>
                    {task !== '' && !taskNeedsClamp && (
                        <Task>{taskText}</Task>
                    )}
                    {task !== '' && taskNeedsClamp && (
                        <TaskToggle
                            type='button'
                            aria-expanded={taskExpanded}
                            onClick={() => setTaskExpanded(!taskExpanded)}
                        >
                            {taskText}
                        </TaskToggle>
                    )}
                </HeaderText>
            </Header>

            <StatusLine data-testid='delegation-status'>
                {statusVariant === 'rejected' && (
                    <>
                        <RejectedIcon><CloseCircleOutlineIcon size={14}/></RejectedIcon>
                        <FormattedMessage
                            id='ai.delegation.rejected'
                            defaultMessage='Rejected'
                        />
                    </>
                )}
                {statusVariant === 'local_decision' && (
                    <>
                        {localDecision ? <SuccessIcon><CheckCircleIcon size={14}/></SuccessIcon> : <RejectedIcon><CloseCircleOutlineIcon size={14}/></RejectedIcon>}
                        {localDecision ? (
                            <FormattedMessage
                                id='ai.delegation.accepted'
                                defaultMessage='Accepted'
                            />
                        ) : (
                            <FormattedMessage
                                id='ai.delegation.rejected'
                                defaultMessage='Rejected'
                            />
                        )}
                    </>
                )}
                {statusVariant === 'phase' && (phase === 'starting' || phase === 'running') && (
                    <>
                        <SpinnerHolder><LoadingSpinner/></SpinnerHolder>
                        {phase === 'starting' && (
                            <FormattedMessage
                                id='ai.delegation.starting'
                                defaultMessage='Starting delegation…'
                            />
                        )}
                        {phase === 'running' && liveUpdate?.activity === 'using_tools' && (
                            <FormattedMessage
                                id='ai.delegation.using_tools'
                                defaultMessage='Using {tools}…'
                                values={{tools: liveUpdate?.tools || ''}}
                            />
                        )}
                        {phase === 'running' && liveUpdate?.activity !== 'using_tools' && (
                            <FormattedMessage
                                id='ai.delegation.working'
                                defaultMessage='Working on the task…'
                            />
                        )}
                        {elapsedSeconds > 0 && <Elapsed>{formatElapsed(elapsedSeconds)}</Elapsed>}
                    </>
                )}
                {statusVariant === 'phase' && phase === 'awaiting_approval' && (
                    <>
                        <NeutralIcon><AlertCircleOutlineIcon size={14}/></NeutralIcon>
                        <FormattedMessage
                            id='ai.delegation.awaiting_approval'
                            defaultMessage='Waiting for your approval to delegate this task'
                        />
                    </>
                )}
                {statusVariant === 'phase' && phase === 'waiting_on_you' && (
                    <>
                        <WarnIcon><AlertCircleOutlineIcon size={14}/></WarnIcon>
                        <strong>
                            <FormattedMessage
                                id='ai.delegation.waiting_on_you'
                                defaultMessage='Waiting on you'
                            />
                        </strong>
                        <FormattedMessage
                            id='ai.delegation.waiting_on_you_detail'
                            defaultMessage='— the agent needs your input in the delegation conversation.'
                        />
                        {elapsedSeconds > 0 && <Elapsed>{formatElapsed(elapsedSeconds)}</Elapsed>}
                    </>
                )}
                {statusVariant === 'phase' && phase === 'completed' && (
                    <>
                        <SuccessIcon><CheckCircleIcon size={14}/></SuccessIcon>
                        <FormattedMessage
                            id='ai.delegation.completed'
                            defaultMessage='Completed'
                        />
                    </>
                )}
                {statusVariant === 'phase' && phase === 'failed' && (
                    <>
                        <ErrorIcon><AlertCircleOutlineIcon size={14}/></ErrorIcon>
                        <FormattedMessage
                            id='ai.delegation.failed'
                            defaultMessage='Failed'
                        />
                    </>
                )}
                {statusVariant === 'phase' && phase === 'timed_out' && (
                    <>
                        <ErrorIcon><AlertCircleOutlineIcon size={14}/></ErrorIcon>
                        <FormattedMessage
                            id='ai.delegation.timed_out'
                            defaultMessage='Timed out'
                        />
                    </>
                )}
            </StatusLine>

            {permalink !== '' && (
                <ConversationLink
                    href={permalink}
                    rel='noreferrer'
                    data-testid='delegation-view-conversation'
                >
                    <FormattedMessage
                        id='ai.delegation.view_conversation'
                        defaultMessage='View conversation'
                    />
                </ConversationLink>
            )}

            {phase === 'completed' && Boolean(tool.result) && (
                <AnswerSection>
                    <AnswerToggle onClick={() => setAnswerExpanded(!answerExpanded)}>
                        {answerExpanded ? <ChevronDownIcon size={14}/> : <ChevronRightIcon size={14}/>}
                        <FormattedMessage
                            id='ai.delegation.answer'
                            defaultMessage='Answer'
                        />
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

            {isProcessing && localDecision == null && !inFlight && (
                <StatusLine>
                    <SpinnerHolder><LoadingSpinner/></SpinnerHolder>
                    <FormattedMessage
                        id='ai.delegation.processing'
                        defaultMessage='Processing...'
                    />
                </StatusLine>
            )}

            {showAcceptReject && !isProcessing && (
                <ButtonRow>
                    <PrimaryButton onClick={onApprove}>
                        <FormattedMessage
                            id='ai.delegation.accept'
                            defaultMessage='Accept'
                        />
                    </PrimaryButton>
                    <SecondaryButton onClick={onReject}>
                        <FormattedMessage
                            id='ai.delegation.reject'
                            defaultMessage='Reject'
                        />
                    </SecondaryButton>
                </ButtonRow>
            )}
            {showShareDecision && !isProcessing && (
                <ButtonRow>
                    <PrimaryButton onClick={onApprove}>
                        <GlobeIcon size={14}/>
                        <FormattedMessage
                            id='ai.delegation.share'
                            defaultMessage='Share'
                        />
                    </PrimaryButton>
                    <SecondaryButton onClick={onReject}>
                        <LockIcon size={14}/>
                        <FormattedMessage
                            id='ai.delegation.keep_private'
                            defaultMessage='Keep private'
                        />
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

const Task = styled.div`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    white-space: pre-wrap;
    word-break: break-word;
`;

const TaskToggle = styled.button`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    white-space: pre-wrap;
    word-break: break-word;
    text-align: left;
    padding: 0;
    border: none;
    background: transparent;
    cursor: pointer;
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
