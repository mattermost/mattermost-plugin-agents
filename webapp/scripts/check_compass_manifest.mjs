#!/usr/bin/env node
// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Drift tripwire for the pinned @mattermost/compass-ui version. Extracts every
// exported component and its own props (inherited HTML attributes excluded)
// from the installed package's .d.ts files and compares the result with the
// committed snapshot that the generative UI coverage tests read
// (src/components/generative_ui/compass/compass_coverage.test.ts).
//
// After bumping Compass, run `npm run update-compass-manifest`, review the
// snapshot diff, then reconcile compass_coverage.ts until jest passes.

/* eslint-disable no-console, no-process-exit */

import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

import ts from 'typescript';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webappDir = path.resolve(scriptDir, '..');
const packageDir = path.join(webappDir, 'node_modules', '@mattermost', 'compass-ui');
const componentsDir = path.join(packageDir, 'dist', 'components');
const snapshotPath = path.join(webappDir, 'src', 'components', 'generative_ui', 'compass', 'compass_manifest.snapshot.json');
const update = process.argv.includes('--update');

function extract() {
    const subpaths = fs.readdirSync(componentsDir).
        filter((dir) => fs.existsSync(path.join(componentsDir, dir, 'index.d.ts'))).
        sort();
    const entries = subpaths.map((dir) => path.join(componentsDir, dir, 'index.d.ts'));

    const program = ts.createProgram(entries, {
        jsx: ts.JsxEmit.ReactJSX,
        moduleResolution: ts.ModuleResolutionKind.Bundler,
        module: ts.ModuleKind.ESNext,
        target: ts.ScriptTarget.ES2022,
        skipLibCheck: true,
        typeRoots: [path.join(webappDir, 'node_modules', '@types')],
        types: ['react'],
    });
    const checker = program.getTypeChecker();
    const inPackage = (decl) => path.resolve(decl.getSourceFile().fileName).startsWith(packageDir);

    const kindOf = (type) => {
        if (type.getCallSignatures().length > 0) {
            return 'callback';
        }
        return (/ReactNode|ReactElement/).test(checker.typeToString(type)) ? 'node' : 'data';
    };

    const literalValues = (type) => {
        const parts = type.isUnion() ? type.types : [type];
        const values = parts.filter((part) => part.isStringLiteral()).map((part) => part.value);
        return values.length ? values.sort() : undefined; // eslint-disable-line no-undefined
    };

    const components = {};
    entries.forEach((entry, index) => {
        const moduleSymbol = checker.getSymbolAtLocation(program.getSourceFile(entry));
        if (!moduleSymbol) {
            return;
        }
        for (const exported of checker.getExportsOfModule(moduleSymbol)) {
            const symbol = exported.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(exported) : exported;
            const decl = symbol.valueDeclaration;
            if (!decl || !(/^[A-Z]/).test(exported.name)) {
                continue;
            }
            const signatures = checker.getTypeOfSymbolAtLocation(symbol, decl).getCallSignatures();
            if (!signatures.length) {
                continue;
            }

            const props = {};
            const param = signatures[0].getParameters()[0];
            if (param) {
                const propsType = checker.getTypeOfSymbolAtLocation(param, decl);
                const seen = new Map();
                for (const member of propsType.isUnion() ? propsType.types : [propsType]) {
                    for (const prop of member.getProperties()) {
                        if (!seen.has(prop.name)) {
                            seen.set(prop.name, prop);
                        }
                    }
                }
                for (const prop of [...seen.values()].sort((a, b) => a.name.localeCompare(b.name))) {
                    const propDecl = prop.declarations?.[0];
                    if (!propDecl || !inPackage(propDecl)) {
                        continue;
                    }
                    const propType = checker.getNonNullableType(checker.getTypeOfSymbolAtLocation(prop, propDecl));
                    props[prop.name] = {
                        kind: kindOf(propType),
                        optional: Boolean(prop.flags & ts.SymbolFlags.Optional),
                        values: literalValues(propType),
                    };
                }
            }
            components[exported.name] = {subpath: `components/${subpaths[index]}`, props};
        }
    });

    const version = JSON.parse(fs.readFileSync(path.join(packageDir, 'package.json'), 'utf8')).version;
    const sorted = Object.fromEntries(Object.entries(components).sort(([a], [b]) => a.localeCompare(b)));
    return {package: '@mattermost/compass-ui', version, components: sorted};
}

// One line per prop keeps bump diffs readable.
function serialize(manifest) {
    const lines = ['{', `    "package": ${JSON.stringify(manifest.package)},`, `    "version": ${JSON.stringify(manifest.version)},`, '    "components": {'];
    const names = Object.keys(manifest.components);
    names.forEach((name, i) => {
        const {subpath, props} = manifest.components[name];
        const propNames = Object.keys(props);
        const trailer = i === names.length - 1 ? '' : ',';
        if (!propNames.length) {
            lines.push(`        ${JSON.stringify(name)}: {"subpath": ${JSON.stringify(subpath)}, "props": {}}${trailer}`);
            return;
        }
        lines.push(`        ${JSON.stringify(name)}: {`, `            "subpath": ${JSON.stringify(subpath)},`, '            "props": {');
        propNames.forEach((prop, j) => {
            lines.push(`                ${JSON.stringify(prop)}: ${JSON.stringify(props[prop])}${j === propNames.length - 1 ? '' : ','}`);
        });
        lines.push('            }', `        }${trailer}`);
    });
    lines.push('    }', '}', '');
    return lines.join('\n');
}

if (!fs.existsSync(componentsDir)) {
    console.error(`check-compass-manifest: ${path.relative(webappDir, componentsDir)} not found; run npm ci first.`);
    process.exit(1);
}

const generated = serialize(extract());

if (update) {
    fs.writeFileSync(snapshotPath, generated);
    console.log(`check-compass-manifest: wrote ${path.relative(webappDir, snapshotPath)}.`);
    process.exit(0);
}

const committed = fs.existsSync(snapshotPath) ? fs.readFileSync(snapshotPath, 'utf8') : '';
if (committed !== generated) {
    console.error('check-compass-manifest: FAILED — the committed Compass manifest snapshot does not match the installed @mattermost/compass-ui.');
    console.error('Run `npm run update-compass-manifest`, review the diff, then update compass/compass_coverage.ts until the generative UI tests pass.');
    process.exit(1);
}
console.log('check-compass-manifest: OK — snapshot matches the installed @mattermost/compass-ui.');
