// Hooks and test bodies run on Playwright's 60 s clock, which container startup
// can exceed on a slow runner; containerTest factories run without a timeout.

const CONTAINER_START = /^(Run[A-Z]\w*|start\w*Stack)$/;
const HOOKS = new Set(['beforeAll', 'beforeEach', 'afterAll', 'afterEach']);
const TEST_MODIFIERS = new Set(['only', 'skip', 'fixme', 'fail']);

function isTestType(node) {
    return node.type === 'Identifier' && /test$/i.test(node.name);
}

function isHookOrTest(callee) {
    if (callee.type === 'Identifier') {
        return isTestType(callee);
    }
    if (callee.type !== 'MemberExpression' || callee.computed) {
        return false;
    }
    const {name} = callee.property;
    return HOOKS.has(name) || (TEST_MODIFIERS.has(name) && isTestType(callee.object));
}

function enclosingCallback(node) {
    for (let fn = node.parent; fn; fn = fn.parent) {
        if ((fn.type === 'ArrowFunctionExpression' || fn.type === 'FunctionExpression') &&
            fn.parent.type === 'CallExpression' && fn.parent.arguments.includes(fn)) {
            return fn.parent.callee;
        }
    }
    return null;
}

export default {
    meta: {
        type: 'problem',
        docs: {description: 'Start containers in a containerTest factory, not in a hook or test body'},
        messages: {
            inHook: 'Start containers in a containerTest factory (helpers/container-fixture); hooks and tests run on the 60 s test clock.',
        },
        schema: [],
    },
    create(context) {
        return {
            CallExpression(call) {
                if (call.callee.type !== 'Identifier' || !CONTAINER_START.test(call.callee.name)) {
                    return;
                }
                for (let node = call; ;) {
                    const callee = enclosingCallback(node);
                    if (!callee || (callee.type === 'Identifier' && callee.name === 'containerTest')) {
                        return;
                    }
                    if (isHookOrTest(callee)) {
                        context.report({node: call, messageId: 'inHook'});
                        return;
                    }
                    node = callee.parent;
                }
            },
        };
    },
};
