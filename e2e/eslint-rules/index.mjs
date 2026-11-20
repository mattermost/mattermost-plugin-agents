import containerExecExitCode from './container-exec-exit-code.mjs';
import containerStartInFixture from './container-start-in-fixture.mjs';
import noArbitraryWait from './no-arbitrary-wait.mjs';
import scopedToolLabel from './scoped-tool-label.mjs';

export default {
    rules: {
        'container-exec-exit-code': containerExecExitCode,
        'container-start-in-fixture': containerStartInFixture,
        'no-arbitrary-wait': noArbitraryWait,
        'scoped-tool-label': scopedToolLabel,
    },
};
