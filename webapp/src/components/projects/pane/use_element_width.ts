// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useLayoutEffect, useRef, useState} from 'react';

// The width of the element the returned ref is attached to, kept current as it resizes. It is 0
// until measured.
export function useElementWidth<T extends HTMLElement>() {
    const ref = useRef<T>(null);
    const [width, setWidth] = useState(0);

    useLayoutEffect(() => {
        const el = ref.current;
        if (!el) {
            return () => {/* nothing measured */};
        }
        const measure = () => setWidth(el.getBoundingClientRect().width);
        measure();
        if (typeof ResizeObserver === 'undefined') {
            return () => {/* measured once */};
        }
        const observer = new ResizeObserver(measure);
        observer.observe(el);
        return () => observer.disconnect();
    }, []);

    return [ref, width] as const;
}
