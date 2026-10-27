import containerExecExitCode from './container-exec-exit-code.mjs';
import containerStartInFixture from './container-start-in-fixture.mjs';
import scopedToolLabel from './scoped-tool-label.mjs';

export default {
    rules: {
        'container-exec-exit-code': containerExecExitCode,
        'container-start-in-fixture': containerStartInFixture,
        'scoped-tool-label': scopedToolLabel,
    },
};
