// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

const BRIDGE = fs.readFileSync(path.join(__dirname, '../../../../api/artifact_bridge.js'), 'utf8').
    replace('/*MM_ARTIFACT_BRIDGE_TOKEN*/null', '"tok"').
    replace('/*MM_ARTIFACT_BRIDGE_TARGET_ORIGIN*/null', '"*"');

type ArtifactWindow = Window & {
    mattermost: {download: (options: Record<string, unknown>) => Promise<unknown>};
    eval: (code: string) => unknown;
};

// Runs the bridge in a frame nested two deep, as on the host page.
function loadBridge(activation: {active: boolean} | null) {
    const wrapper = document.createElement('iframe');
    document.body.appendChild(wrapper);
    const inner = wrapper.contentDocument!.createElement('iframe');
    wrapper.contentDocument!.body.appendChild(inner);
    const win = inner.contentWindow as unknown as ArtifactWindow;
    if (activation) {
        const state = activation;
        class UserActivation {
            get isActive() {
                return state.active;
            }
        }
        Object.defineProperty(win.navigator, 'userActivation', {value: new UserActivation(), configurable: true});
    }
    const posted = jest.spyOn(window, 'postMessage').mockImplementation(() => undefined); // eslint-disable-line no-undefined
    win.eval(BRIDGE);
    return {win, posted};
}

afterEach(() => {
    jest.restoreAllMocks();
    document.body.innerHTML = '';
});

describe('artifact bridge download() user activation', () => {
    test.each([
        {name: 'without user activation, download() is rejected and nothing reaches the host', activation: {active: false}, posts: 0, code: 'permission_denied'},
        {name: 'with user activation, download() is sent to the host', activation: {active: true}, posts: 1, code: ''},
        {name: 'in a browser without the API, download() is sent to the host', activation: null, posts: 1, code: ''},
    ])('$name', async ({activation, posts, code}) => {
        const {win, posted} = loadBridge(activation);
        const result = win.mattermost.download({mimeType: 'text/plain', data: 'x'});
        if (code) {
            await expect(result).rejects.toMatchObject({code});
        }
        const downloads = posted.mock.calls.filter(([message]) => (message as {method?: string}).method === 'download');
        expect(downloads).toHaveLength(posts);
    });
});
