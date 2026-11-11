// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

import {mattermostLibrary} from './library';

// Agent replies are persisted posts, and OpenUI Lang arguments are positional,
// so the grammar may only grow: new components, new optional trailing
// arguments, and wider enums or unions. Anything else changes how old posts
// render. `npm run update-genui-contract` refreshes the snapshot, and refuses
// to when the change is breaking.

type Schema = Record<string, unknown>;
type Arg = {name: string; required: boolean; schema: Schema};
type Contract = {root: string; components: Record<string, Arg[]>};

const SNAPSHOT_PATH = path.resolve(__dirname, 'library_contract.snapshot.json');

function currentContract(): Contract {
    const defs = (mattermostLibrary.toJSONSchema().$defs ?? {}) as Record<string, {properties?: Record<string, Schema>; required?: string[]}>;
    const components: Record<string, Arg[]> = {};
    for (const name of Object.keys(mattermostLibrary.components).sort()) {
        const def = defs[name] ?? {};
        components[name] = Object.entries(def.properties ?? {}).map(([arg, schema]) => ({
            name: arg,
            required: (def.required ?? []).includes(arg),
            schema,
        }));
    }
    return {root: mattermostLibrary.root ?? '', components};
}

// One line per argument keeps snapshot diffs readable.
function serialize(contract: Contract): string {
    const names = Object.keys(contract.components);
    const lines = ['{', `    "root": ${JSON.stringify(contract.root)},`, '    "components": {'];
    names.forEach((name, i) => {
        const args = contract.components[name];
        const trailer = i === names.length - 1 ? '' : ',';
        if (!args.length) {
            lines.push(`        ${JSON.stringify(name)}: []${trailer}`);
            return;
        }
        lines.push(`        ${JSON.stringify(name)}: [`);
        args.forEach((arg, j) => lines.push(`            ${JSON.stringify(arg)}${j === args.length - 1 ? '' : ','}`));
        lines.push(`        ]${trailer}`);
    });
    lines.push('    }', '}', '');
    return lines.join('\n');
}

const asSet = (value: unknown) => new Set(Array.isArray(value) ? value : [value]);
const isSubset = (a: Set<unknown>, b: Set<unknown>) => [...a].every((item) => b.has(item));

/** True when every value `before` accepts is still accepted by `after`. */
export function widens(before: Schema, after: Schema): boolean {
    if (JSON.stringify(before) === JSON.stringify(after)) {
        return true;
    }
    if (Array.isArray(before.anyOf)) {
        const options = Array.isArray(after.anyOf) ? after.anyOf as Schema[] : [after];
        return (before.anyOf as Schema[]).every((option) => options.some((candidate) => widens(option, candidate)));
    }
    if (Array.isArray(after.anyOf)) {
        return (after.anyOf as Schema[]).some((candidate) => widens(before, candidate));
    }
    if (before.enum || after.enum) {
        return after.enum ? Boolean(before.enum) && isSubset(asSet(before.enum), asSet(after.enum)) : isSubset(asSet(before.type), asSet(after.type));
    }
    if (before.type === 'array' && after.type === 'array') {
        return widens((before.items ?? {}) as Schema, (after.items ?? {}) as Schema);
    }
    const {type: beforeType, ...beforeRest} = before;
    const {type: afterType, ...afterRest} = after;
    return isSubset(asSet(beforeType), asSet(afterType)) && JSON.stringify(beforeRest) === JSON.stringify(afterRest);
}

export function breakingChanges(before: Contract, after: Contract): string[] {
    const problems: string[] = [];
    if (before.root && before.root !== after.root) {
        problems.push(`root changed from ${before.root} to ${after.root}`);
    }
    for (const [name, oldArgs] of Object.entries(before.components)) {
        const newArgs = after.components[name];
        if (!newArgs) {
            problems.push(`${name} was removed`);
            continue;
        }
        oldArgs.forEach((oldArg, index) => {
            const newArg = newArgs[index];
            if (!newArg) {
                problems.push(`${name} argument ${index + 1} (${oldArg.name}) was removed`);
            } else if (newArg.name !== oldArg.name) {
                problems.push(`${name} argument ${index + 1} changed from ${oldArg.name} to ${newArg.name}`);
            } else if (newArg.required && !oldArg.required) {
                problems.push(`${name}.${oldArg.name} became required`);
            } else if (!widens(oldArg.schema, newArg.schema)) {
                problems.push(`${name}.${oldArg.name} no longer accepts everything it did`);
            }
        });
        newArgs.slice(oldArgs.length).filter((arg) => arg.required).forEach((arg) => {
            problems.push(`${name}.${arg.name} was added as a required argument`);
        });
    }
    return problems;
}

describe('generative UI grammar contract', () => {
    const committed: Contract = fs.existsSync(SNAPSHOT_PATH) ? JSON.parse(fs.readFileSync(SNAPSHOT_PATH, 'utf8')) : {root: '', components: {}};
    const current = currentContract();

    it('only grows: no removed, reordered, narrowed or newly required arguments', () => {
        expect(breakingChanges(committed, current)).toEqual([]);
    });

    it('matches the committed snapshot', () => {
        const expected = serialize(current);

        // eslint-disable-next-line no-process-env -- opt-in regeneration switch set by `npm run update-genui-contract`
        if (process.env.UPDATE_GENERATIVE_UI_CONTRACT === '1' && breakingChanges(committed, current).length === 0) {
            fs.writeFileSync(SNAPSHOT_PATH, expected);
        }
        expect(fs.readFileSync(SNAPSHOT_PATH, 'utf8')).toBe(expected);
    });

    it.each([
        {name: 'an appended optional argument', after: {root: 'Stack', components: {A: [{name: 'x', required: true, schema: {type: 'string'}}, {name: 'y', required: false, schema: {type: 'string'}}]}}, breaking: false},
        {name: 'a wider enum', after: {root: 'Stack', components: {A: [{name: 'x', required: true, schema: {type: 'string', enum: ['a', 'b', 'c']}}]}}, breaking: false},
        {name: 'a narrower enum', after: {root: 'Stack', components: {A: [{name: 'x', required: true, schema: {type: 'string', enum: ['a']}}]}}, breaking: true},
        {name: 'a renamed argument', after: {root: 'Stack', components: {A: [{name: 'z', required: true, schema: {type: 'string', enum: ['a', 'b']}}]}}, breaking: true},
        {name: 'an appended required argument', after: {root: 'Stack', components: {A: [{name: 'x', required: true, schema: {type: 'string', enum: ['a', 'b']}}, {name: 'y', required: true, schema: {type: 'string'}}]}}, breaking: true},
        {name: 'a removed component', after: {root: 'Stack', components: {}}, breaking: true},
    ])('treats $name as breaking: $breaking', ({after, breaking}) => {
        const before: Contract = {root: 'Stack', components: {A: [{name: 'x', required: true, schema: {type: 'string', enum: ['a', 'b']}}]}};
        expect(breakingChanges(before, after as Contract).length > 0).toBe(breaking);
    });

    it('accepts a union gaining a member and rejects one losing it', () => {
        const union = (...refs: string[]) => ({type: 'array', items: {anyOf: refs.map((ref) => ({$ref: `#/$defs/${ref}`}))}});
        expect(widens(union('A', 'B'), union('A', 'B', 'C'))).toBe(true);
        expect(widens(union('A', 'B'), union('A'))).toBe(false);
        expect(widens({type: 'string'}, {type: ['string', 'number']})).toBe(true);
        expect(widens({type: ['string', 'number']}, {type: 'string'})).toBe(false);
    });
});
