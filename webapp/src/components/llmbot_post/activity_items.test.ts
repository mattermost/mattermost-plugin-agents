// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ToolCallStatus} from '../tool_types';

import {deriveActivity, isTerminalToolStatus} from './activity_items';
import {makeRound, makeServerTool, makeTool} from './test_support';

describe('isTerminalToolStatus', () => {
    test.each([
        [ToolCallStatus.Pending, false],
        [ToolCallStatus.Accepted, false],
        [ToolCallStatus.Rejected, true],
        [ToolCallStatus.Error, true],
        [ToolCallStatus.Success, true],
        [ToolCallStatus.AutoApproved, true],
    ])('status %s is terminal: %s', (status, expected) => {
        expect(isTerminalToolStatus(status)).toBe(expected);
    });
});

describe('deriveActivity round split', () => {
    const narration = makeRound('r1', 'Let me look that up', [makeTool({id: 'tc_a'})]);
    const secondTool = makeRound('r2', 'Now the next one', [makeTool({id: 'tc_b'})]);
    const answer = makeRound('r3', 'Here is the answer');
    const serverThenAnswer = makeRound('s1', 'Found it: 42', [], [makeServerTool({id: 'srv_a'})]);
    const serverOnly = makeRound('s2', '', [], [makeServerTool({id: 'srv_b'})]);

    test.each([
        {name: 'no rounds', rounds: [], activity: [], answer: []},
        {name: 'no tools', rounds: [answer], activity: [], answer: ['r3']},
        {name: 'splits after the last tool round', rounds: [narration, secondTool, answer], activity: ['r1', 'r2'], answer: ['r3']},
        {name: 'trailing streamed text stays the answer', rounds: [narration, answer], activity: ['r1'], answer: ['r3']},
        {name: 'text of a round whose last tool is a client call is narration', rounds: [narration], activity: ['r1'], answer: []},
        {name: 'server-tool round without text folds whole', rounds: [serverOnly, answer], activity: ['s2'], answer: ['r3']},
        {name: 'text after server tools in an earlier round is narration', rounds: [serverThenAnswer, serverOnly], activity: ['s1', 's2'], answer: []},
    ])('$name', ({rounds, activity, answer: answerIds}) => {
        const result = deriveActivity(rounds);

        expect(result.activityRounds.map((round) => round.id)).toEqual(activity);
        expect(result.answerRounds.map((round) => round.id)).toEqual(answerIds);
    });

    test('keeps the text after the last server tools as the answer', () => {
        const result = deriveActivity([serverThenAnswer]);

        expect(result.activityRounds).toHaveLength(1);
        expect(result.activityRounds[0].text).toBe('');
        expect(result.activityRounds[0].serverTools).toEqual(serverThenAnswer.serverTools);
        expect(result.answerRounds).toHaveLength(1);
        expect(result.answerRounds[0].text).toBe('Found it: 42');
        expect(result.answerRounds[0].serverTools).toEqual([]);
    });

    test('returns the same split parts for an unchanged round', () => {
        const first = deriveActivity([serverThenAnswer]);
        const second = deriveActivity([serverThenAnswer]);

        expect(second.activityRounds[0]).toBe(first.activityRounds[0]);
        expect(second.answerRounds[0]).toBe(first.answerRounds[0]);
    });
});

describe('deriveActivity with a pending decision', () => {
    const meta = makeRound('r1', '', [makeTool({id: 'tc_meta'})]);
    const bridge = makeRound('r2', 'Almost there');
    const pending = makeRound('r3', 'I will post that', [makeTool({id: 'tc_post', status: ToolCallStatus.Pending})]);

    test.each([
        {name: 'splits before the pending round', rounds: [meta, pending], activity: ['r1'], answer: ['r3']},
        {name: 'no activity when the only tool round is pending', rounds: [pending], activity: [], answer: ['r3']},
        {name: 'text before the pending round stays out', rounds: [meta, bridge, pending], activity: ['r1'], answer: ['r2', 'r3']},
    ])('$name', ({rounds, activity, answer}) => {
        const result = deriveActivity(rounds, {pendingDecisionRoundId: 'r3'});

        expect(result.activityRounds.map((round) => round.id)).toEqual(activity);
        expect(result.answerRounds.map((round) => round.id)).toEqual(answer);
    });

    test('ignores a pending round id that is not in the list', () => {
        const result = deriveActivity([meta, pending], {pendingDecisionRoundId: 'missing'});

        expect(result.activityRounds.map((round) => round.id)).toEqual(['r1', 'r3']);
    });
});

describe('deriveActivity items', () => {
    test('lists server tools before client tools within a round, in round order', () => {
        const result = deriveActivity([
            makeRound('r1', 'narration', [makeTool({id: 'tc_a'})], [makeServerTool({id: 'srv_a'})]),
            makeRound('r2', '', [makeTool({id: 'tc_b'}), makeTool({id: 'tc_c'})]),
        ]);

        expect(result.items.map((item) => item.id)).toEqual(['server:srv_a', 'tool:tc_a', 'tool:tc_b', 'tool:tc_c']);
    });

    test('keys items by invocation so live and persisted rounds agree', () => {
        const live = deriveActivity([makeRound('live', '', [makeTool({id: 'tc_a', status: ToolCallStatus.Pending})])]);
        const persisted = deriveActivity([makeRound('turn_1', '', [makeTool({id: 'tc_a'})])]);

        expect(live.items[0].id).toBe(persisted.items[0].id);
    });

    test.each([
        {name: 'client tool running', tool: makeTool({status: ToolCallStatus.Pending}), running: true, error: false, rejected: false},
        {name: 'client tool accepted', tool: makeTool({status: ToolCallStatus.Accepted}), running: true, error: false, rejected: false},
        {name: 'client tool failed', tool: makeTool({status: ToolCallStatus.Error}), running: false, error: true, rejected: false},
        {name: 'client tool rejected', tool: makeTool({status: ToolCallStatus.Rejected}), running: false, error: false, rejected: true},
        {name: 'server tool left in progress', server: makeServerTool({status: 'in_progress'}), running: false, error: false, rejected: false},
        {name: 'server tool failed', server: makeServerTool({status: 'error'}), running: false, error: true, rejected: false},
        {name: 'server tool done', server: makeServerTool({status: 'success'}), running: false, error: false, rejected: false},
    ])('reports status for $name', ({tool, server, running, error, rejected}) => {
        const result = deriveActivity([makeRound('r1', '', tool ? [tool] : [], server ? [server] : [])]);

        expect(result.hasRunningTool).toBe(running);
        expect(result.hasError).toBe(error);
        expect(result.hasRejected).toBe(rejected);
    });
});
