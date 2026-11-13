// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import {Provider} from 'react-redux';
import {IntlProvider} from 'react-intl';

import manifest from '@/manifest';

import PostText from '../post_text';

import {GenerativeUIActions, GenerativeUIActionsContext, PostInteraction, Submission} from './actions';

const program = [
    'root = Stack([intro, form, next])',
    'intro = Text("Pick where to deploy.")',
    'form = Form("deploy", [env, reason, notify], [Button("Deploy"), Button("Cancel", "secondary")])',
    'env = RadioGroup("env", "Environment", ["staging", "production"], "staging")',
    'reason = TextInput("reason", "Reason", "Why now?", "", true)',
    'notify = Checkbox("notify", "Notify the team")',
    'next = Buttons([Button("Show details")])',
].join('\n');

const message = `Here is the deploy form.\n\n\`\`\`openui-lang\n${program}\n\`\`\``;

const state = {
    entities: {
        general: {config: {SiteURL: 'http://localhost:8065'}},
        teams: {teams: {}},
        channels: {channels: {chan1: {id: 'chan1', team_id: 'team1', type: 'D'}}},
    },
    [`plugins-${manifest.id}`]: {},
};

const store = {
    getState: () => state,
    subscribe: () => jest.fn(),
    dispatch: jest.fn(),
} as any;

const renderForm = (interaction: PostInteraction, submit: GenerativeUIActions['submit'] = jest.fn().mockResolvedValue(null), options: {showCursor?: boolean; offset?: number; message?: string} = {}) => {
    const text = options.message ?? message;
    const view = render(
        <Provider store={store}>
            <IntlProvider locale='en'>
                <GenerativeUIActionsContext.Provider value={{interaction, submit}}>
                    <PostText
                        message={options.showCursor ? text.slice(0, -4) : text}
                        channelID='chan1'
                        postID='post1'
                        allowGenerativeUI={true}
                        showCursor={options.showCursor}
                        uiBlockOffset={options.offset}
                    />
                </GenerativeUIActionsContext.Provider>
            </IntlProvider>
        </Provider>,
    );
    return {...view, submit};
};

beforeAll(() => {
    (window as any).PostUtils = {
        formatText: (text: string) => text,
        messageHtmlToComponent: (html: string) => <span>{html}</span>,
    };
});

describe('generative UI forms', () => {
    it('submits the clicked button with the form values, in field order', async () => {
        const {submit} = renderForm({kind: 'editable'}, jest.fn().mockResolvedValue(null), {offset: 2});
        const form = screen.getByTestId('generative-ui-form');

        fireEvent.click(within(form).getByLabelText('production'));
        fireEvent.change(within(form).getByLabelText('Reason'), {target: {value: 'Hotfix for login'}});
        fireEvent.click(within(form).getByLabelText('Notify the team'));
        await act(async () => {
            fireEvent.click(within(form).getByRole('button', {name: 'Deploy'}));
        });

        expect(submit).toHaveBeenCalledTimes(1);
        expect(submit).toHaveBeenCalledWith({
            block: 2,
            form: 'deploy',
            label: 'Deploy',
            fields: [
                {name: 'env', label: 'Environment', value: 'production'},
                {name: 'reason', label: 'Reason', value: 'Hotfix for login'},
                {name: 'notify', label: 'Notify the team', value: true},
            ],
        } satisfies Submission);
    });

    it('submits the agent defaults the user left alone', async () => {
        const {submit} = renderForm({kind: 'editable'});
        const form = screen.getByTestId('generative-ui-form');
        fireEvent.change(within(form).getByLabelText('Reason'), {target: {value: 'Routine'}});
        await act(async () => {
            fireEvent.click(within(form).getByRole('button', {name: 'Deploy'}));
        });
        expect((submit as jest.Mock).mock.calls[0][0].fields).toEqual([
            {name: 'env', label: 'Environment', value: 'staging'},
            {name: 'reason', label: 'Reason', value: 'Routine'},
            {name: 'notify', label: 'Notify the team', value: false},
        ]);
    });

    it('flags an empty required field instead of submitting', () => {
        const {submit} = renderForm({kind: 'editable'});
        const form = screen.getByTestId('generative-ui-form');
        fireEvent.click(within(form).getByRole('button', {name: 'Deploy'}));
        expect(submit).not.toHaveBeenCalled();
        expect(within(form).getByText('Required')).toBeTruthy();
    });

    it('sends a standalone button with no form and no values', async () => {
        const {submit} = renderForm({kind: 'editable'});
        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: 'Show details'}));
        });
        expect(submit).toHaveBeenCalledWith({block: 0, form: '', label: 'Show details', fields: []});
    });

    it('disables every control while a click is in flight, and shows a failure', async () => {
        let reject: (err: Error) => void = () => {}; // eslint-disable-line no-empty-function
        const submit = jest.fn(() => new Promise<void>((_, rej) => {
            reject = rej;
        }));
        renderForm({kind: 'editable'}, submit);
        fireEvent.click(screen.getByRole('button', {name: 'Show details'}));
        fireEvent.click(screen.getByRole('button', {name: 'Show details'}));
        expect(submit).toHaveBeenCalledTimes(1);
        expect((screen.getByRole('button', {name: 'Cancel'}) as HTMLButtonElement).disabled).toBe(true);

        await act(async () => {
            reject(new Error('this generated form is no longer active'));
        });
        await waitFor(() => expect(screen.getByText('Could not send your response')).toBeTruthy());
        expect(screen.getByText('this generated form is no longer active')).toBeTruthy();
    });

    it.each([
        {name: 'onlooker', interaction: {kind: 'onlooker', requesterUsername: 'alice'} as PostInteraction, notice: 'Only @alice can respond'},
        {name: 'stale reply', interaction: {kind: 'stale'} as PostInteraction, notice: 'This form is no longer active'},
        {name: 'read-only', interaction: {kind: 'readonly'} as PostInteraction, notice: null},
    ])('keeps every control disabled for a $name', ({interaction, notice}) => {
        const {submit} = renderForm(interaction);
        for (const button of screen.getAllByRole('button').filter((b) => b.textContent !== 'View source')) {
            expect((button as HTMLButtonElement).disabled).toBe(true);
        }
        expect((screen.getByLabelText('Reason') as HTMLInputElement).disabled).toBe(true);
        fireEvent.click(screen.getByRole('button', {name: 'Deploy'}));
        expect(submit).not.toHaveBeenCalled();
        if (notice) {
            expect(screen.getByText(notice)).toBeTruthy();
        }
    });

    it('shows a submitted form read-only with the submitted values and the chosen button', () => {
        renderForm({
            kind: 'submitted',
            submission: {
                source_post_id: 'post1',
                block: 0,
                form: 'deploy',
                label: 'Deploy',
                fields: [
                    {name: 'env', label: 'Environment', value: 'production'},
                    {name: 'reason', label: 'Reason', value: 'Hotfix'},
                    {name: 'notify', label: 'Notify the team', value: true},
                ],
            },
        });
        const form = screen.getByTestId('generative-ui-form');
        expect((within(form).getByLabelText('production') as HTMLInputElement).checked).toBe(true);
        expect((within(form).getByLabelText('Reason') as HTMLInputElement).value).toBe('Hotfix');
        expect((within(form).getByLabelText('Notify the team') as HTMLInputElement).checked).toBe(true);
        expect(within(form).getByRole('button', {name: 'Deploy'}).getAttribute('aria-pressed')).toBe('true');
        expect(within(form).getByRole('button', {name: 'Cancel'}).getAttribute('aria-pressed')).toBe('false');
        expect(within(form).getByText('Submitted')).toBeTruthy();
    });

    it('keeps controls disabled while the block streams', () => {
        const {submit} = renderForm({kind: 'editable'}, jest.fn(), {showCursor: true});
        const deploy = screen.queryByRole('button', {name: 'Deploy'});
        if (deploy) {
            fireEvent.click(deploy);
            expect((deploy as HTMLButtonElement).disabled).toBe(true);
        }
        expect(submit).not.toHaveBeenCalled();
    });

    it.each([
        {name: 'the label the server stored', submittedLabel: 'Go'},
        {name: 'the label just clicked, not yet stored', submittedLabel: ' Go '},
    ])('marks a padded button chosen from $name', ({submittedLabel}) => {
        const padded = '```openui-lang\nroot = Stack([form])\nform = Form("pick", [], [Button(" Go "), Button("Stop")])\n```';
        renderForm(
            {kind: 'submitted', submission: {source_post_id: 'post1', block: 0, form: 'pick', label: submittedLabel, fields: []}},
            jest.fn(),
            {message: padded},
        );
        const form = screen.getByTestId('generative-ui-form');
        expect(within(form).getByRole('button', {name: 'Go'}).getAttribute('aria-pressed')).toBe('true');
        expect(within(form).getByRole('button', {name: 'Stop'}).getAttribute('aria-pressed')).toBe('false');
    });
});
