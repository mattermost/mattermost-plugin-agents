// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {z} from 'zod/v4';
import {createLibrary, defineComponent, type PromptOptions} from '@openuidev/react-lang';

import {Divider as CompassDivider} from '@mattermost/compass-ui/components/divider';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {FieldMessage} from '@mattermost/compass-ui/components/field-message';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {ShortcutTagGroup} from '@mattermost/compass-ui/components/shortcut-tag';
import {Tag as CompassTag} from '@mattermost/compass-ui/components/tag';

import {noticeType, statusTone, tagColor} from './compass/compass_coverage';
import {compassLeaf} from './compass/compass_leaf';
import * as D from './data_primitives';
import * as P from './primitives';

type Node = {props?: Record<string, unknown>} | null | undefined;

// Child component props arrive as parsed element nodes; streaming can leave holes.
const nodeProps = (nodes: unknown): Array<Record<string, unknown>> =>
    (Array.isArray(nodes) ? nodes : []).
        map((node: Node) => node?.props).
        filter((props): props is Record<string, unknown> => Boolean(props));

const asArray = <T, >(value: T[] | undefined | null): T[] => (Array.isArray(value) ? value : []);

export const Tag = compassLeaf({
    name: 'Tag',
    description: 'Compact status pill, e.g. a state, owner, or priority.',
    compass: 'Tag',
    component: CompassTag,
    args: {
        label: {prop: 'label', schema: z.string(), fallback: ''},
        color: {prop: 'type', schema: tagColor.schema.optional(), values: tagColor, fallback: 'default'},
    },
    fixed: {size: 'small'},
});

const Tags = defineComponent({
    name: 'Tags',
    description: 'A wrapping row of Tag pills.',
    props: z.object({
        items: z.array(Tag.ref),
    }),
    component: ({props, renderNode}) => <P.InlineGroup>{renderNode(props.items)}</P.InlineGroup>,
});

const Heading = defineComponent({
    name: 'Heading',
    description: 'Section heading. Use sparingly; Card already has a title.',
    props: z.object({
        text: z.string(),
        size: z.enum(['large', 'medium', 'small']).optional(),
    }),
    component: ({props}) => (
        <P.Heading
            text={props.text ?? ''}
            size={props.size}
        />
    ),
});

const Text = defineComponent({
    name: 'Text',
    description: 'A paragraph. Supports inline Mattermost markdown (bold, italics, `code`, links). tone "muted" for secondary text, "small" for footnotes.',
    props: z.object({
        text: z.string(),
        tone: z.enum(['default', 'muted', 'small']).optional(),
    }),
    component: ({props}) => (
        <P.Text
            text={props.text ?? ''}
            tone={props.tone}
        />
    ),
});

const Notice = compassLeaf({
    name: 'Notice',
    description: 'Highlighted callout for a key takeaway, warning, or blocker. "hint" is for a tip.',
    compass: 'SectionNotice',
    component: SectionNotice,
    args: {
        title: {prop: 'title', schema: z.string(), fallback: ''},
        text: {prop: 'description', schema: z.string().optional()},
        type: {prop: 'type', schema: noticeType.schema.optional(), values: noticeType, fallback: 'info'},
    },
});

const Status = compassLeaf({
    name: 'Status',
    description: 'One-line status with a tone icon; lighter than Notice. "info" has no icon.',
    compass: 'FieldMessage',
    component: FieldMessage,
    args: {
        text: {prop: 'message', schema: z.string(), fallback: ''},
        tone: {prop: 'tone', schema: statusTone.schema.optional(), values: statusTone, fallback: 'info'},
    },
});

const Keys = compassLeaf({
    name: 'Keys',
    description: 'A keyboard shortcut, one key per item, e.g. Keys(["Ctrl", "Shift", "K"]).',
    compass: 'ShortcutTagGroup',
    component: ShortcutTagGroup,
    args: {
        keys: {prop: 'labels', schema: z.array(z.string()), fallback: []},
    },
});

const Empty = compassLeaf({
    name: 'Empty',
    description: 'Placeholder when a search or list has no results.',
    compass: 'EmptyState',
    component: EmptyState,
    args: {
        title: {prop: 'title', schema: z.string(), fallback: ''},
        text: {prop: 'description', schema: z.string().optional()},
    },
});

const Field = defineComponent({
    name: 'Field',
    description: 'One label/value pair inside Fields.',
    props: z.object({
        label: z.string(),
        value: z.union([z.string(), z.number(), Tag.ref]),
    }),
    component: () => null,
});

const Fields = defineComponent({
    name: 'Fields',
    description: 'Two-column label/value list for record details (owner, due date, status...).',
    props: z.object({
        items: z.array(Field.ref),
    }),
    component: ({props, renderNode}) => (
        <P.Fields
            items={nodeProps(props.items).map((item) => ({
                label: String(item.label ?? ''),
                value: renderNode(item.value),
            }))}
        />
    ),
});

const TRENDS: readonly D.Trend[] = ['good', 'bad', 'neutral', 'up', 'down'];
const asTrend = (value: unknown): D.Trend | undefined => TRENDS.find((trend) => trend === value);

const Stat = defineComponent({
    name: 'Stat',
    description: 'A headline metric. change is a short delta like "+12%"; its sign sets the arrow. trend says if the change is "good" (green) or "bad" (red); "up"/"down" are legacy aliases.',
    props: z.object({
        label: z.string(),
        value: z.union([z.string(), z.number()]),
        change: z.string().optional(),
        trend: z.enum(TRENDS).optional(),
    }),
    component: () => null,
});

const Stats = defineComponent({
    name: 'Stats',
    description: 'A responsive grid of Stat tiles (2-4 is ideal).',
    props: z.object({
        items: z.array(Stat.ref),
    }),
    component: ({props}) => (
        <D.StatGrid count={nodeProps(props.items).length}>
            {nodeProps(props.items).map((item, index) => (
                <D.Stat
                    key={`${String(item.label)}-${index}`}
                    label={String(item.label ?? '')}
                    value={(item.value as string | number | undefined) ?? ''}
                    change={item.change as string | undefined}
                    trend={asTrend(item.trend)}
                />
            ))}
        </D.StatGrid>
    ),
});

const Table = defineComponent({
    name: 'Table',
    description: 'Row-oriented table. Each row is an array of cells in column order; a cell is a string, number, or Tag.',
    props: z.object({
        columns: z.array(z.string()),
        rows: z.array(z.array(z.union([z.string(), z.number(), Tag.ref]))),
    }),
    component: ({props, renderNode}) => (
        <D.Table
            columns={asArray(props.columns).map(String)}
            rows={asArray(props.rows).map((row) => asArray(row).map((cell) => renderNode(cell)))}
        />
    ),
});

const BarChart = defineComponent({
    name: 'BarChart',
    description: 'Horizontal bar chart comparing values across labels (same length arrays). unit is appended to values, e.g. "%" or " hrs".',
    props: z.object({
        labels: z.array(z.string()),
        values: z.array(z.number()),
        title: z.string().optional(),
        unit: z.string().optional(),
    }),
    component: ({props}) => (
        <D.BarChart
            labels={asArray(props.labels).map(String)}
            values={asArray(props.values).map(Number)}
            title={props.title}
            unit={props.unit}
        />
    ),
});

const Progress = defineComponent({
    name: 'Progress',
    description: 'Progress toward a goal. value is 0-100; caption replaces the default "NN%" text, e.g. "7 of 10 tasks".',
    props: z.object({
        label: z.string(),
        value: z.number(),
        caption: z.string().optional(),
    }),
    component: ({props}) => (
        <D.Progress
            label={props.label ?? ''}
            value={Number(props.value)}
            caption={props.caption}
        />
    ),
});

const Step = defineComponent({
    name: 'Step',
    description: 'One item in Steps.',
    props: z.object({
        title: z.string(),
        status: z.enum(['done', 'current', 'todo', 'blocked']).optional(),
        description: z.string().optional(),
    }),
    component: () => null,
});

const Steps = defineComponent({
    name: 'Steps',
    description: 'Ordered checklist, plan, or timeline with a status per step.',
    props: z.object({
        items: z.array(Step.ref),
    }),
    component: ({props}) => (
        <P.Steps
            items={nodeProps(props.items).map((item) => ({
                title: String(item.title ?? ''),
                status: (item.status as P.StepStatus | undefined) ?? 'todo',
                description: item.description as string | undefined,
            }))}
        />
    ),
});

const Link = defineComponent({
    name: 'Link',
    description: 'An external link (http, https, or mailto).',
    props: z.object({
        label: z.string(),
        url: z.string(),
    }),
    component: ({props}) => (
        <P.Link
            label={props.label ?? ''}
            url={props.url ?? ''}
        />
    ),
});

const Divider = compassLeaf({
    name: 'Divider',
    description: 'Thin horizontal rule between groups of content.',
    compass: 'Divider',
    component: CompassDivider,
    args: {},
});

const ContentChild = z.union([
    Heading.ref,
    Text.ref,
    Notice.ref,
    Status.ref,
    Keys.ref,
    Empty.ref,
    Fields.ref,
    Stats.ref,
    Table.ref,
    BarChart.ref,
    Progress.ref,
    Steps.ref,
    Tags.ref,
    Link.ref,
    Divider.ref,
]);

const Card = defineComponent({
    name: 'Card',
    description: 'Bordered panel grouping related content under a title.',
    props: z.object({
        title: z.string(),
        children: z.array(ContentChild),
        subtitle: z.string().optional(),
    }),
    component: ({props, renderNode}) => (
        <P.Card
            title={props.title}
            subtitle={props.subtitle}
        >
            {renderNode(props.children)}
        </P.Card>
    ),
});

const Columns = defineComponent({
    name: 'Columns',
    description: 'Places Cards side by side; wraps to one column on narrow screens. Use for comparisons.',
    props: z.object({
        children: z.array(Card.ref),
    }),
    component: ({props, renderNode}) => <P.Row>{renderNode(props.children)}</P.Row>,
});

const Stack = defineComponent({
    name: 'Stack',
    description: 'Root vertical layout.',
    props: z.object({
        children: z.array(z.union([...ContentChild.options, Card.ref, Columns.ref])),
        gap: z.enum(['s', 'm', 'l']).optional(),
    }),
    component: ({props, renderNode}) => <P.Column $gap={props.gap}>{renderNode(props.children)}</P.Column>,
});

export const mattermostLibrary = createLibrary({
    root: 'Stack',
    components: [
        Stack,
        Card,
        Columns,
        Heading,
        Text,
        Notice,
        Status,
        Keys,
        Empty,
        Fields,
        Field,
        Stats,
        Stat,
        Table,
        BarChart,
        Progress,
        Steps,
        Step,
        Tags,
        Tag,
        Link,
        Divider,
    ],
    componentGroups: [
        {name: 'Layout', components: ['Stack', 'Card', 'Columns', 'Divider']},
        {
            name: 'Text',
            components: ['Heading', 'Text', 'Notice', 'Status', 'Keys', 'Link'],
            notes: ['Use at most one Notice per reply, for the key takeaway; use Status for a one-line result.'],
        },
        {name: 'Data', components: ['Fields', 'Field', 'Stats', 'Stat', 'Table', 'BarChart', 'Progress', 'Empty']},
        {
            name: 'Status',
            components: ['Steps', 'Step', 'Tags', 'Tag'],

            // From Compass's compass-build skill (references/components.md, "Label or status tag").
            notes: ['Pick the Tag color by meaning: info for context, warning for caution, success or danger for results, neutral only for plain metadata.'],
        },
    ],
});

// Display-only: no Query/Mutation, $state bindings, or actions until they can
// be routed through the server's tool policy.
export const promptOptions: PromptOptions = {
    preamble: 'OpenUI Lang reference for the components available in Mattermost.',
    toolCalls: false,
    bindings: false,
    builtinFunctions: false,
    examples: [
        [
            'root = Stack([summary, metrics, details])',
            'summary = Text("Release **v2.4** is on track for Friday.")',
            'metrics = Stats([Stat("Open PRs", 6, "-3 since Monday", "good"), Stat("Bugs fixed", 41, "+12", "good")])',
            'details = Card("Remaining work", [plan])',
            'plan = Steps([Step("Code freeze", "done"), Step("QA sign-off", "current", "2 blockers left"), Step("Ship", "todo")])',
        ].join('\n'),
        [
            'root = Stack([cmp])',
            'cmp = Columns([a, b])',
            'a = Card("Postgres", [Fields([Field("Latency", "4 ms"), Field("Status", Tag("Healthy", "success"))])])',
            'b = Card("Redis", [Fields([Field("Latency", "38 ms"), Field("Status", Tag("Degraded", "warning"))])])',
        ].join('\n'),
    ],
    additionalRules: [
        'Only use the components listed above, with arguments in the documented order.',
        'Keep text inside components short; prose belongs outside the code fence as normal markdown.',
    ],
};

export const generativeUIPrompt = () => mattermostLibrary.prompt(promptOptions);
