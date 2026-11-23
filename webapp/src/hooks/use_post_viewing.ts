// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useRef} from 'react';

import {getViewingTracker, ViewingRegistration} from '@/viewing';

// Registers the returned element as a rendering of the post. While active
// (the response is still being produced) and on screen, the server is told
// this user is watching it.
export function usePostViewing<T extends Element>(postId: string, active: boolean) {
    const elementRef = useRef<T>(null);
    const registrationRef = useRef<ViewingRegistration | null>(null);

    useEffect(() => {
        const element = elementRef.current;
        if (!element) {
            return undefined; // eslint-disable-line no-undefined
        }
        const registration = getViewingTracker().register(postId, element);
        registrationRef.current = registration;
        return () => {
            registration.dispose();
            registrationRef.current = null;
        };
    }, [postId]);

    useEffect(() => {
        registrationRef.current?.setActive(active);
    }, [active, postId]);

    return elementRef;
}
