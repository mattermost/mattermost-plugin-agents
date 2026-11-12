// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act} from '@testing-library/react';

// jsdom has no layout, so the pinned skills (which measure their buttons and the space they have) see
// zero everywhere and render nothing. Call this inside a describe: it gives every measured piece a
// width of 10 per character (a prompt called "Prompt 1" is 80, a "+2" chip is 20, no gap between them)
// and the pinned skills group the width a test sets, 1000 unless it says otherwise.
export function usePinnedSkillsLayoutMock(initialWidth = 1000) {
    const state = {width: initialWidth};

    // Floating UI (an open menu) observes too, and reads the entries it is called with.
    const observers: Array<(entries: unknown[]) => void> = [];

    class FakeResizeObserver {
        observe = jest.fn();
        disconnect = jest.fn();
        constructor(callback: (entries: unknown[]) => void) {
            observers.push(callback);
        }
    }

    beforeEach(() => {
        state.width = initialWidth;
        observers.length = 0;
        (global as any).ResizeObserver = FakeResizeObserver;
        jest.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function measuredWidth(this: HTMLElement) {
            return (this.textContent?.length ?? 0) * 10;
        });
        jest.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function groupClientWidth(this: HTMLElement) {
            return this.dataset.testid === 'agent-chat-pinned-skills' ? state.width : 0;
        });
    });

    afterEach(() => {
        jest.restoreAllMocks();
        delete (global as any).ResizeObserver;
    });

    return {
        setWidth: (width: number) => {
            state.width = width;
        },

        /** Tells every observer the size changed, as the browser does after a resize. */
        resize: () => act(() => observers.forEach((callback) => callback([]))),
    };
}
