// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import packageJSON from '../../../../package.json';
import {mattermostLibrary} from '../library';

import {compassCoverage} from './compass_coverage';
import {compassBindings} from './compass_leaf';
import manifest from './compass_manifest.snapshot.json';

type ManifestProp = {kind: string; optional: boolean; values?: string[]};
const components = manifest.components as Record<string, {subpath: string; props: Record<string, ManifestProp>}>;

const sorted = (values: Iterable<string>) => [...values].sort();
const mapped = Object.entries(compassCoverage).flatMap(([name, entry]) => (entry.status === 'mapped' ? [{name, entry}] : []));
const propsWith = (props: Record<string, string>, decision: string) => sorted(Object.keys(props).filter((prop) => props[prop] === decision));

// When a Compass bump fails here, update compass_coverage.ts (and the leaves in
// library.tsx) to decide how the change is exposed to the LLM.
describe('Compass coverage', () => {
    it('is pinned to the exact version the manifest snapshot was taken from', () => {
        const pinned = packageJSON.dependencies['@mattermost/compass-ui'];
        expect(pinned).toMatch(/^\d+\.\d+\.\d+(-[\w.]+)?$/);
        expect(manifest.version).toBe(pinned);
    });

    it('classifies every Compass export, and only those', () => {
        expect(sorted(Object.keys(compassCoverage))).toEqual(sorted(Object.keys(components)));
    });

    it.each(mapped)('decides on every own prop of $name', ({name, entry}) => {
        expect(sorted(Object.keys(entry.props))).toEqual(sorted(Object.keys(components[name].props)));
    });

    it.each(mapped)('maps every value of the literal-union args of $name', ({name, entry}) => {
        for (const prop of propsWith(entry.props, 'arg')) {
            const values = components[name].props[prop].values;
            if (values) {
                expect({prop, values: sorted(Object.keys(entry.values?.[prop]?.map ?? {}))}).toEqual({prop, values});
            }
        }
    });

    it.each(mapped)('renders $name only from OpenUI components that exist', ({entry}) => {
        for (const openui of entry.openui) {
            expect(mattermostLibrary.components).toHaveProperty(openui);
        }
    });

    it.each([...compassBindings.entries()].map(([openui, binding]) => ({openui, binding})))('binds $openui to the props its coverage entry declares', ({openui, binding}) => {
        const entry = compassCoverage[binding.compass];
        if (entry?.status !== 'mapped') {
            throw new Error(`${binding.compass} is used by ${openui} but is not mapped in compass_coverage.ts`);
        }
        expect(entry.openui).toContain(openui);
        expect(sorted(binding.args)).toEqual(propsWith(entry.props, 'arg'));
        expect(sorted(Object.keys(binding.fixed))).toEqual(propsWith(entry.props, 'fixed'));
        for (const [prop, value] of Object.entries(binding.fixed)) {
            const values = components[binding.compass].props[prop].values;
            if (values) {
                expect(values).toContain(value);
            }
        }
    });
});
