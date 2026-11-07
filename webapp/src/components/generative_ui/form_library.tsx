// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {z} from 'zod/v4';
import {defineComponent} from '@openuidev/react-lang';

import * as F from './form_components';

const asArray = <T, >(value: T[] | undefined | null): T[] => (Array.isArray(value) ? value : []);

const fieldName = z.string().describe('Field id, letters, digits, - or _ only');

export const TextInput = defineComponent({
    name: 'TextInput',
    description: 'Single-line text field inside a Form.',
    props: z.object({
        name: fieldName,
        label: z.string(),
        placeholder: z.string().optional(),
        defaultValue: z.string().optional(),
        required: z.boolean().optional(),
    }),
    component: ({props}) => (
        <F.TextInputView
            name={props.name ?? ''}
            label={props.label ?? ''}
            placeholder={props.placeholder}
            defaultValue={props.defaultValue}
            required={props.required}
        />
    ),
});

export const TextArea = defineComponent({
    name: 'TextArea',
    description: 'Multi-line text field inside a Form.',
    props: z.object({
        name: fieldName,
        label: z.string(),
        placeholder: z.string().optional(),
        defaultValue: z.string().optional(),
        required: z.boolean().optional(),
    }),
    component: ({props}) => (
        <F.TextAreaView
            name={props.name ?? ''}
            label={props.label ?? ''}
            placeholder={props.placeholder}
            defaultValue={props.defaultValue}
            required={props.required}
        />
    ),
});

export const Select = defineComponent({
    name: 'Select',
    description: 'Dropdown to pick one of several options inside a Form.',
    props: z.object({
        name: fieldName,
        label: z.string(),
        options: z.array(z.string()),
        defaultValue: z.string().optional(),
        required: z.boolean().optional(),
    }),
    component: ({props}) => (
        <F.SelectView
            name={props.name ?? ''}
            label={props.label ?? ''}
            options={asArray(props.options).map(String)}
            defaultValue={props.defaultValue}
            required={props.required}
        />
    ),
});

export const RadioGroup = defineComponent({
    name: 'RadioGroup',
    description: 'Pick one of a few (2-5) options, all visible, inside a Form.',
    props: z.object({
        name: fieldName,
        label: z.string(),
        options: z.array(z.string()),
        defaultValue: z.string().optional(),
        required: z.boolean().optional(),
    }),
    component: ({props}) => (
        <F.RadioGroupView
            name={props.name ?? ''}
            label={props.label ?? ''}
            options={asArray(props.options).map(String)}
            defaultValue={props.defaultValue}
            required={props.required}
        />
    ),
});

export const Checkbox = defineComponent({
    name: 'Checkbox',
    description: 'Yes/no choice inside a Form.',
    props: z.object({
        name: fieldName,
        label: z.string(),
        defaultChecked: z.boolean().optional(),
        required: z.boolean().optional(),
    }),
    component: ({props}) => (
        <F.CheckboxView
            name={props.name ?? ''}
            label={props.label ?? ''}
            defaultChecked={props.defaultChecked}
            required={props.required}
        />
    ),
});

export const Button = defineComponent({
    name: 'Button',
    description: 'Clicking sends its label back to you as the user\'s reply, with the values of its Form when inside one.',
    props: z.object({
        label: z.string(),
        variant: z.enum(['primary', 'secondary', 'danger']).optional(),
    }),
    component: ({props}) => (
        <F.ButtonView
            label={props.label ?? ''}
            variant={props.variant}
        />
    ),
});

export const Buttons = defineComponent({
    name: 'Buttons',
    description: 'A row of Buttons outside a Form, e.g. suggested next steps.',
    props: z.object({
        items: z.array(Button.ref),
    }),
    component: ({props, renderNode}) => <F.ButtonRow>{renderNode(props.items)}</F.ButtonRow>,
});

export const Form = defineComponent({
    name: 'Form',
    description: 'Collects input from the user. name is a unique form id; buttons submit the form.',
    props: z.object({
        name: fieldName,
        fields: z.array(z.union([TextInput.ref, TextArea.ref, Select.ref, RadioGroup.ref, Checkbox.ref])),
        buttons: z.array(Button.ref),
    }),
    component: ({props, renderNode}) => (
        <F.FormView
            name={props.name ?? ''}
            fields={renderNode(props.fields)}
            buttons={renderNode(props.buttons)}
        />
    ),
});
