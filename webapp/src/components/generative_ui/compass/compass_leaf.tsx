// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {z} from 'zod/v4';
import {defineComponent} from '@openuidev/react-lang';

/**
 * Exposes a Compass union to the LLM. Keys are every Compass value (enforce
 * with `satisfies Record<CompassUnion, string | null>` so a new Compass value
 * fails tsc); values are the OpenUI name, or null to keep it hidden. Key order
 * is the order the prompt lists the values in.
 */
export function exposeValues<C extends string, O extends string>(map: Record<C, O | null>) {
    const toCompass = {} as Record<O, C>;
    for (const [compassValue, openuiValue] of Object.entries(map) as Array<[C, O | null]>) {
        if (openuiValue) {
            toCompass[openuiValue] = compassValue;
        }
    }
    return {
        map: map as Record<string, string | null>,
        schema: z.enum(Object.keys(toCompass) as [O, ...O[]]),
        toCompass: (value: unknown, fallback: string): string => (Object.hasOwn(toCompass, String(value)) ? toCompass[value as O] : fallback),
    };
}

export type ExposedValues = {
    map: Record<string, string | null>;
    toCompass: (value: unknown, fallback: string) => string;
};

type LeafArg = {

    /** Compass prop this positional argument is passed to. */
    prop: string;
    schema: z.ZodType;

    /** Translates enum arguments; values outside it render as `fallback`. */
    values?: ExposedValues;

    /** Passed when the argument is missing, e.g. mid-stream. */
    fallback?: unknown;
};

export type CompassBinding = {
    compass: string;
    args: string[];
    fixed: Record<string, unknown>;
};

const bindings = new Map<string, CompassBinding>();

/** Which Compass props each compassLeaf passes, keyed by OpenUI name. */
export const compassBindings: ReadonlyMap<string, CompassBinding> = bindings;

/**
 * Defines an OpenUI component that renders a single Compass component. The
 * renderer passes only the declared args and fixed props, so LLM output never
 * reaches callbacks, className, aria-* or slots.
 */
export function compassLeaf<P extends object>(config: {
    name: string;
    description: string;

    /** Compass export name, as listed in compass_manifest.snapshot.json. */
    compass: string;
    component: React.ComponentType<P>;

    /** Positional arguments in OpenUI order. Only append to this. */
    args: Record<string, LeafArg>;
    fixed?: Partial<P>;
}) {
    const entries = Object.entries(config.args);
    bindings.set(config.name, {
        compass: config.compass,
        args: entries.map(([, arg]) => arg.prop),
        fixed: {...config.fixed},
    });

    const Component = config.component as React.ComponentType<Record<string, unknown>>;
    return defineComponent({
        name: config.name,
        description: config.description,
        props: z.object(Object.fromEntries(entries.map(([key, arg]) => [key, arg.schema]))),
        component: ({props}) => {
            const passed: Record<string, unknown> = {...config.fixed};
            for (const [key, arg] of entries) {
                const raw = (props as Record<string, unknown>)[key];
                if (arg.values) {
                    passed[arg.prop] = arg.values.toCompass(raw, String(arg.fallback ?? ''));
                } else if (raw !== null && raw !== undefined) { // eslint-disable-line no-undefined
                    passed[arg.prop] = raw;
                } else if (arg.fallback !== undefined) { // eslint-disable-line no-undefined
                    passed[arg.prop] = arg.fallback;
                }
            }
            return <Component {...passed}/>;
        },
    });
}
