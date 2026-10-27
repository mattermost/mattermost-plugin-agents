// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RefObject, useCallback, useEffect, useState} from 'react';

import {THREAD_SCROLLER_SELECTOR} from './agent_chat_host_styles';

// Further than this from the bottom counts as scrolled up (a few lines of text). No compass
// token covers a scroll distance.
export const SCROLLED_UP_THRESHOLD_PX = 80;

export interface ThreadScroll {

    // The host thread viewer is scrolled away from its newest content.
    scrolledUp: boolean;
    scrollToBottom: () => void;
}

// Follows the host thread viewer's scroll container inside `paneRef`: scroll events (they do not
// bubble, so they are caught in the capture phase) and size changes of the scroller and its
// content, which streaming text grows without scrolling. The container renders after the pane
// does, so it is looked up again whenever the pane's markup changes.
export function useThreadScroll(paneRef: RefObject<HTMLElement | null>): ThreadScroll {
    const [scrolledUp, setScrolledUp] = useState(false);

    useEffect(() => {
        const pane = paneRef.current;
        if (!pane) {
            return () => null;
        }
        let scroller: HTMLElement | null = null;
        const resizeObserver = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => measure());

        function measure() {
            if (!scroller) {
                setScrolledUp(false);
                return;
            }
            const distance = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight;
            setScrolledUp(distance > SCROLLED_UP_THRESHOLD_PX);
        }

        function attach() {
            const found = pane!.querySelector<HTMLElement>(THREAD_SCROLLER_SELECTOR);
            if (found === scroller) {
                return;
            }
            resizeObserver?.disconnect();
            scroller = found;
            if (scroller) {
                resizeObserver?.observe(scroller);
                Array.from(scroller.children).forEach((child) => resizeObserver?.observe(child));
            }
            measure();
        }

        const onScroll = (e: Event) => {
            if (scroller && e.target === scroller) {
                measure();
            }
        };
        pane.addEventListener('scroll', onScroll, true);
        const mutationObserver = new MutationObserver(attach);
        mutationObserver.observe(pane, {childList: true, subtree: true});
        attach();

        return () => {
            pane.removeEventListener('scroll', onScroll, true);
            mutationObserver.disconnect();
            resizeObserver?.disconnect();
        };
    }, [paneRef]);

    const scrollToBottom = useCallback(() => {
        const scroller = paneRef.current?.querySelector<HTMLElement>(THREAD_SCROLLER_SELECTOR);
        scroller?.scrollTo({top: scroller.scrollHeight, behavior: 'smooth'});
    }, [paneRef]);

    return {scrolledUp, scrollToBottom};
}
