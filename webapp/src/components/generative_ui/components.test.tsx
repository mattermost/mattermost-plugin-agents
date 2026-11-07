// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen, within} from '@testing-library/react';

import {noticeType, statusTone, tagColor} from './compass/compass_coverage';
import {mattermostLibrary, promptOptions} from './library';
import {installPostUtils, openUIMessage, renderAgentPost} from './test_helpers';

// One render case per library component, run through PostText like a real
// agent post. Compass-backed components also assert the Compass DOM they rely
// on, so a Compass pin bump that changes it fails here, per component.

type Fixture = {

    /** Positional arguments, as OpenUI Lang expressions. */
    args: string[];

    /** Places the call where its schema allows it; defaults to a Stack child. */
    place?: (call: string) => string;
    check: (block: HTMLElement) => void;

    /** Selector the rendered block must contain for each value of an enum argument. */
    variants?: Record<string, (value: string) => string>;
};

// Compass ships CSS modules, so a block class like `tag` is emitted as
// `_tag_<hash>_<line>`; `_tag__label_…` must not match it.
const compassRoots = (block: HTMLElement, name: string) => {
    const pattern = new RegExp(`^_${name}_[a-z0-9]+_\\d+$`);
    return [...block.querySelectorAll('[class]')].filter((element) => [...element.classList].some((cls) => pattern.test(cls)));
};
const compassModifier = (name: string) => `[class*="_${name}_"]`;

const fixtures: Record<string, Fixture> = {
    Stack: {
        args: ['[Text("Hello")]', '"m"'],
        place: (call) => call,
        check: (block) => expect(within(block).getByText('Hello')).toBeTruthy(),
    },
    Card: {
        args: ['"Plan"', '[Text("Body")]', '"Due Friday"'],
        check: (block) => {
            expect(within(block).getByRole('heading', {name: 'Plan'})).toBeTruthy();
            expect(within(block).getByText('Body')).toBeTruthy();
            expect(within(block).getByText('Due Friday')).toBeTruthy();
        },
    },
    Columns: {
        args: ['[Card("Postgres", [Text("4 ms")]), Card("Redis", [Text("38 ms")])]'],
        check: (block) => {
            expect(within(block).getByRole('heading', {name: 'Postgres'})).toBeTruthy();
            expect(within(block).getByRole('heading', {name: 'Redis'})).toBeTruthy();
        },
    },
    Heading: {
        args: ['"Overview"', '"large"'],
        check: (block) => expect(within(block).getByRole('heading', {name: 'Overview'})).toBeTruthy(),
    },
    Text: {
        args: ['"Release is on track"', '"muted"'],
        check: (block) => expect(within(block).getByText('Release is on track')).toBeTruthy(),
    },
    Notice: {
        args: ['"Blocker"', '"Two tests fail"', '"warning"'],
        check: (block) => {
            expect(within(block).getByText('Blocker')).toBeTruthy();
            expect(within(block).getByText('Two tests fail')).toBeTruthy();
            expect(compassRoots(block, 'section-notice')).toHaveLength(1);
        },
        variants: {
            type: (value) => {
                const compass = noticeType.toCompass(value, '');
                return compass === 'info' ? compassModifier('section-notice') : `[class*="section-notice--type-${compass}"]`;
            },
        },
    },
    Status: {
        args: ['"Deployed to staging"', '"success"'],
        check: (block) => {
            expect(within(block).getByText('Deployed to staging')).toBeTruthy();
            expect(compassRoots(block, 'field-message')).toHaveLength(1);
        },
        variants: {tone: (value) => `[class*="field-message--tone-${statusTone.toCompass(value, '')}"]`},
    },
    Keys: {
        args: ['["Ctrl", "Shift", "K"]'],
        check: (block) => {
            expect(compassRoots(block, 'shortcut-tag__key')).toHaveLength(3);
            expect(within(block).getByText('Shift')).toBeTruthy();
        },
    },
    Empty: {
        args: ['"No incidents"', '"Nothing was opened this week."'],
        check: (block) => {
            expect(within(block).getByText('No incidents')).toBeTruthy();
            expect(within(block).getByText('Nothing was opened this week.')).toBeTruthy();
            expect(compassRoots(block, 'empty-state')).toHaveLength(1);
        },
    },
    Fields: {
        args: ['[Field("Owner", "Alice"), Field("Status", Tag("Healthy", "success"))]'],
        check: (block) => {
            expect(within(block).getByText('Owner').tagName).toBe('DT');
            expect(within(block).getByText('Alice').tagName).toBe('DD');
            expect(within(block).getByText('Healthy')).toBeTruthy();
        },
    },
    Field: {
        args: ['"Due"', '"Friday"'],
        place: (call) => `Fields([${call}])`,
        check: (block) => expect(within(block).getByText('Friday').tagName).toBe('DD'),
    },
    Stats: {
        args: ['[Stat("Open PRs", 6, "-3", "good"), Stat("Fixed", 41)]'],
        check: (block) => {
            expect(within(block).getByText('Open PRs')).toBeTruthy();
            expect(within(block).getByText('41')).toBeTruthy();
        },
    },
    Stat: {
        args: ['"Error rate"', '"18%"', '"+17 pts"', '"bad"'],
        place: (call) => `Stats([${call}])`,
        check: (block) => {
            expect(within(block).getByText('18%')).toBeTruthy();
            expect(within(block).getByText('+17 pts')).toBeTruthy();
        },
    },
    Table: {
        args: ['["Service", "Status"]', '[["api", Tag("Healthy", "success")], ["search", 3]]'],
        check: (block) => {
            const table = within(block).getByRole('table');
            expect(within(table).getByRole('columnheader', {name: 'Status'})).toBeTruthy();
            expect(within(table).getByText('Healthy')).toBeTruthy();
            expect(within(table).getByText('3')).toBeTruthy();
        },
    },
    BarChart: {
        args: ['["payments", "auth"]', '[130, 5]', '"Minutes of impact"', '" min"'],
        check: (block) => {
            expect(within(block).getByRole('img', {name: 'payments: 130 min'})).toBeTruthy();
            expect(within(block).getByText('Minutes of impact')).toBeTruthy();
            const bars = compassRoots(block, 'progress-bar');
            expect(bars).toHaveLength(2);
            bars.forEach((bar) => expect(bar.getAttribute('aria-hidden')).toBe('true'));
            expect(within(block).queryByRole('progressbar')).toBeNull();
        },
    },
    Progress: {
        args: ['"Milestones"', '60', '"3 of 5 done"'],
        check: (block) => {
            const bar = within(block).getByRole('progressbar', {name: 'Milestones'});
            expect(bar.getAttribute('aria-valuenow')).toBe('60');
            expect(bar.getAttribute('aria-valuetext')).toBe('3 of 5 done');
            expect(bar.className).toContain('progress-bar--size-large');
        },
    },
    Steps: {
        args: ['[Step("Code freeze", "done"), Step("QA", "current", "2 blockers left")]'],
        check: (block) => {
            expect(within(block).getAllByRole('listitem')).toHaveLength(2);
            expect(within(block).getByText('2 blockers left')).toBeTruthy();
        },
    },
    Step: {
        args: ['"Ship"', '"todo"', '"Friday"'],
        place: (call) => `Steps([${call}])`,
        check: (block) => expect(within(block).getByRole('listitem').textContent).toContain('Ship'),
    },
    Tags: {
        args: ['[Tag("api"), Tag("db", "danger")]'],
        check: (block) => expect(compassRoots(block, 'tag')).toHaveLength(2),
    },
    Tag: {
        args: ['"Healthy"', '"success"'],
        place: (call) => `Tags([${call}])`,
        check: (block) => {
            expect(within(block).getByText('Healthy')).toBeTruthy();
            expect(block.querySelector('[class*="tag--size-small"]')).toBeTruthy();
        },
        variants: {color: (value) => `[class*="tag--type-${tagColor.toCompass(value, '')}"]`},
    },
    Link: {
        args: ['"Runbook"', '"https://example.com/runbook"'],
        check: (block) => expect(within(block).getByText(/Runbook/)).toBeTruthy(),
    },
    Divider: {
        args: [],
        check: (block) => expect(compassRoots(block, 'divider').map((divider) => divider.querySelector('hr'))).toEqual([expect.anything()]),
    },
};

const program = (name: string, args: string[]) => {
    const call = `${name}(${args.join(', ')})`;
    const placed = fixtures[name].place?.(call) ?? `Stack([${call}])`;
    return `root = ${placed}`;
};

const renderBlock = (source: string) => {
    renderAgentPost(openUIMessage(source));
    const block = screen.getByTestId('generative-ui-block');

    // The error boundary's fallback shows the raw program instead of components.
    expect(within(block).queryByText(source, {exact: false})).toBeNull();
    return block;
};

type ArgSchema = {enum?: string[]};
const defs = mattermostLibrary.toJSONSchema().$defs as Record<string, {properties?: Record<string, ArgSchema>}>;

const enumCases = Object.keys(fixtures).flatMap((name) => Object.entries(defs[name]?.properties ?? {}).flatMap(([arg, schema], index) => (schema.enum ?? []).map((value) => ({name, arg, index, value}))));

beforeAll(installPostUtils);

// React warnings (keys, invalid props) fail the test. jsdom's own
// "Not implemented" reports from accessible-name lookups are not ours.
let consoleError: jest.SpyInstance;
beforeEach(() => {
    consoleError = jest.spyOn(console, 'error');
});
afterEach(() => {
    const ours = consoleError.mock.calls.filter(([first]) => !String(first?.message ?? first).startsWith('Not implemented'));
    consoleError.mockRestore();
    expect(ours).toEqual([]);
});

describe('generative UI components', () => {
    it('has a render case for every library component', () => {
        expect(Object.keys(fixtures).sort()).toEqual(Object.keys(mattermostLibrary.components).sort());
    });

    it.each(Object.keys(fixtures))('renders %s', (name) => {
        fixtures[name].check(renderBlock(program(name, fixtures[name].args)));
    });

    it.each(enumCases)('renders $name with $arg "$value"', ({name, arg, index, value}) => {
        const args = [...fixtures[name].args];
        while (args.length < index) {
            args.push('null');
        }
        args[index] = JSON.stringify(value);

        const block = renderBlock(program(name, args));
        fixtures[name].check(block);
        const variant = fixtures[name].variants?.[arg];
        if (variant) {
            expect(block.querySelector(variant(value))).toBeTruthy();
        }
    });

    it.each(promptOptions.examples ?? [])('renders every streamed prefix of a prompt example without falling back', (example) => {
        const lines = example.split('\n');
        const prefixes = lines.flatMap((line, i) => {
            const before = lines.slice(0, i).join('\n');
            const joiner = before ? '\n' : '';
            return [before + joiner + line.slice(0, Math.ceil(line.length / 2)), before + joiner + line];
        });
        for (const prefix of prefixes) {
            const {unmount} = renderAgentPost(openUIMessage(prefix, {closed: false}), {streaming: true});
            const block = screen.getByTestId('generative-ui-block');
            expect(block.querySelector('pre')).toBeNull();
            unmount();
        }
    });
});

describe('Stat change', () => {
    it.each([
        {change: '+2', trend: 'bad', direction: 'up', sentiment: 'bad'},
        {change: '-3 since Monday', trend: 'good', direction: 'down', sentiment: 'good'},
        {change: '+17 pts', trend: 'down', direction: 'up', sentiment: 'bad'},
        {change: '−4%', trend: 'up', direction: 'down', sentiment: 'good'},
        {change: 'flat', trend: 'neutral', direction: 'none', sentiment: 'neutral'},
        {change: '+1', trend: 'sideways', direction: 'up', sentiment: 'neutral'},
    ])('$change with trend $trend points $direction and reads as $sentiment', ({change, trend, direction, sentiment}) => {
        const block = renderBlock(`root = Stack([Stats([Stat("Metric", 1, "${change}", "${trend}")])])`);
        const element = within(block).getByText(change);
        expect(element.getAttribute('data-direction')).toBe(direction);
        expect(element.getAttribute('data-sentiment')).toBe(sentiment);
    });
});

describe('Table', () => {
    it('renders every cell in both the wide table and the narrow stacked layout', () => {
        const block = renderBlock('root = Stack([Table(["Service", "Owner", "Status"], [["payments", "@alice", Tag("Resolved", "success")], ["auth", "@dan", Tag("Down", "danger")]])])');
        const wide = within(block).getByTestId('generative-ui-table-wide');
        const narrow = within(block).getByTestId('generative-ui-table-narrow');
        for (const cell of ['payments', '@alice', 'Resolved', 'auth', '@dan', 'Down']) {
            expect(within(wide).getByText(cell)).toBeTruthy();
            expect(within(narrow).getByText(cell).closest('dd')).toBeTruthy();
        }
        expect(within(narrow).getAllByText('Owner').map((label) => label.tagName)).toEqual(['DT', 'DT']);
    });
});