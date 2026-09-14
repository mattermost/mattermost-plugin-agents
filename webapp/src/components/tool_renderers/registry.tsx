// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Renderer registry: maps a tool call to the component that renders it. First
// match wins; no match falls back to the generic ToolCard. Matched components
// strictly parse their arguments and render the generic ToolCard themselves on
// any mismatch, so redacted/unexpected payloads never break a rich card.

import React from 'react';

import {originKind, bareToolName} from '@/utils/tool_identity';

import {ToolApprovalStage, ToolCall, UserInteractionSelect} from '../tool_types';
import ToolCard from '../tool_card';
import QuestionCard, {parseQuestionArgs} from '../question_card';

import {RichCardProps} from './tool_card_shell';
import ReadPostPreviewCard from './posts/read_post';
import CreatePostPreviewCard from './posts/create_post';

// Everything ToolApprovalSet knows about a single tool call; entries pull the
// subset they need.
export interface ToolRenderContext {
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
    approvalStage: ToolApprovalStage;
    isAutoApproved: boolean;

    // User-interaction (question) wiring.
    canAnswer: boolean;
    onAnswer?: (selections: string[], custom: string) => void;
    onSkip?: () => void;
}

interface RendererEntry {
    match: (tool: ToolCall) => boolean;
    render: (ctx: ToolRenderContext) => React.ReactNode;
}

// Strip the question wiring; everything else is card props.
function toRichProps(ctx: ToolRenderContext): RichCardProps {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const {canAnswer, onAnswer, onSkip, ...cardProps} = ctx;
    return cardProps;
}

// Build a registry entry for an embedded Mattermost tool. The card itself
// falls back to the generic ToolCard when its argument parse fails, so the
// matcher only needs identity.
function embeddedEntry(bareName: string, Component: React.FC<RichCardProps>): RendererEntry {
    return {
        match: (tool) =>
            originKind(tool.server_origin) === 'embedded' &&
            bareToolName(tool) === bareName,
        render: (ctx) => <Component {...toRichProps(ctx)}/>,
    };
}

const registry: RendererEntry[] = [

    // QuestionCard: a select-interaction tool whose arguments parse into a
    // renderable question. Redacted payloads render the generic card.
    {
        match: (tool) => tool.user_interaction === UserInteractionSelect,
        render: (ctx) => {
            const question = parseQuestionArgs(ctx.tool.arguments);
            if (!question) {
                return <ToolCard {...toRichProps(ctx)}/>;
            }
            return (
                <QuestionCard
                    tool={ctx.tool}
                    question={question}
                    isProcessing={ctx.isProcessing}
                    localDecision={ctx.localDecision}
                    canAnswer={ctx.canAnswer}
                    onAnswer={ctx.onAnswer}
                    onSkip={ctx.onSkip}
                />
            );
        },
    },
    embeddedEntry('read_post', ReadPostPreviewCard),
    embeddedEntry('create_post', CreatePostPreviewCard),
];

/**
 * Render a single tool call: the first matching registry entry, or the generic
 * ToolCard when nothing matches.
 */
export function renderToolCall(ctx: ToolRenderContext): React.ReactNode {
    const entry = registry.find((e) => e.match(ctx.tool));
    if (entry) {
        return entry.render(ctx);
    }
    return <ToolCard {...toRichProps(ctx)}/>;
}
