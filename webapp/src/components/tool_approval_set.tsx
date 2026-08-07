// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';

import type {ClientError} from '@mattermost/client';

import {GlobalState} from '@mattermost/types/store';

import {doAskUserCancel, doToolCall, doToolResult} from '@/client';
import {invalidateConversation} from '@/hooks/use_conversation';

import {AskAnotherUserToolName, ToolAnswer, ToolApprovalStage, ToolCall, ToolCallStatus, UserInteractionSelect} from './tool_types';
import ToolCard, {type AskCancelState} from './tool_card';
import QuestionCard, {parseQuestionArgs} from './question_card';

// Styled components
const ToolCallsContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-bottom: 12px;
	margin-top: 8px;
`;

const StatusBar = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 12px;
    margin-top: 8px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border-radius: 4px;
    font-size: 12px;
`;

const BatchButtonContainer = styled.div`
    display: flex;
    gap: 8px;
`;

const BatchButton = styled.button`
    background: rgba(var(--button-bg-rgb), 0.08);
    color: var(--button-bg);
    border: none;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 11px;
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

// Tool call interfaces
interface ToolApprovalSetProps {
    postID: string;
    conversationID?: string;
    toolCalls: ToolCall[];
    approvalStage: ToolApprovalStage;
    canApprove: boolean;
    canExpand: boolean;
    showArguments: boolean;
    showResults: boolean;
}

// Define a type for tool decisions
type ToolDecision = {
    [toolId: string]: boolean; // true = approved, false = rejected
};

const ToolApprovalSet: React.FC<ToolApprovalSetProps> = (props) => {
    const {formatMessage} = useIntl();

    // Track which tools are currently being processed
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [error, setError] = useState('');

    // Track collapsed state for each tool
    const [collapsedTools, setCollapsedTools] = useState<string[]>([]);
    const [toolDecisions, setToolDecisions] = useState<ToolDecision>({});
    const submitInFlightRef = useRef(false);
    const toolDecisionsRef = useRef<ToolDecision>({});

    // Structured answers for accepted user-interaction tools, keyed by tool
    // call ID. Sent as tool_answers alongside accepted_tool_ids.
    const toolAnswersRef = useRef<Record<string, ToolAnswer>>({});

    // Cancel state per waiting AskAnotherUser call (F5). A successful cancel
    // stays 'submitting' until the conversation refetch removes the waiting
    // card, so the control cannot be clicked twice.
    const [askCancelStates, setAskCancelStates] = useState<Record<string, AskCancelState>>({});

    // The anchor post's author is the conversation bot; its username rides
    // along on the cancel request (same rationale as doAskUserResponse).
    const botUsername = useSelector<GlobalState, string | undefined>((state) => {
        const authorID = state.entities.posts.posts[props.postID]?.user_id;
        return authorID ? state.entities.users.profiles[authorID]?.username : undefined; // eslint-disable-line no-undefined
    });

    const isCallStage = props.approvalStage === 'call';
    const isResultStage = props.approvalStage === 'result';
    const pendingToolCalls = useMemo(() => {
        return props.toolCalls.filter((call) => call.status === ToolCallStatus.Pending);
    }, [props.toolCalls]);
    const isInterruptedAutoRound = isCallStage &&
        pendingToolCalls.length > 0 &&
        pendingToolCalls.every((call) => call.would_auto_execute);

    // Approval is per pending tool. Earlier auto-approved tools in the same
    // response should not suppress controls for later manual ones.
    const effectiveCanApprove = props.canApprove;

    const decisionToolCalls = useMemo(() => {
        if (!effectiveCanApprove) {
            return [];
        }

        if (isCallStage) {
            // Calls that passed the auto-execution policy run server-side
            // once the rest of the batch is resolved — no decision needed.
            return props.toolCalls.filter((call) =>
                call.status === ToolCallStatus.Pending && !call.would_auto_execute,
            );
        }

        if (!isResultStage) {
            // 'done' stage — server says no decision remains, render no buttons.
            return [];
        }

        // User-interaction results are decided at answer time (the user
        // authored them) and auto-executed results are decided at write time,
        // so neither needs a share/keep-private decision.
        return props.toolCalls.filter((call) =>
            !call.user_interaction &&
            !call.decided &&
            (call.status === ToolCallStatus.Success ||
            call.status === ToolCallStatus.Error ||
            call.status === ToolCallStatus.AutoApproved),
        );
    }, [props.toolCalls, effectiveCanApprove, isCallStage, isResultStage]);

    const decisionToolIDSet = useMemo(() => {
        return new Set(decisionToolCalls.map((call) => call.id));
    }, [decisionToolCalls]);

    useEffect(() => {
        setToolDecisions({});
        setIsSubmitting(false);
        setError('');
        setAskCancelStates({});
        submitInFlightRef.current = false;
        toolDecisionsRef.current = {};
        toolAnswersRef.current = {};
    }, [props.toolCalls, props.approvalStage]);

    const submitDecisions = useCallback(async (approvedToolIDs: string[]) => {
        if (submitInFlightRef.current) {
            return;
        }

        submitInFlightRef.current = true;
        setIsSubmitting(true);
        try {
            if (isCallStage) {
                const answers: Record<string, ToolAnswer> = {};
                for (const id of approvedToolIDs) {
                    if (toolAnswersRef.current[id]) {
                        answers[id] = toolAnswersRef.current[id];
                    }
                }
                await doToolCall(props.postID, approvedToolIDs, answers);
            } else {
                await doToolResult(props.postID, approvedToolIDs);
            }

            // The channel path for Accept does not stream a follow-up (that
            // happens on Share). Force a refetch so the UI transitions from
            // 'call' to 'result' stage without waiting for a WebSocket event.
            if (props.conversationID) {
                invalidateConversation(props.conversationID);
            }
            setIsSubmitting(false);
        } catch (err) {
            setError(formatMessage({
                id: 'ai.tool_call.submit_failed',
                defaultMessage: 'Failed to submit tool decisions',
            }));
            setIsSubmitting(false);
        } finally {
            submitInFlightRef.current = false;
        }
    }, [isCallStage, props.postID, props.conversationID]);

    const handleToolDecision = useCallback((toolID: string, approved: boolean) => {
        if (!effectiveCanApprove || isSubmitting || submitInFlightRef.current || !decisionToolIDSet.has(toolID)) {
            return;
        }

        const updatedDecisions = {
            ...toolDecisionsRef.current,
            [toolID]: approved,
        };
        toolDecisionsRef.current = updatedDecisions;
        setToolDecisions(updatedDecisions);

        const hasUndecided = decisionToolCalls.some((tool) => {
            return !Object.hasOwn(updatedDecisions, tool.id);
        });

        if (hasUndecided) {
            return;
        }

        const approvedToolIDs = decisionToolCalls.
            filter((tool) => {
                return updatedDecisions[tool.id];
            }).
            map((tool) => tool.id);

        submitDecisions(approvedToolIDs);
    }, [effectiveCanApprove, isSubmitting, decisionToolIDSet, decisionToolCalls, submitDecisions]);

    const handleAskCancel = useCallback(async (toolID: string) => {
        if (!botUsername || askCancelStates[toolID] === 'submitting') {
            return;
        }
        setAskCancelStates((prev) => ({...prev, [toolID]: 'submitting'}));
        try {
            await doAskUserCancel(props.postID, botUsername, {tool_use_id: toolID});
            if (props.conversationID) {
                invalidateConversation(props.conversationID);
            }

            // Stay 'submitting': the refetched conversation removes the
            // waiting card (or renders the terminal state) and the state
            // reset effect clears this entry.
        } catch (err) {
            if ((err as ClientError).status_code === 409) {
                // The question was resolved by a racing answer/decline —
                // not an error; the refetched terminal state settles the UI.
                if (props.conversationID) {
                    invalidateConversation(props.conversationID);
                }
            } else {
                setAskCancelStates((prev) => ({...prev, [toolID]: 'error'}));
            }
        }
    }, [botUsername, askCancelStates, props.postID, props.conversationID]);

    const handleQuestionAnswer = useCallback((toolID: string, selections: string[], custom: string) => {
        const answer: ToolAnswer = custom ? {selected: selections, custom} : {selected: selections};
        toolAnswersRef.current = {
            ...toolAnswersRef.current,
            [toolID]: answer,
        };
        handleToolDecision(toolID, true);
    }, [handleToolDecision]);

    const handleBatchDecision = useCallback((approved: boolean) => {
        if (!effectiveCanApprove || isSubmitting || submitInFlightRef.current) {
            return;
        }

        const updatedDecisions = {...toolDecisionsRef.current};
        for (const tool of decisionToolCalls) {
            // Questions cannot be batch-decided: an answer (or explicit skip)
            // is required per question.
            if (isCallStage && tool.user_interaction) {
                continue;
            }
            updatedDecisions[tool.id] = approved;
        }
        toolDecisionsRef.current = updatedDecisions;
        setToolDecisions(updatedDecisions);

        // Submitting marks every undecided tool as rejected server-side, so
        // wait for the remaining questions to be answered or skipped first.
        const hasUndecided = decisionToolCalls.some((tool) => {
            return !Object.hasOwn(updatedDecisions, tool.id);
        });
        if (hasUndecided) {
            return;
        }

        const approvedToolIDs = decisionToolCalls.
            filter((tool) => {
                return updatedDecisions[tool.id];
            }).
            map((tool) => tool.id);

        submitDecisions(approvedToolIDs);
    }, [effectiveCanApprove, isSubmitting, isCallStage, decisionToolCalls, submitDecisions]);

    const toggleCollapse = (toolID: string) => {
        setCollapsedTools((prev) =>
            (prev.includes(toolID) ? prev.filter((id) => id !== toolID) : [...prev, toolID]),
        );
    };

    if (props.toolCalls.length === 0) {
        return null;
    }

    if (error) {
        return <div className='error'>{error}</div>;
    }

    // The "N tools need decisions" bar and batch buttons only make sense for
    // approval-type decisions; questions are self-describing cards that must
    // be answered (or skipped) individually.
    const approvalDecisionCalls = decisionToolCalls.filter((call) => !call.user_interaction);
    const undecidedApprovalCount = approvalDecisionCalls.filter((call) => !Object.hasOwn(toolDecisions, call.id)).length;

    // Helper to compute if a tool should be collapsed
    const isToolCollapsed = (tool: ToolCall) => {
        // Auto-approved tools are always collapsed by default — the user
        // did not interact with them, so the expanded card would just be
        // visual noise. Click still toggles.
        if (tool.status === ToolCallStatus.AutoApproved) {
            return !collapsedTools.includes(tool.id);
        }

        // Pending tools (call stage) expand by default so users see what
        // they are being asked to approve. Executed tools in the result
        // stage also expand so the output is visible during the share
        // decision. Otherwise collapse.
        const defaultExpanded = isCallStage ?
            tool.status === ToolCallStatus.Pending :
            isResultStage && (tool.status === ToolCallStatus.Success ||
                tool.status === ToolCallStatus.Error);

        // Check if user has toggled this tool
        const isCollapsed = collapsedTools.includes(tool.id);

        // If default is expanded, being in the list means user collapsed it
        // If default is collapsed, being in the list means user expanded it
        return defaultExpanded ? isCollapsed : !isCollapsed;
    };

    return (
        <ToolCallsContainer>
            {props.toolCalls.map((tool) => {
                const isDecisionCall = decisionToolIDSet.has(tool.id);

                // In a mixed approval batch, policy-approved calls stay
                // hidden until the user's decisions let the server run them.
                // Live calls and interrupted all-auto rounds remain visible.
                if (tool.status === ToolCallStatus.Pending &&
                    tool.would_auto_execute &&
                    isCallStage &&
                    !isInterruptedAutoRound) {
                    return null;
                }

                if (tool.user_interaction === UserInteractionSelect) {
                    // Redacted calls (non-requesters) have no arguments to
                    // render; fall through to the generic tool card.
                    const question = parseQuestionArgs(tool.arguments);
                    if (question) {
                        return (
                            <QuestionCard
                                key={tool.id}
                                tool={tool}
                                question={question}
                                isProcessing={isDecisionCall && isSubmitting}
                                localDecision={isDecisionCall ? toolDecisions[tool.id] : undefined} // eslint-disable-line no-undefined
                                canAnswer={isDecisionCall && isCallStage}
                                onAnswer={isDecisionCall ? (selections, custom) => handleQuestionAnswer(tool.id, selections, custom) : undefined} // eslint-disable-line no-undefined
                                onSkip={isDecisionCall ? () => handleToolDecision(tool.id, false) : undefined} // eslint-disable-line no-undefined
                            />
                        );
                    }
                }

                // Requester-only cancel control for outstanding
                // AskAnotherUser questions (F5). Observers never see it.
                const canCancelAsk = props.canApprove &&
                    tool.name === AskAnotherUserToolName &&
                    tool.status === ToolCallStatus.Waiting;

                return (
                    <ToolCard
                        key={tool.id}
                        postID={props.postID}
                        tool={tool}
                        isCollapsed={isToolCollapsed(tool)}
                        isProcessing={(isDecisionCall || isInterruptedAutoRound) && isSubmitting}
                        localDecision={isDecisionCall ? toolDecisions[tool.id] : undefined} // eslint-disable-line no-undefined
                        onToggleCollapse={() => toggleCollapse(tool.id)}
                        onApprove={isDecisionCall ? () => handleToolDecision(tool.id, true) : undefined} // eslint-disable-line no-undefined
                        onReject={isDecisionCall ? () => handleToolDecision(tool.id, false) : undefined} // eslint-disable-line no-undefined
                        canExpand={props.canExpand}
                        showArguments={props.showArguments}
                        showResults={props.showResults}
                        approvalStage={props.approvalStage}
                        isAutoApproved={tool.status === ToolCallStatus.AutoApproved}
                        onCancelAsk={canCancelAsk ? () => handleAskCancel(tool.id) : undefined} // eslint-disable-line no-undefined
                        askCancelState={canCancelAsk ? (askCancelStates[tool.id] ?? 'idle') : undefined} // eslint-disable-line no-undefined
                    />
                );
            })}

            {/* Only show status bar for multiple approval decisions */}
            {approvalDecisionCalls.length > 1 && isSubmitting && (
                <StatusBar>
                    <div>
                        <FormattedMessage
                            id='ai.tool_call.submitting'
                            defaultMessage='Submitting...'
                        />
                    </div>
                </StatusBar>
            )}

            {approvalDecisionCalls.length > 1 && undecidedApprovalCount > 0 && !isSubmitting && (
                <StatusBar>
                    <div>
                        <FormattedMessage
                            id='ai.tool_call.pending_decisions'
                            defaultMessage='{count, plural, =0 {All tools decided} one {# tool needs a decision} other {# tools need decisions}}'
                            values={{count: undecidedApprovalCount}}
                        />
                    </div>
                    <BatchButtonContainer>
                        <BatchButton
                            type='button'
                            onClick={() => handleBatchDecision(true)}
                        >
                            <FormattedMessage
                                id='ai.tool_call.accept_all'
                                defaultMessage='Accept all'
                            />
                        </BatchButton>
                        <BatchButton
                            type='button'
                            onClick={() => handleBatchDecision(false)}
                        >
                            <FormattedMessage
                                id='ai.tool_call.reject_all'
                                defaultMessage='Reject all'
                            />
                        </BatchButton>
                    </BatchButtonContainer>
                </StatusBar>
            )}

            {isInterruptedAutoRound && effectiveCanApprove && (
                <StatusBar>
                    {isSubmitting ? (
                        <div>
                            <FormattedMessage
                                id='ai.tool_call.submitting'
                                defaultMessage='Submitting...'
                            />
                        </div>
                    ) : (
                        <BatchButton
                            type='button'
                            onClick={() => submitDecisions([])}
                        >
                            <FormattedMessage
                                id='ai.tool_call.run_tools'
                                defaultMessage='Run tools'
                            />
                        </BatchButton>
                    )}
                </StatusBar>
            )}
        </ToolCallsContainer>
    );
};

export default ToolApprovalSet;
