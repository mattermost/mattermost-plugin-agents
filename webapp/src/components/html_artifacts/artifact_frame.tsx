// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import styled, {css} from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {AlertCircleOutlineIcon, CloseIcon, RefreshIcon} from '@mattermost/compass-icons/components';

import {artifactURL} from '@/client';

import {
    ArtifactBroker,
    ArtifactContext,
    ArtifactUser,
    DisplayMode,
    colorSchemeFor,
    readThemeFromCSS,
    toArtifactUser,
} from './artifact_broker';
import ArtifactConsentPrompt from './artifact_consent_prompt';

export const MIN_INLINE_HEIGHT = 120;
export const MAX_INLINE_HEIGHT = 600;
export const DEFAULT_INLINE_HEIGHT = 320;
const READY_TIMEOUT_MS = 10000;

// Automatic resets after the artifact navigates its own frame, per mount (and
// per manual reload). More would let a self-reloading artifact fetch forever.
export const MAX_AUTO_RESETS = 1;

export function clampInlineHeight(height: number): number {
    return Math.min(MAX_INLINE_HEIGHT, Math.max(MIN_INLINE_HEIGHT, Math.ceil(height)));
}

export interface ArtifactFrameHandle {
    reload: () => void;
}

interface Props {
    fileId: string;
    fileName: string;
    displayMode: DisplayMode;
    onEscape?: () => void;

    // True while the frame is covered (the inline card under the fullscreen
    // viewer): it must not show a consent prompt the viewer cannot see.
    obscured?: boolean;
}

type Status = 'loading' | 'ready' | 'error';

interface PendingConsent {
    resolve: (allowed: boolean) => void;
    reject: () => void;
}

const ArtifactFrame = forwardRef<ArtifactFrameHandle, Props>(({fileId, fileName, displayMode, onEscape, obscured = false}, ref) => {
    const intl = useIntl();
    const iframeRef = useRef<HTMLIFrameElement>(null);

    // Bumping loadKey remounts the iframe, which is the only host-initiated load.
    const [loadKey, setLoadKey] = useState(0);
    const loadCountRef = useRef(0);
    const [status, setStatus] = useState<Status>('loading');
    const [height, setHeight] = useState(DEFAULT_INLINE_HEIGHT);
    const [navigationBlocked, setNavigationBlocked] = useState(false);
    const autoResetsRef = useRef(0);
    const [navigationStopped, setNavigationStopped] = useState(false);
    const [pendingConsent, setPendingConsent] = useState<PendingConsent | null>(null);

    // Re-read the theme whenever preferences change (theme switches live there).
    const preferences = useSelector<GlobalState, unknown>((state) => state.entities.preferences?.myPreferences);
    const currentUser = useSelector<GlobalState, any>((state) => state.entities.users.profiles[state.entities.users.currentUserId]);
    const locale = intl.locale;

    const context = useMemo<ArtifactContext>(() => {
        const theme = readThemeFromCSS(window.getComputedStyle(document.documentElement));
        return {
            theme,
            colorScheme: colorSchemeFor(theme.centerChannelBg),
            displayMode,
            locale,
        };

    // preferences is the change signal for the CSS variables read above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [preferences, displayMode, locale]);

    const user = useMemo<ArtifactUser | null>(() => (currentUser ? toArtifactUser(currentUser, locale) : null), [currentUser, locale]);

    const contextRef = useRef(context);
    contextRef.current = context;
    const userRef = useRef(user);
    userRef.current = user;
    const onEscapeRef = useRef(onEscape);
    onEscapeRef.current = onEscape;
    const pendingConsentRef = useRef<PendingConsent | null>(null);
    pendingConsentRef.current = pendingConsent;
    const obscuredRef = useRef(obscured);
    obscuredRef.current = obscured;

    const broker = useMemo(() => new ArtifactBroker({
        fileId,
        getTargetWindow: () => iframeRef.current?.contentWindow,
        getContext: () => contextRef.current,
        getCurrentUser: () => userRef.current,
        requestConsent: () => new Promise<boolean>((resolve, reject) => {
            setPendingConsent({resolve, reject});
        }),
        dismissConsent: () => setPendingConsent(null),
        canPrompt: () => !obscuredRef.current,
        onReady: () => setStatus('ready'),
        onResize: (h) => setHeight(clampInlineHeight(h)),
        onEscape: () => onEscapeRef.current?.(),
    }), [fileId]);

    useEffect(() => {
        window.addEventListener('message', broker.handleMessage);
        return () => {
            window.removeEventListener('message', broker.handleMessage);
            broker.dispose();

            // Unanswered prompts are dropped without recording a decision.
            pendingConsentRef.current?.reject();
        };
    }, [broker]);

    useEffect(() => {
        broker.sendContext();
    }, [broker, context]);

    // Each host load: reset state and arm the ready timeout.
    useEffect(() => {
        broker.reset();
        loadCountRef.current = 0;
        setStatus('loading');
        const timer = window.setTimeout(() => {
            if (!broker.isReady()) {
                setStatus('error');
            }
        }, READY_TIMEOUT_MS);
        return () => window.clearTimeout(timer);
    }, [broker, loadKey]);

    const remount = useCallback(() => {
        setHeight(DEFAULT_INLINE_HEIGHT);
        setLoadKey((k) => k + 1);
    }, []);

    // A viewer-initiated reload also re-arms the navigation guard.
    const reload = useCallback(() => {
        autoResetsRef.current = 0;
        setNavigationStopped(false);
        setNavigationBlocked(false);
        remount();
    }, [remount]);

    useImperativeHandle(ref, () => ({reload}), [reload]);

    const handleLoad = useCallback(() => {
        loadCountRef.current += 1;
        if (loadCountRef.current <= 1) {
            return;
        }

        // The artifact navigated its own frame (reload, meta refresh, link).
        if (autoResetsRef.current < MAX_AUTO_RESETS) {
            autoResetsRef.current += 1;
            setNavigationBlocked(true);
            remount();
            return;
        }

        // It keeps doing so: unload it rather than fetching in a loop.
        setNavigationBlocked(false);
        setNavigationStopped(true);
        pendingConsentRef.current?.reject();
        setPendingConsent(null);
    }, [remount]);

    const handleError = useCallback(() => setStatus('error'), []);

    const answerConsent = useCallback((allowed: boolean) => {
        pendingConsentRef.current?.resolve(allowed);
        setPendingConsent(null);
    }, []);

    const title = intl.formatMessage({defaultMessage: 'Interactive artifact: {fileName}'}, {fileName});

    return (
        <Container
            $displayMode={displayMode}
            $height={height}
            data-testid='html-artifact-frame'
        >
            {!navigationStopped && (
                <Frame
                    key={loadKey}
                    ref={iframeRef}
                    src={artifactURL(fileId)}
                    sandbox='allow-scripts'
                    referrerPolicy='no-referrer'
                    title={title}
                    onLoad={handleLoad}
                    onError={handleError}
                    $hidden={status !== 'ready'}
                />
            )}
            {navigationStopped && (
                <Overlay data-testid='html-artifact-navigation-stopped'>
                    <ErrorBox role='alert'>
                        <Icon
                            size='20'
                            glyph={<AlertCircleOutlineIcon/>}
                        />
                        <FormattedMessage defaultMessage='This artifact kept trying to navigate away and was stopped.'/>
                        <Button
                            emphasis='tertiary'
                            size='x-small'
                            leadingIcon={<Icon glyph={<RefreshIcon/>}/>}
                            onClick={reload}
                        >
                            <FormattedMessage defaultMessage='Reload'/>
                        </Button>
                    </ErrorBox>
                </Overlay>
            )}
            {!navigationStopped && status === 'loading' && (
                <Overlay>
                    <Spinner
                        size='24'
                        aria-label={intl.formatMessage({defaultMessage: 'Loading artifact'})}
                    />
                </Overlay>
            )}
            {!navigationStopped && status === 'error' && (
                <Overlay>
                    <ErrorBox role='alert'>
                        <Icon
                            size='20'
                            glyph={<AlertCircleOutlineIcon/>}
                        />
                        <FormattedMessage defaultMessage='This artifact could not be loaded.'/>
                        <Button
                            emphasis='tertiary'
                            size='x-small'
                            leadingIcon={<Icon glyph={<RefreshIcon/>}/>}
                            onClick={reload}
                        >
                            <FormattedMessage defaultMessage='Retry'/>
                        </Button>
                    </ErrorBox>
                </Overlay>
            )}
            {navigationBlocked && (
                <Notice role='status'>
                    <NoticeText>
                        <FormattedMessage defaultMessage='This artifact tried to navigate away and was stopped.'/>
                    </NoticeText>
                    <IconButton
                        size='x-small'
                        icon={<Icon glyph={<CloseIcon/>}/>}
                        aria-label={intl.formatMessage({defaultMessage: 'Dismiss'})}
                        title={intl.formatMessage({defaultMessage: 'Dismiss'})}
                        onClick={() => setNavigationBlocked(false)}
                    />
                </Notice>
            )}
            {pendingConsent && (
                <ArtifactConsentPrompt
                    fileName={fileName}
                    onAllow={() => answerConsent(true)}
                    onDeny={() => answerConsent(false)}
                />
            )}
        </Container>
    );
});

ArtifactFrame.displayName = 'ArtifactFrame';

export default ArtifactFrame;

const Container = styled.div<{$displayMode: DisplayMode; $height: number}>`
    position: relative;
    width: 100%;
    background: var(--center-channel-bg);
    ${(props) => (props.$displayMode === 'inline' ? css`
        height: ${props.$height}px;
        transition: height 0.15s ease-out;
    ` : css`
        flex: 1;
        min-height: 0;
    `)}
`;

const Frame = styled.iframe<{$hidden: boolean}>`
    display: block;
    width: 100%;
    height: 100%;
    border: none;
    background: var(--center-channel-bg);
    visibility: ${(props) => (props.$hidden ? 'hidden' : 'visible')};
`;

const Overlay = styled.div`
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--center-channel-bg);
`;

const ErrorBox = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    text-align: center;

    > :first-child {
        color: var(--error-text);
    }
`;

const Notice = styled.div`
    position: absolute;
    top: 8px;
    left: 8px;
    right: 8px;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px 6px 12px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    box-shadow: 0 4px 6px rgba(0, 0, 0, 0.12);
    color: var(--center-channel-color);
    font-size: 12px;
`;

const NoticeText = styled.span`
    flex: 1;
`;
