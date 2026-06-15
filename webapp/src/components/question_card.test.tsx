// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import QuestionCard, {parseQuestionArgs, QuestionArgs} from './question_card';
import {ToolCall, ToolCallStatus} from './tool_types';

function makeTool(overrides: Partial<ToolCall> = {}): ToolCall {
    return {
        id: 'q_1',
        name: 'AskUserQuestion',
        description: '',
        status: ToolCallStatus.Pending,
        user_interaction: 'select',
        ...overrides,
    };
}

function makeQuestion(overrides: Partial<QuestionArgs> = {}): QuestionArgs {
    return {
        question: 'Which channel should I post in?',
        options: [{label: 'UX Design'}, {label: 'Design team'}, {label: 'Product'}],
        multiSelect: false,
        ...overrides,
    };
}

function renderCard(props: Partial<React.ComponentProps<typeof QuestionCard>> = {}) {
    return render(
        <IntlProvider locale='en'>
            <QuestionCard
                tool={props.tool ?? makeTool()}
                question={props.question ?? makeQuestion()}
                isProcessing={props.isProcessing ?? false}
                localDecision={props.localDecision}
                canAnswer={props.canAnswer ?? true}
                onAnswer={props.onAnswer}
                onSkip={props.onSkip}
            />
        </IntlProvider>,
    );
}

describe('parseQuestionArgs', () => {
    test('parses a well-formed single-select question', () => {
        const parsed = parseQuestionArgs({
            question: 'Pick one',
            options: [{label: 'A', description: 'first'}, {label: 'B'}],
        });

        // toEqual treats an absent key as equal to an undefined one, so the
        // second option (no description key) asserts none was parsed.
        expect(parsed).toEqual({
            question: 'Pick one',
            options: [{label: 'A', description: 'first'}, {label: 'B'}],
            multiSelect: false,
        });
    });

    test('reads multi_select into multiSelect', () => {
        const parsed = parseQuestionArgs({
            question: 'Pick some',
            options: [{label: 'A'}],
            multi_select: true,
        });
        expect(parsed?.multiSelect).toBe(true);
    });

    test.each([
        ['null arguments (redacted for non-requesters)', null],
        ['array arguments', [{label: 'A'}]],
        ['missing question', {options: [{label: 'A'}]}],
        ['empty question', {question: '', options: [{label: 'A'}]}],
        ['missing options', {question: 'Q?'}],
        ['empty options', {question: 'Q?', options: []}],
        ['option without a label', {question: 'Q?', options: [{description: 'no label'}]}],
        ['option with an empty label', {question: 'Q?', options: [{label: ''}]}],
        ['non-object option', {question: 'Q?', options: ['A']}],
    ])('returns null for %s', (_label, args) => {
        expect(parseQuestionArgs(args as ToolCall['arguments'])).toBeNull();
    });
});

describe('QuestionCard', () => {
    test('renders the question and every option', () => {
        renderCard();
        expect(screen.getByText('Which channel should I post in?')).not.toBeNull();
        expect(screen.getByText('UX Design')).not.toBeNull();
        expect(screen.getByText('Design team')).not.toBeNull();
        expect(screen.getByText('Product')).not.toBeNull();
    });

    test('single-select answers with exactly the clicked option', () => {
        const onAnswer = jest.fn();
        renderCard({onAnswer, onSkip: jest.fn()});

        fireEvent.click(screen.getByText('UX Design'));
        fireEvent.click(screen.getByText('Design team')); // replaces the prior choice
        fireEvent.click(screen.getByText('Accept'));

        expect(onAnswer).toHaveBeenCalledWith(['Design team']);
    });

    test('multi-select accumulates and toggles options off', () => {
        const onAnswer = jest.fn();
        renderCard({question: makeQuestion({multiSelect: true}), onAnswer, onSkip: jest.fn()});

        fireEvent.click(screen.getByText('UX Design'));
        fireEvent.click(screen.getByText('Product'));
        fireEvent.click(screen.getByText('UX Design')); // toggle the first back off
        fireEvent.click(screen.getByText('Accept'));

        expect(onAnswer).toHaveBeenCalledWith(['Product']);
    });

    test('Accept is disabled until something is selected', () => {
        renderCard({onAnswer: jest.fn(), onSkip: jest.fn()});
        const accept = screen.getByText('Accept').closest('button') as HTMLButtonElement;
        expect(accept.disabled).toBe(true);
    });

    test('Skip calls onSkip without selecting anything', () => {
        const onSkip = jest.fn();
        const onAnswer = jest.fn();
        renderCard({onAnswer, onSkip});

        fireEvent.click(screen.getByText('Skip'));

        expect(onSkip).toHaveBeenCalledTimes(1);
        expect(onAnswer).not.toHaveBeenCalled();
    });

    test('renders no controls and an Answered status for a resolved question', () => {
        renderCard({
            tool: makeTool({status: ToolCallStatus.Success, result: '{"selected":["Product"]}'}),
            onAnswer: jest.fn(),
            onSkip: jest.fn(),
        });

        expect(screen.queryByText('Accept')).toBeNull();
        expect(screen.queryByText('Skip')).toBeNull();
        expect(screen.getByText('Answered')).not.toBeNull();
    });

    test('shows a Skipped status for a declined question', () => {
        renderCard({
            tool: makeTool({status: ToolCallStatus.Rejected}),
            onAnswer: jest.fn(),
            onSkip: jest.fn(),
        });
        expect(screen.getByText('Skipped')).not.toBeNull();
        expect(screen.queryByText('Accept')).toBeNull();
    });

    test('shows a waiting status and no controls when the viewer cannot answer', () => {
        renderCard({canAnswer: false, onAnswer: jest.fn(), onSkip: jest.fn()});

        expect(screen.getByText('Waiting for an answer from the requester')).not.toBeNull();
        expect(screen.queryByText('Accept')).toBeNull();
        expect(screen.queryByText('Skip')).toBeNull();
    });
});
