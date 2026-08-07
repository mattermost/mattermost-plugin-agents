// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ToolCall, ToolCallStatus} from '../tool_types';

import {deriveActivity, isTerminalToolStatus} from './activity_items';
import type {Round} from './turn_content_utils';

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'tc_1',
        name: 'search_tools',
        description: '',
        status: ToolCallStatus.Success,
        ...overrides,
    };
}

function makeRound(id: string, text: string, toolCalls: ToolCall[] = []): Round {
    return {
        id,
        text,
        toolCalls,
        reasoning: {summary: '', signature: ''},
        annotations: [],
    };
}

describe('isTerminalToolStatus', () => {
    test.each([
        [ToolCallStatus.Pending, false],
        [ToolCallStatus.Accepted, false],
        [ToolCallStatus.Rejected, true],
        [ToolCallStatus.Error, true],
        [ToolCallStatus.Success, true],
        [ToolCallStatus.AutoApproved, true],
    ])('status %i is terminal: %s', (status, expected) => {
        expect(isTerminalToolStatus(status)).toBe(expected);
    });
});

describe('deriveActivity', () => {
    // A post that never called a tool must render exactly as it did before
    // the activity area existed: every round is an answer round.
    test('produces no activity for a post without tool calls', () => {
        const rounds = [makeRound('r1', 'just an answer')];
        const activity = deriveActivity(rounds);

        expect(activity.items).toEqual([]);
        expect(activity.activityRounds).toEqual([]);
        expect(activity.answerRounds).toEqual(rounds);
        expect(activity.toolCount).toBe(0);
    });

    test('returns empty activity for no rounds at all', () => {
        const activity = deriveActivity([]);

        expect(activity.items).toEqual([]);
        expect(activity.activityRounds).toEqual([]);
        expect(activity.answerRounds).toEqual([]);
    });

    // The rounds up to and including the last tool round fold into the
    // activity area; everything after it is the answer.
    test('splits rounds at the last round that has tool calls', () => {
        const first = makeRound('r1', 'Let me get the jira tools loaded', [makeTool({id: 'tc_a', name: 'search_tools'})]);
        const second = makeRound('r2', 'Now let me create the Jira ticket', [makeTool({id: 'tc_b', name: 'CreateJiraIssue'})]);
        const answer = makeRound('r3', 'Created MM-1.');

        const activity = deriveActivity([first, second, answer]);

        expect(activity.activityRounds).toEqual([first, second]);
        expect(activity.answerRounds).toEqual([answer]);
    });

    test('flattens intermediate text and tool calls into one chronological list', () => {
        const first = makeRound('r1', 'Let me get the jira tools loaded', [
            makeTool({id: 'tc_a', name: 'search_tools'}),
            makeTool({id: 'tc_b', name: 'load_tool'}),
        ]);
        const second = makeRound('r2', 'Now let me create the Jira ticket', [
            makeTool({id: 'tc_c', name: 'CreateJiraIssue'}),
        ]);
        const answer = makeRound('r3', 'Created MM-1.');

        const activity = deriveActivity([first, second, answer]);

        expect(activity.items.map((item) => item.kind)).toEqual(['text', 'tool', 'tool', 'text', 'tool']);
        expect(activity.items.map((item) => (item.kind === 'text' ? item.text : item.toolCall.id))).toEqual([
            'Let me get the jira tools loaded',
            'tc_a',
            'tc_b',
            'Now let me create the Jira ticket',
            'tc_c',
        ]);
        expect(activity.toolCount).toBe(3);
    });

    // The streaming text of the in-progress round is the answer until a tool
    // call lands in that round; only then does it fold into the activity area.
    test('keeps the trailing round out of the activity area until it has tool calls', () => {
        const toolRound = makeRound('r1', 'looking that up', [makeTool({id: 'tc_a'})]);
        const streaming = makeRound('live', 'here is what I fou');

        const streamingActivity = deriveActivity([toolRound, streaming]);
        expect(streamingActivity.answerRounds).toEqual([streaming]);
        expect(streamingActivity.items).toHaveLength(2);

        const withTool = makeRound('live', 'here is what I found', [makeTool({id: 'tc_b'})]);
        const foldedActivity = deriveActivity([toolRound, withTool]);
        expect(foldedActivity.answerRounds).toEqual([]);
        expect(foldedActivity.items).toHaveLength(4);
    });

    test('omits an empty round text from the item list', () => {
        const activity = deriveActivity([makeRound('r1', '   ', [makeTool({id: 'tc_a'})])]);

        expect(activity.items).toHaveLength(1);
        expect(activity.items[0].kind).toBe('tool');
    });

    // The collapsed row is a single line, so a multi-line snippet must not
    // carry its line breaks into it.
    test('collapses whitespace in an intermediate text snippet', () => {
        const activity = deriveActivity([makeRound('r1', ' Let me\n  look\tthat up ', [makeTool({id: 'tc_a'})])]);

        expect(activity.items[0]).toMatchObject({kind: 'text', text: 'Let me look that up'});
    });

    // Item ids key the animated row, so they must be stable across the status
    // changes that a tool call goes through while it runs.
    test('keys items by round and tool id so a status change does not change the id', () => {
        const pending = deriveActivity([makeRound('r1', 'x', [makeTool({id: 'tc_a', status: ToolCallStatus.Pending})])]);
        const done = deriveActivity([makeRound('r1', 'x', [makeTool({id: 'tc_a', status: ToolCallStatus.Success})])]);

        expect(pending.items.map((item) => item.id)).toEqual(done.items.map((item) => item.id));
        expect(pending.items[1].id).toBe('r1:tool:tc_a');
    });

    test.each([
        {name: 'pending tool', status: ToolCallStatus.Pending, running: true, error: false, rejected: false},
        {name: 'accepted tool', status: ToolCallStatus.Accepted, running: true, error: false, rejected: false},
        {name: 'errored tool', status: ToolCallStatus.Error, running: false, error: true, rejected: false},
        {name: 'rejected tool', status: ToolCallStatus.Rejected, running: false, error: false, rejected: true},
        {name: 'auto-approved tool', status: ToolCallStatus.AutoApproved, running: false, error: false, rejected: false},
    ])('reports outcome flags for a $name', ({status, running, error, rejected}) => {
        const activity = deriveActivity([makeRound('r1', '', [makeTool({id: 'tc_a', status})])]);

        expect(activity.hasRunningTool).toBe(running);
        expect(activity.hasError).toBe(error);
        expect(activity.hasRejected).toBe(rejected);
    });

    // A round the viewer must decide on renders in full below the activity
    // area, so neither it nor its text may be folded into the row.
    test('splits before a round the viewer owes a decision on', () => {
        const first = makeRound('r1', 'Let me look that up', [makeTool({id: 'tc_a', name: 'search_tools'})]);
        const pending = makeRound('r2', 'I will post that', [makeTool({id: 'tc_b', status: ToolCallStatus.Pending})]);

        const activity = deriveActivity([first, pending], 'r2');

        expect(activity.activityRounds).toEqual([first]);
        expect(activity.answerRounds).toEqual([pending]);
        expect(activity.items.map((item) => item.id)).toEqual(['r1:text', 'r1:tool:tc_a']);
        expect(activity.toolCount).toBe(1);
    });

    // Nothing precedes the pending round, so there is no activity area at all
    // and the post renders as a plain message plus its approval card.
    test('produces no activity when the only tool round awaits a decision', () => {
        const pending = makeRound('r1', 'I will post that', [makeTool({id: 'tc_a', status: ToolCallStatus.Pending})]);

        const activity = deriveActivity([pending], 'r1');

        expect(activity.items).toEqual([]);
        expect(activity.activityRounds).toEqual([]);
        expect(activity.answerRounds).toEqual([pending]);
    });

    // Onlookers owe no decision, so the caller passes no id and the pending
    // round folds in exactly like a resolved one.
    test('folds the pending round in when no decision is owed', () => {
        const pending = makeRound('r1', 'I will post that', [makeTool({id: 'tc_a', status: ToolCallStatus.Pending})]);

        const activity = deriveActivity([pending]);

        expect(activity.activityRounds).toEqual([pending]);
        expect(activity.answerRounds).toEqual([]);
        expect(activity.hasRunningTool).toBe(true);
    });

    // Text-only rounds between the last folded tool round and the pending one
    // belong to the answer, not the activity area.
    test('keeps text-only rounds before a pending round out of the activity area', () => {
        const toolRound = makeRound('r1', 'looking', [makeTool({id: 'tc_a'})]);
        const textOnly = makeRound('r2', 'almost there');
        const pending = makeRound('r3', 'I will post that', [makeTool({id: 'tc_b', status: ToolCallStatus.Pending})]);

        const activity = deriveActivity([toolRound, textOnly, pending], 'r3');

        expect(activity.activityRounds).toEqual([toolRound]);
        expect(activity.answerRounds).toEqual([textOnly, pending]);
    });

    // A stale id (the round already resolved and was replaced) must not
    // silently drop the whole activity area.
    test('ignores a pending round id that is not in the list', () => {
        const rounds = [makeRound('r1', 'looking', [makeTool({id: 'tc_a'})])];

        const activity = deriveActivity(rounds, 'gone');

        expect(activity.activityRounds).toEqual(rounds);
        expect(activity.items).toHaveLength(2);
    });

    test('counts tools across every activity round', () => {
        const activity = deriveActivity([
            makeRound('r1', '', [makeTool({id: 'tc_a'}), makeTool({id: 'tc_b'})]),
            makeRound('r2', '', [makeTool({id: 'tc_c', status: ToolCallStatus.Error})]),
            makeRound('r3', 'done'),
        ]);

        expect(activity.toolCount).toBe(3);
        expect(activity.hasError).toBe(true);
        expect(activity.hasRunningTool).toBe(false);
    });
});
