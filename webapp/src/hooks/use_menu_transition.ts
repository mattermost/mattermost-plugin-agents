// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useState} from 'react';

/**
 * Keeps a compass PopoverMenu mounted while it plays its exit animation.
 * Pass `rendered` as `isOpen` to the Dropdown that positions the menu, give the
 * menu `open={isOpen}` and `onExited`, and the menu unmounts once it has faded out.
 */
export function useMenuTransition(isOpen: boolean) {
    const [mounted, setMounted] = useState(isOpen);

    useEffect(() => {
        if (isOpen) {
            setMounted(true);
        }
    }, [isOpen]);

    const onExited = useCallback(() => setMounted(false), []);

    return {rendered: isOpen || mounted, onExited};
}
