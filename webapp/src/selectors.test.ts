// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from './manifest';
import {getCustomPrompts, getPinnedPromptIds} from './selectors';

type SelectorState = Parameters<typeof getCustomPrompts>[0];

const makeState = (pluginState?: Record<string, unknown>) => ({
    [`plugins-${manifest.id}`]: pluginState,
}) as unknown as SelectorState;

describe('array selectors return stable references while unloaded', () => {
    const selectors = [
        {name: 'getCustomPrompts', select: getCustomPrompts},
        {name: 'getPinnedPromptIds', select: getPinnedPromptIds},
    ];

    const states = [
        {desc: 'null plugin state values', state: makeState({customPrompts: null, pinnedPromptIds: null})},
        {desc: 'missing plugin state', state: makeState()},
    ];

    describe.each(selectors)('$name', ({select}) => {
        it.each(states)('returns the same empty array for $desc', ({state}) => {
            const first = select(state);
            const second = select(state);

            expect(first).toEqual([]);
            expect(second).toBe(first);
        });
    });

    it('returns the stored arrays once loaded', () => {
        const customPrompts = [{id: 'p1'}];
        const pinnedPromptIds = ['p1'];
        const state = makeState({customPrompts, pinnedPromptIds});

        expect(getCustomPrompts(state)).toBe(customPrompts);
        expect(getPinnedPromptIds(state)).toBe(pinnedPromptIds);
    });
});
