// testcontainers' exec resolves even when the command exits non-zero, so an
// unchecked call lets a failed setup step pass silently.

function isContainerExec(call) {
    const {callee} = call;
    if (callee.type !== 'MemberExpression' || callee.computed || callee.property.name !== 'exec') {
        return false;
    }
    const receiver = callee.object;
    const name = receiver.type === 'MemberExpression' ? receiver.property.name : receiver.name;
    return typeof name === 'string' && /container$/i.test(name);
}

// withDeadline(step, ms, () => container.exec(...)) resolves to the exec result,
// so the exit code has to be read where the withDeadline call lands.
function passedThroughDeadline(call) {
    const arrow = call.parent;
    const outer = arrow.parent;
    return arrow.type === 'ArrowFunctionExpression' && arrow.body === call &&
        outer.type === 'CallExpression' && outer.callee.type === 'Identifier' && outer.callee.name === 'withDeadline' &&
        outer.arguments.at(-1) === arrow;
}

// Before the await, exitCode would be read off the pending promise.
function readsExitCode(call, sourceCode) {
    if (passedThroughDeadline(call)) {
        return readsExitCode(call.parent.parent, sourceCode);
    }
    if (call.parent.type !== 'AwaitExpression') {
        return false;
    }
    const node = call.parent.parent;
    if (node.type === 'MemberExpression' && node.property.name === 'exitCode') {
        return true;
    }
    if (node.type !== 'VariableDeclarator' || node.id.type !== 'ObjectPattern') {
        return false;
    }
    const prop = node.id.properties.find((p) => p.type === 'Property' && p.key.name === 'exitCode');
    const variable = prop && sourceCode.getDeclaredVariables(node).find((v) => v.identifiers.includes(prop.value));
    return Boolean(variable) && variable.references.some((ref) => ref.isRead());
}

export default {
    meta: {
        type: 'problem',
        docs: {description: 'Check the exit code of container.exec'},
        messages: {
            unchecked: 'container.exec resolves on non-zero exit; await it and check exitCode (or use mattermost.mmctl).',
        },
        schema: [],
    },
    create(context) {
        return {
            CallExpression(call) {
                if (isContainerExec(call) && !readsExitCode(call, context.sourceCode)) {
                    context.report({node: call, messageId: 'unchecked'});
                }
            },
        };
    },
};
