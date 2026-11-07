// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen, within} from '@testing-library/react';

import {installPostUtils, renderAgentPost} from './test_helpers';

const program = [
    'root = Stack([intro, stats, table, plan, link])',
    'intro = Text("Release is **on track**")',
    'stats = Stats([Stat("Open PRs", 6, "-3", "down"), Stat("Fixed", 41)])',
    'table = Table(["Service", "Status"], [["api", Tag("Healthy", "success")], ["search", Tag("Degraded", "warning")]])',
    'plan = Steps([Step("Code freeze", "done"), Step("Ship", "todo")])',
    'link = Link("Runbook", "https://example.com/runbook")',
].join('\n');

const message = `Two services need attention.\n\n\`\`\`openui-lang\n${program}\n\`\`\`\n\nLet me know if you want details.`;

const renderPost = (pluginState: Record<string, unknown>, allowGenerativeUI = true) => renderAgentPost(message, {pluginState, allowGenerativeUI});

beforeAll(installPostUtils);

describe('generative UI in agent posts', () => {
    it('renders an OpenUI Lang block as Mattermost components between the prose', () => {
        renderPost({});

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

    it.each([
        {name: 'plain text', linkPolicy: {kind: 'plain_text'}, clickable: false},
        {name: 'any domain', linkPolicy: {kind: 'any_domain'}, clickable: true},
        {name: 'allowlist containing the host', linkPolicy: {kind: 'allowlist', allowedDomains: [{host: 'example.com', wildcard: false}]}, clickable: true},
        {name: 'allowlist without the host', linkPolicy: {kind: 'allowlist', allowedDomains: [{host: 'other.com', wildcard: false}]}, clickable: false},
    ])('follows the $name link policy', ({linkPolicy, clickable}) => {
        renderPost({linkPolicy});
        if (clickable) {
            expect(screen.getByRole('link', {name: /Runbook/}).getAttribute('href')).toBe('https://example.com/runbook');
        } else {
            expect(screen.queryByRole('link', {name: /Runbook/})).toBeNull();
            expect(screen.getByText('Runbook (https://example.com/runbook)')).toBeTruthy();
        }
    });

    it('leaves the block as markdown when the post does not opt in', () => {
        renderPost({}, false);
        expect(screen.queryByTestId('generative-ui-block')).toBeNull();
        expect(screen.getByText(/```openui-lang/)).toBeTruthy();
    });
});
