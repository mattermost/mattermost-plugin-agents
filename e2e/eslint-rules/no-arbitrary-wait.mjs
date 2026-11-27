// Fixed sleeps (`waitForTimeout`) and `networkidle` waits make Playwright specs
// both slow and flaky against Mattermost's persistent WebSocket and background
// requests. Wait on an observable UI or mock condition instead.

function isNetworkIdleLiteral(node) {
    return Boolean(node) && node.type === 'Literal' && node.value === 'networkidle';
}

function hasNetworkIdleWaitUntil(node) {
    if (!node || node.type !== 'ObjectExpression') {
        return false;
    }
    return node.properties.some(
        (prop) => prop.type === 'Property' &&
            !prop.computed &&
            prop.key.type === 'Identifier' &&
            prop.key.name === 'waitUntil' &&
            isNetworkIdleLiteral(prop.value),
    );
}

export default {
    meta: {
        type: 'problem',
        docs: {description: 'Disallow waitForTimeout and networkidle waits in Playwright tests'},
        messages: {
            timeout: 'Do not use waitForTimeout; wait on an observable condition (web-first expect, expect.poll, or mock request history).',
            networkidle: 'Do not wait on networkidle; wait on an observable DOM/API condition or domcontentloaded instead.',
        },
        schema: [],
    },
    create(context) {
        return {
            CallExpression(call) {
                if (call.callee.type !== 'MemberExpression' || call.callee.computed || call.callee.property.type !== 'Identifier') {
                    return;
                }
                const method = call.callee.property.name;
                if (method === 'waitForTimeout') {
                    context.report({node: call, messageId: 'timeout'});
                    return;
                }
                if (method === 'waitForLoadState' && isNetworkIdleLiteral(call.arguments[0])) {
                    context.report({node: call, messageId: 'networkidle'});
                    return;
                }
                if ((method === 'goto' || method === 'reload' || method === 'setContent') &&
                    call.arguments.some(hasNetworkIdleWaitUntil)) {
                    context.report({node: call, messageId: 'networkidle'});
                }
            },
        };
    },
};
