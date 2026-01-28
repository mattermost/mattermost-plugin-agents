// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useLayoutEffect, useMemo, useRef, useState, useCallback} from 'react';
import {useSelector} from 'react-redux';
import {GlobalState} from '@mattermost/types/store';
import {PreferenceType} from '@mattermost/types/preferences';
import {Client4 as Client4Class} from '@mattermost/client';

import {FINISHED, TTCategoriesMapToSteps} from './tours';

const Client4 = new Client4Class();

export type Punchout = {
    x: string;
    y: string;
    width: string;
    height: string;
};

type PunchoutOffset = {
    x: number;
    y: number;
    width: number;
    height: number;
};

const PREFERENCE_CATEGORY = 'mattermost-ai-tutorial';

export function useMeasurePunchouts(
    elementIds: string[],
    additionalDeps: unknown[] = [],
    offset?: PunchoutOffset,
): Punchout | null {
    const elementsAvailable = useElementAvailable(elementIds);
    const [windowSize, setWindowSize] = useState<DOMRect>();

    useLayoutEffect(() => {
        let timeoutId: NodeJS.Timeout;
        const updateSize = () => {
            clearTimeout(timeoutId);
            timeoutId = setTimeout(() => {
                setWindowSize(document.getElementById('root')?.getBoundingClientRect());
            }, 100);
        };

        window.addEventListener('resize', updateSize);
        return () => {
            window.removeEventListener('resize', updateSize);
            clearTimeout(timeoutId);
        };
    }, []);

    return useMemo(() => {
        let minX = Number.MAX_SAFE_INTEGER;
        let minY = Number.MAX_SAFE_INTEGER;
        let maxX = Number.MIN_SAFE_INTEGER;
        let maxY = Number.MIN_SAFE_INTEGER;

        for (const id of elementIds) {
            const el = document.getElementById(id);
            if (!el) {
                return null;
            }

            const {x, y, width, height} = el.getBoundingClientRect();
            minX = Math.min(minX, x);
            minY = Math.min(minY, y);
            maxX = Math.max(maxX, x + width);
            maxY = Math.max(maxY, y + height);
        }

        return {
            x: `${minX + (offset?.x ?? 0)}px`,
            y: `${minY + (offset?.y ?? 0)}px`,
            width: `${(maxX - minX) + (offset?.width ?? 0)}px`,
            height: `${(maxY - minY) + (offset?.height ?? 0)}px`,
        };
    }, [...elementIds, ...additionalDeps, windowSize, elementsAvailable]);
}

export const useShowTutorialStep = (
    stepToShow: number,
    category: string,
    defaultAutostart = true,
): boolean => {
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const [step, setStep] = useState<number | null>(null);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        if (!currentUserId) {
            return;
        }

        const fetchPreference = async () => {
            try {
                const preferences = await Client4.getMyPreferences() as unknown as PreferenceType[];
                const pref = preferences.find(
                    (p: PreferenceType) => p.category === PREFERENCE_CATEGORY && p.name === category,
                );
                if (pref) {
                    setStep(parseInt(pref.value, 10));
                } else {
                    setStep(defaultAutostart ? 0 : FINISHED);
                }
            } catch {
                setStep(defaultAutostart ? 0 : FINISHED);
            } finally {
                setLoading(false);
            }
        };

        fetchPreference();
    }, [currentUserId, category, defaultAutostart]);

    if (loading || step === null) {
        return false;
    }

    return step === stepToShow;
};

export const useElementAvailable = (elementIds: string[]): boolean => {
    const intervalRef = useRef<NodeJS.Timeout | null>(null);
    const [available, setAvailable] = useState(false);

    useEffect(() => {
        if (available) {
            if (intervalRef.current) {
                clearInterval(intervalRef.current);
            }
            return () => { /* empty */ };
        }

        intervalRef.current = setInterval(() => {
            if (elementIds.every((id) => document.getElementById(id))) {
                setAvailable(true);
                if (intervalRef.current) {
                    clearInterval(intervalRef.current);
                }
            }
        }, 500);

        return () => {
            if (intervalRef.current) {
                clearInterval(intervalRef.current);
            }
        };
    }, [elementIds, available]);

    return available;
};

export const useTourManager = (category: string, onFinish?: () => void) => {
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const [show, setShow] = useState(false);

    const handleOpen = useCallback((e: React.MouseEvent) => {
        e.stopPropagation();
        e.preventDefault();
        setShow(true);
    }, []);

    const handleDismiss = useCallback(async () => {
        setShow(false);

        if (currentUserId) {
            try {
                await Client4.savePreferences(currentUserId, [{
                    user_id: currentUserId,
                    category: PREFERENCE_CATEGORY,
                    name: category,
                    value: String(FINISHED),
                }]);
            } catch { /* empty */ }
        }

        onFinish?.();
    }, [currentUserId, category, onFinish]);

    const handleNext = useCallback(async (nextStep: number) => {
        const steps = TTCategoriesMapToSteps[category];
        if (!steps || nextStep >= Object.keys(steps).length - 1) {
            handleDismiss();
            return;
        }

        if (currentUserId) {
            try {
                await Client4.savePreferences(currentUserId, [{
                    user_id: currentUserId,
                    category: PREFERENCE_CATEGORY,
                    name: category,
                    value: String(nextStep),
                }]);
            } catch { /* empty */ }
        }
    }, [currentUserId, category, handleDismiss]);

    return {show, setShow, handleOpen, handleDismiss, handleNext};
};
