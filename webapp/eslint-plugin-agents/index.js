// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

const jsxA11y = require('eslint-plugin-jsx-a11y');

// jsx-a11y only recognizes lowercase DOM tags, so it skips <Header onClick>
// when Header is `styled.div`. These wrappers map each file's top-level
// styled-components declarations to their DOM tag before delegating.
const A11Y_RULES = [
    'alt-text',
    'anchor-has-content',
    'aria-activedescendant-has-tabindex',
    'aria-props',
    'aria-proptypes',
    'aria-role',
    'click-events-have-key-events',
    'control-has-associated-label',
    'iframe-has-title',
    'interactive-supports-focus',
    'no-noninteractive-element-interactions',
    'no-noninteractive-tabindex',
    'no-static-element-interactions',
    'role-has-required-aria-props',
    'role-supports-aria-props',
    'tabindex-no-positive',
];

// Returns {tag} for styled.div / styled('div'), {base} for styled(Other),
// following .attrs(...) and template tags.
function styledTag(node) {
    switch (node?.type) {
    case 'TaggedTemplateExpression':
        return styledTag(node.tag);
    case 'CallExpression': {
        if (node.callee.type !== 'Identifier' || node.callee.name !== 'styled') {
            return styledTag(node.callee);
        }
        const [arg] = node.arguments;
        if (arg?.type === 'Literal') {
            return {tag: arg.value};
        }
        return arg?.type === 'Identifier' ? {base: arg.name} : null;
    }
    case 'MemberExpression':
        if (node.object.type === 'Identifier' && node.object.name === 'styled') {
            return {tag: node.property.name};
        }
        return styledTag(node.object);
    default:
        return null;
    }
}

function styledComponentTags(program) {
    const declared = new Map();
    for (const statement of program.body) {
        const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement;
        if (declaration?.type !== 'VariableDeclaration') {
            continue;
        }
        for (const {id, init} of declaration.declarations) {
            const found = id.type === 'Identifier' && styledTag(init);
            if (found) {
                declared.set(id.name, found);
            }
        }
    }

    const resolve = (name, seen = new Set()) => {
        const found = declared.get(name);
        if (!found || seen.has(name)) {
            return null;
        }
        seen.add(name);
        return found.tag || resolve(found.base, seen);
    };
    return Object.fromEntries([...declared.keys()].map((name) => [name, resolve(name)]).filter(([, tag]) => tag));
}

function withStyledComponents(rule) {
    return {
        meta: rule.meta,
        create(context) {
            const settings = {
                ...context.settings,
                'jsx-a11y': {...context.settings['jsx-a11y'], components: styledComponentTags(context.sourceCode.ast)},
            };
            return rule.create(Object.create(context, {settings: {value: settings}}));
        },
    };
}

function recommendedSeverity(name) {
    const entry = jsxA11y.configs.recommended.rules[`jsx-a11y/${name}`];
    return Array.isArray(entry) ? ['error', ...entry.slice(1)] : 'error';
}

module.exports = {
    rules: Object.fromEntries(A11Y_RULES.map((name) => [name, withStyledComponents(jsxA11y.rules[name])])),
    configs: {
        recommended: {
            plugins: ['agents'],
            settings: {'jsx-a11y': {polymorphicPropName: 'as'}},
            rules: Object.fromEntries(A11Y_RULES.map((name) => [`agents/${name}`, recommendedSeverity(name)])),
        },
    },
};
