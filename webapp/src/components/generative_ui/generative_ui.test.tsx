// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, within} from '@testing-library/react';
import {Provider} from 'react-redux';
import {IntlProvider} from 'react-intl';

import manifest from '@/manifest';

import PostText from '../post_text';

const program = [
    'root = Stack([intro, stats, table, plan, link])',
    'intro = Text("Release is **on track**")',
    'stats = Stats([Stat("Open PRs", 6, "-3", "down"), Stat("Fixed", 41)])',
    'table = Table(["Service", "Status"], [["api", Tag("Healthy", "success")], ["search", Tag("Degraded", "warning")]])',
    'plan = Steps([Step("Code freeze", "done"), Step("Ship", "todo")])',
    'link = Link("Runbook", "https://example.com/runbook")',
].join('\n');

const message = `Two services need attention.\n\n\`\`\`openui-lang\n${program}\n\`\`\`\n\nLet me know if you want details.`;

const makeStore = (pluginState: Record<string, unknown>) => {
    const state = {
        entities: {
            general: {config: {SiteURL: 'http://localhost:8065'}},
            teams: {teams: {}},
            channels: {channels: {chan1: {id: 'chan1', team_id: 'team1', type: 'D'}}},
        },
        [`plugins-${manifest.id}`]: pluginState,
    };
    return {getState: () => state, subscribe: () => jest.fn(), dispatch: jest.fn()} as any;
};

const renderPost = (pluginState: Record<string, unknown>, allowGenerativeUI = true) => render(
    <Provider store={makeStore(pluginState)}>
        <IntlProvider locale='en'>
            <PostText
                message={message}
                channelID='chan1'
                postID='post1'
                allowGenerativeUI={allowGenerativeUI}
            />
        </IntlProvider>
    </Provider>,
);

beforeAll(() => {
    (window as any).PostUtils = {
        formatText: (text: string) => text,
        messageHtmlToComponent: (html: string) => <span>{html}</span>,
    };
});

describe('generative UI in agent posts', () => {
    it('renders an OpenUI Lang block as Mattermost components between the prose', () => {
        renderPost({generativeUIEnabled: true});

        const block = screen.getByTestId('generative-ui-block');
        expect(screen.getByText('Two services need attention.')).toBeTruthy();
        expect(screen.getByText('Let me know if you want details.')).toBeTruthy();

        const table = within(block).getByRole('table');
        expect(within(table).getByRole('columnheader', {name: 'Service'})).toBeTruthy();
        expect(within(table).getByText('Degraded')).toBeTruthy();
        expect(within(block).getByText('Open PRs')).toBeTruthy();
        expect(within(block).getByText('41')).toBeTruthy();
        expect(within(block).getByText('Code freeze')).toBeTruthy();
        expect(within(block).queryByText('root = Stack', {exact: false})).toBeNull();
    });

    it('shows links as plain text unless unsafe links are allowed', () => {
        const {unmount} = renderPost({generativeUIEnabled: true, allowUnsafeLinks: false});
        expect(screen.queryByRole('link', {name: /Runbook/})).toBeNull();
        expect(screen.getByText('Runbook (https://example.com/runbook)')).toBeTruthy();
        unmount();

        renderPost({generativeUIEnabled: true, allowUnsafeLinks: true});
        expect(screen.getByRole('link', {name: /Runbook/}).getAttribute('href')).toBe('https://example.com/runbook');
    });

    it.each([
        {name: 'feature disabled', pluginState: {generativeUIEnabled: false}, allowGenerativeUI: true},
        {name: 'post does not opt in', pluginState: {generativeUIEnabled: true}, allowGenerativeUI: false},
    ])('leaves the block as markdown when $name', ({pluginState, allowGenerativeUI}) => {
        renderPost(pluginState, allowGenerativeUI);
        expect(screen.queryByTestId('generative-ui-block')).toBeNull();
        expect(screen.getByText(/```openui-lang/)).toBeTruthy();
    });
});
