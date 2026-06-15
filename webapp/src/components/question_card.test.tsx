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
        allowFreeForm: false,
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
        // allow_free_form defaults to true when absent.
        expect(parsed).toEqual({
            question: 'Pick one',
            options: [{label: 'A', description: 'first'}, {label: 'B'}],
            multiSelect: false,
            allowFreeForm: true,
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

    test('explicit allow_free_form false disables free-form', () => {
        const parsed = parseQuestionArgs({
            question: 'Q?',
            options: [{label: 'A'}],
            allow_free_form: false,
        });
        expect(parsed?.allowFreeForm).toBe(false);
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

        expect(onAnswer).toHaveBeenCalledWith(['Design team'], '');
    });

    test('multi-select accumulates and toggles options off', () => {
        const onAnswer = jest.fn();
        renderCard({question: makeQuestion({multiSelect: true}), onAnswer, onSkip: jest.fn()});

        fireEvent.click(screen.getByText('UX Design'));
        fireEvent.click(screen.getByText('Product'));
        fireEvent.click(screen.getByText('UX Design')); // toggle the first back off
        fireEvent.click(screen.getByText('Accept'));

        expect(onAnswer).toHaveBeenCalledWith(['Product'], '');
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

    test('free-form row appears only when allowFreeForm is enabled', () => {
        const {queryByText, rerender} = renderCard({onAnswer: jest.fn(), onSkip: jest.fn()});
        expect(queryByText('Something else…')).toBeNull();

        rerender(
            <IntlProvider locale='en'>
                <QuestionCard
                    tool={makeTool()}
                    question={makeQuestion({allowFreeForm: true})}
                    isProcessing={false}
                    canAnswer={true}
                    onAnswer={jest.fn()}
                    onSkip={jest.fn()}
                />
            </IntlProvider>,
        );
        expect(queryByText('Something else…')).not.toBeNull();
    });

    test('selecting the free-form row reveals the text input', () => {
        const {queryByPlaceholderText, getByText} = renderCard({
            question: makeQuestion({allowFreeForm: true}),
            onAnswer: jest.fn(),
            onSkip: jest.fn(),
        });

        expect(queryByPlaceholderText('Something else…')).toBeNull();
        fireEvent.click(getByText('Something else…'));
        expect(queryByPlaceholderText('Something else…')).not.toBeNull();
    });

    test('typing a free-form answer and accepting calls onAnswer with the custom text', () => {
        const onAnswer = jest.fn();
        const {getByText, getByPlaceholderText} = renderCard({
            question: makeQuestion({allowFreeForm: true}),
            onAnswer,
            onSkip: jest.fn(),
        });

        fireEvent.click(getByText('Something else…'));
        fireEvent.change(getByPlaceholderText('Something else…'), {target: {value: 'Post it in #random'}});
        fireEvent.click(getByText('Accept'));

        expect(onAnswer).toHaveBeenCalledWith([], 'Post it in #random');
    });

    test('single-select free-form replaces a predefined choice', () => {
        const onAnswer = jest.fn();
        const {getByText, getByPlaceholderText} = renderCard({
            question: makeQuestion({allowFreeForm: true}),
            onAnswer,
            onSkip: jest.fn(),
        });

        fireEvent.click(getByText('UX Design'));
        fireEvent.click(getByText('Something else…')); // replaces the predefined choice
        fireEvent.change(getByPlaceholderText('Something else…'), {target: {value: 'My own answer'}});
        fireEvent.click(getByText('Accept'));

        expect(onAnswer).toHaveBeenCalledWith([], 'My own answer');
    });

    test('Accept is disabled when the free-form row is selected but empty', () => {
        const {getByText, getByPlaceholderText} = renderCard({
            question: makeQuestion({allowFreeForm: true}),
            onAnswer: jest.fn(),
            onSkip: jest.fn(),
        });

        fireEvent.click(getByText('Something else…'));
        expect((getByText('Accept').closest('button') as HTMLButtonElement).disabled).toBe(true);

        // Whitespace-only text is treated as empty, so Accept stays disabled.
        fireEvent.change(getByPlaceholderText('Something else…'), {target: {value: '   '}});
        expect((getByText('Accept').closest('button') as HTMLButtonElement).disabled).toBe(true);
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
