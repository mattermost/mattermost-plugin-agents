// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {doToolCall} from '@/client';

import {ToolCall, ToolCallStatus} from './llmbot_post/llmbot_post';
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
}

// Define a type for tool decisions
type ToolDecision = {
    [toolId: string]: boolean | null; // true = approved, false = rejected, null = undecided
};

const ToolApprovalSet: React.FC<ToolApprovalSetProps> = (props) => {
    // Track which tools are currently being processed
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [error, setError] = useState('');

    // Track collapsed state for each tool
    const [collapsedTools, setCollapsedTools] = useState<string[]>([]);
    const [toolDecisions, setToolDecisions] = useState<ToolDecision>({});

    const handleToolDecision = async (toolID: string, approved: boolean) => {
        if (isSubmitting) {
            return;
        }

        const updatedDecisions = {
            ...toolDecisions,
            [toolID]: approved,
        };
        setToolDecisions(updatedDecisions);

        const hasUndecided = props.toolCalls.some((tool) => {
            return !Object.hasOwn(updatedDecisions, tool.id) || updatedDecisions[tool.id] === null;
        });

        if (hasUndecided) {
            // If there are still undecided tools, do not submit yet
            return;
        }

        // Submit when all tools are decided

        const approvedToolIDs = Object.entries(updatedDecisions).
            filter(([, isApproved]) => {
                return isApproved;
            }).
            map(([id]) => id);

        setIsSubmitting(true);
        try {
            await doToolCall(props.postID, approvedToolIDs);
        } catch (err) {
            setError('Failed to submit tool decisions');
            setIsSubmitting(false);
        }
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

    // Get pending tool calls
    const pendingToolCalls = props.toolCalls.filter((call) => call.status === ToolCallStatus.Pending);

    // Get processed tool calls
    const processedToolCalls = props.toolCalls.filter((call) => call.status !== ToolCallStatus.Pending);

    // Calculate how many tools are left to decide on
    const undecidedCount = Object.values(toolDecisions).filter((decision) => decision === null).length;

    // Helper to compute if a tool should be collapsed
    const isToolCollapsed = (tool: ToolCall) => {
        // Pending tools are expanded by default, others are collapsed
        const defaultExpanded = tool.status === ToolCallStatus.Pending;

        // Check if user has toggled this tool
        const isCollapsed = collapsedTools.includes(tool.id);

        // If default is expanded, being in the list means user collapsed it
        // If default is collapsed, being in the list means user expanded it
        return defaultExpanded ? isCollapsed : !isCollapsed;
    };

    return (
        <ToolCallsContainer>
            {pendingToolCalls.map((tool) => (
                <ToolCard
                    key={tool.id}
                    tool={tool}
                    isCollapsed={isToolCollapsed(tool)}
                    isProcessing={isSubmitting}
                    onToggleCollapse={() => toggleCollapse(tool.id)}
                    onApprove={() => handleToolDecision(tool.id, true)}
                    onReject={() => handleToolDecision(tool.id, false)}
                />
            ))}

            {processedToolCalls.map((tool) => (
                <ToolCard
                    key={tool.id}
                    tool={tool}
                    isCollapsed={isToolCollapsed(tool)}
                    isProcessing={false}
                    onToggleCollapse={() => toggleCollapse(tool.id)}
                />
            ))}

            {/* Only show status bar for multiple pending tools */}
            {pendingToolCalls.length > 1 && isSubmitting && (
                <StatusBar>
                    <div>
                        <FormattedMessage
                            id='ai.tool_call.submitting'
                            defaultMessage='Submitting...'
                        />
                    </div>
                </StatusBar>
            )}

            {/* Only show status counter for multiple pending tools that haven't been submitted yet */}
            {pendingToolCalls.length > 1 && undecidedCount > 0 && !isSubmitting && (
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
