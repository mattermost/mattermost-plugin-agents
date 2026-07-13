// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Renderer registry: maps a tool call to the component that renders it. Entries
// are ordered and the first match wins; no match falls back to the generic
// ToolCard. Matching is on canonical tool identity (origin kind + bare name)
// plus a strict parse of the arguments, so a redacted/unexpected payload always
// degrades to the generic field list rather than a broken rich card.

import React from 'react';

import {originKind, bareToolName} from '@/utils/tool_identity';

import {ToolApprovalStage, ToolCall} from '../tool_types';
import ToolCard from '../tool_card';
import QuestionCard, {parseQuestionArgs} from '../question_card';

import {RichCardProps} from './rich_card_parts';
import {
    CreatePostCard, parseCreatePost,
    DmCard, parseDm,
    GroupMessageCard, parseGroupMessage,
    SearchPostsCard, parseSearchPosts,
    SearchUsersCard, parseSearchUsers,
    ReadPostCard, parseReadPost,
    GetChannelInfoCard, parseGetChannelInfo,
} from './rich_cards';

// Everything tool_approval_set knows about a single tool call. Registry entries
// pull the subset they need (rich cards need the ToolCard-style props; the
// QuestionCard entry also needs the question-answer wiring).
export interface ToolRenderContext {
    postID: string;
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

// Map the render context to the props the generic/rich card shell components take.
function toRichProps(ctx: ToolRenderContext): RichCardProps {
    return {
        postID: ctx.postID,
        tool: ctx.tool,
        isCollapsed: ctx.isCollapsed,
        isProcessing: ctx.isProcessing,
        localDecision: ctx.localDecision,
        onToggleCollapse: ctx.onToggleCollapse,
        onApprove: ctx.onApprove,
        onReject: ctx.onReject,
        canExpand: ctx.canExpand,
        showArguments: ctx.showArguments,
        showResults: ctx.showResults,
        approvalStage: ctx.approvalStage,
        isAutoApproved: ctx.isAutoApproved,
    };
}

// Build a registry entry for an embedded Mattermost tool matched by bare name
// plus a successful strict parse.
function embeddedEntry(
    bareName: string,
    parse: (args: ToolCall['arguments']) => unknown,
    Component: React.FC<RichCardProps>,
): RendererEntry {
    return {
        match: (tool) =>
            originKind(tool.server_origin) === 'embedded' &&
            bareToolName(tool) === bareName &&
            parse(tool.arguments) !== null,
        render: (ctx) => <Component {...toRichProps(ctx)}/>,
    };
}

const registry: RendererEntry[] = [

    // QuestionCard: the proving migration. Matches a user-interaction select
    // tool whose arguments parse into a renderable question; a redacted payload
    // yields null and falls through to the generic card (preserving the exact
    // pre-registry behavior).
    {
        match: (tool) =>
            tool.user_interaction === 'select' &&
            parseQuestionArgs(tool.arguments) !== null,
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
    embeddedEntry('create_post', parseCreatePost, CreatePostCard),
    embeddedEntry('dm', parseDm, DmCard),
    embeddedEntry('group_message', parseGroupMessage, GroupMessageCard),
    embeddedEntry('search_posts', parseSearchPosts, SearchPostsCard),
    embeddedEntry('search_users', parseSearchUsers, SearchUsersCard),
    embeddedEntry('read_post', parseReadPost, ReadPostCard),
    embeddedEntry('get_channel_info', parseGetChannelInfo, GetChannelInfoCard),
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
