// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Any frame can post a message to the Mattermost window, and every `message`
// listener there receives it: the artifact's sandbox does not stop it. On
// servers that provide it, the web app's isolated frame registry delivers
// messages from a registered frame (and the frames nested in it) to that
// frame's handler alone, so a listener elsewhere on the page that skips its
// sender checks can never be reached from an artifact.
export type IsolatedFrameRegistry = {
    register: (frame: HTMLIFrameElement, handler: (event: MessageEvent) => void) => () => void;
};

// Returns the web app's isolated frame registry, or null on servers that
// predate it, where the artifact frame listens on the window itself.
export function isolatedFrameRegistry(): IsolatedFrameRegistry | null {
    const registry = window.WebappUtils?.isolatedFrames;
    return typeof registry?.register === 'function' ? registry : null;
}
