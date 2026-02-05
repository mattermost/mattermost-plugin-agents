// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {doToolCall, doToolResult} from '@/client';

import {ToolApprovalStage, ToolCall, ToolCallStatus} from './tool_types';
import ToolCard from './tool_card';

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

// Tool call interfaces
interface ToolApprovalSetProps {
    postID: string;
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
    const autoSubmitRef = useRef(false);
    const submitInFlightRef = useRef(false);

    const isCallStage = props.approvalStage === 'call';

    const decisionToolCalls = useMemo(() => {
        if (!props.canApprove) {
            return [];
        }

        if (isCallStage) {
            return props.toolCalls.filter((call) => call.status === ToolCallStatus.Pending);
        }

        return props.toolCalls.filter((call) =>
            call.status === ToolCallStatus.Success ||
            call.status === ToolCallStatus.Error,
        );
    }, [props.toolCalls, props.canApprove, isCallStage]);

    const decisionToolIDSet = useMemo(() => {
        return new Set(decisionToolCalls.map((call) => call.id));
    }, [decisionToolCalls]);

    useEffect(() => {
        setToolDecisions({});
        setIsSubmitting(false);
        setError('');
        autoSubmitRef.current = false;
        submitInFlightRef.current = false;
    }, [props.toolCalls, props.approvalStage]);

    const submitDecisions = useCallback(async (approvedToolIDs: string[]) => {
        if (submitInFlightRef.current) {
            return;
        }

        submitInFlightRef.current = true;
        setIsSubmitting(true);
        try {
            if (isCallStage) {
                await doToolCall(props.postID, approvedToolIDs);
            } else {
                await doToolResult(props.postID, approvedToolIDs);
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
    }, [isCallStage, props.postID]);

    useEffect(() => {
        if (isCallStage || !props.canApprove) {
            return;
        }

        if (decisionToolCalls.length > 0 || props.toolCalls.length === 0) {
            return;
        }

        const allRejected = props.toolCalls.every((call) => call.status === ToolCallStatus.Rejected);
        if (!allRejected) {
            return;
        }

        if (autoSubmitRef.current || isSubmitting || submitInFlightRef.current) {
            return;
        }

        autoSubmitRef.current = true;
        submitDecisions([]);
    }, [decisionToolCalls.length, isCallStage, isSubmitting, props.canApprove, props.postID, props.toolCalls, submitDecisions]);

    const handleToolDecision = async (toolID: string, approved: boolean) => {
        if (!props.canApprove || isSubmitting || submitInFlightRef.current || !decisionToolIDSet.has(toolID)) {
            return;
        }

        const updatedDecisions = {
            ...toolDecisions,
            [toolID]: approved,
        };
        setToolDecisions(updatedDecisions);

        const hasUndecided = decisionToolCalls.some((tool) => {
            return !Object.hasOwn(updatedDecisions, tool.id);
        });

        if (hasUndecided) {
            // If there are still undecided tools, do not submit yet
            return;
        }

        // Submit when all tools are decided

        const approvedToolIDs = decisionToolCalls.
            filter((tool) => {
                return updatedDecisions[tool.id];
            }).
            map((tool) => tool.id);

        submitDecisions(approvedToolIDs);
    };

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

    const nonDecisionToolCalls = props.toolCalls.filter((call) => !decisionToolIDSet.has(call.id));

    // Calculate how many tools are left to decide on
    const undecidedCount = decisionToolCalls.filter((call) => !Object.hasOwn(toolDecisions, call.id)).length;

    // Helper to compute if a tool should be collapsed
    const isToolCollapsed = (tool: ToolCall) => {
        // Pending tools are expanded by default, others are collapsed
        const defaultExpanded = isCallStage ?
            tool.status === ToolCallStatus.Pending :
            tool.status === ToolCallStatus.Success || tool.status === ToolCallStatus.Error;

        // Check if user has toggled this tool
        const isCollapsed = collapsedTools.includes(tool.id);

        // If default is expanded, being in the list means user collapsed it
        // If default is collapsed, being in the list means user expanded it
        return defaultExpanded ? isCollapsed : !isCollapsed;
    };

    return (
        <ToolCallsContainer>
            {decisionToolCalls.map((tool) => (
                <ToolCard
                    key={tool.id}
                    tool={tool}
                    isCollapsed={isToolCollapsed(tool)}
                    isProcessing={isSubmitting}
                    onToggleCollapse={() => toggleCollapse(tool.id)}
                    onApprove={() => handleToolDecision(tool.id, true)}
                    onReject={() => handleToolDecision(tool.id, false)}
                    canExpand={props.canExpand}
                    showArguments={props.showArguments}
                    showResults={props.showResults}
                    approvalStage={props.approvalStage}
                />
            ))}

            {nonDecisionToolCalls.map((tool) => (
                <ToolCard
                    key={tool.id}
                    tool={tool}
                    isCollapsed={isToolCollapsed(tool)}
                    isProcessing={false}
                    onToggleCollapse={() => toggleCollapse(tool.id)}
                    canExpand={props.canExpand}
                    showArguments={props.showArguments}
                    showResults={props.showResults}
                    approvalStage={props.approvalStage}
                />
            ))}

            {/* Only show status bar for multiple decisions */}
            {decisionToolCalls.length > 1 && isSubmitting && (
                <StatusBar>
                    <div>
                        <FormattedMessage
                            id='ai.tool_call.submitting'
                            defaultMessage='Submitting...'
                        />
                    </div>
                </StatusBar>
            )}

            {/* Only show status counter for multiple decisions that haven't been submitted yet */}
            {decisionToolCalls.length > 1 && undecidedCount > 0 && !isSubmitting && (
                <StatusBar>
                    <div>
                        <FormattedMessage
                            id='ai.tool_call.pending_decisions'
                            defaultMessage='{count, plural, =0 {All tools decided} one {# tool needs a decision} other {# tools need decisions}}'
                            values={{count: undecidedCount}}
                        />
                    </div>
                </StatusBar>
            )}
        </ToolCallsContainer>
    );
};

export default ToolApprovalSet;
