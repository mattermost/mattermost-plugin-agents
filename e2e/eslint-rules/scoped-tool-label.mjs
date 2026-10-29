// Tool names render twice during the live-to-persisted handoff: on the tool
// card and in the activity header's "current" row. A text locator for a tool
// name that is not scoped to the card matches both and trips strict mode.
//
// Tool-name text is any identifier ending in `Label` (the spec convention for
// tool display names). It may only be located inside toolCard(...) or
// .locator(TOOL_CARD_SELECTOR). `expect(...).toHaveCount(0)` is exempt: a
// post-wide absence check is stronger, and counts are not strict.

const TEXT_METHODS = new Set(['getByText', 'getByRole', 'filter']);

function labelArgument(call) {
    const method = call.callee.property.name;
    const [first, second] = call.arguments;
    if (method === 'getByText') {
        return first;
    }
    const options = method === 'getByRole' ? second : first;
    if (!options || options.type !== 'ObjectExpression') {
        return null;
    }
    const key = method === 'getByRole' ? 'name' : 'hasText';
    const prop = options.properties.find((p) => p.type === 'Property' && p.key.type === 'Identifier' && p.key.name === key);
    return prop ? prop.value : null;
}

function isToolLabel(node) {
    return Boolean(node) && node.type === 'Identifier' && /Label$/.test(node.name);
}

function variableInit(sourceCode, identifier) {
    let scope = sourceCode.getScope(identifier);
    while (scope) {
        const variable = scope.set.get(identifier.name);
        if (variable) {
            const def = variable.defs[0];
            return def && def.type === 'Variable' ? def.node.init : null;
        }
        scope = scope.upper;
    }
    return null;
}

function isCardScoped(sourceCode, node, seen = new Set()) {
    if (!node || seen.has(node)) {
        return false;
    }
    seen.add(node);
    switch (node.type) {
    case 'AwaitExpression':
    case 'TSNonNullExpression':
    case 'TSAsExpression':
        return isCardScoped(sourceCode, node.argument ?? node.expression, seen);
    case 'Identifier':
        return isCardScoped(sourceCode, variableInit(sourceCode, node), seen);
    case 'CallExpression': {
        const {callee} = node;
        if (callee.type === 'Identifier') {
            return callee.name === 'toolCard';
        }
        if (callee.type !== 'MemberExpression') {
            return false;
        }
        const [first] = node.arguments;
        if (callee.property.name === 'locator' && first && first.type === 'Identifier' && first.name === 'TOOL_CARD_SELECTOR') {
            return true;
        }
        return isCardScoped(sourceCode, callee.object, seen);
    }
    case 'MemberExpression':
        return isCardScoped(sourceCode, node.object, seen);
    default:
        return false;
    }
}

function isAbsenceCheck(call) {
    const expectCall = call.parent;
    if (expectCall.type !== 'CallExpression' || expectCall.callee.type !== 'Identifier' || expectCall.callee.name !== 'expect') {
        return false;
    }
    const matcher = expectCall.parent;
    if (matcher.type !== 'MemberExpression' || matcher.property.name !== 'toHaveCount') {
        return false;
    }
    const [count] = matcher.parent.arguments;
    return Boolean(count) && count.type === 'Literal' && count.value === 0;
}

export default {
    meta: {
        type: 'problem',
        docs: {description: 'Locate tool names inside tool cards, not across the whole post'},
        messages: {
            unscoped: '`{{label}}` is a tool name and also renders in the activity header; locate it with toolCard(scope, {{label}}).',
        },
        schema: [],
    },
    create(context) {
        const {sourceCode} = context;
        return {
            CallExpression(call) {
                if (call.callee.type !== 'MemberExpression' || call.callee.property.type !== 'Identifier' || !TEXT_METHODS.has(call.callee.property.name)) {
                    return;
                }
                const label = labelArgument(call);
                if (!isToolLabel(label) || isCardScoped(sourceCode, call.callee.object) || isAbsenceCheck(call)) {
                    return;
                }
                context.report({node: call, messageId: 'unscoped', data: {label: label.name}});
            },
        };
    },
};
