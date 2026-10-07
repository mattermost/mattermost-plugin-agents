// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import styled, {css} from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {AlertCircleOutlineIcon, DownloadOutlineIcon, RefreshIcon} from '@mattermost/compass-icons/components';

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
import {fetchArtifactToken} from './artifact_token';
import {downloadArtifact} from './download';

export const MIN_INLINE_HEIGHT = 120;
export const MAX_INLINE_HEIGHT = 600;
export const DEFAULT_INLINE_HEIGHT = 320;
const READY_TIMEOUT_MS = 10000;

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

    // Bumping loadKey remounts the iframe (with a fresh broker), which is the
    // only host-initiated load.
    const [loadKey, setLoadKey] = useState(0);
    const loadCountRef = useRef(0);
    const [status, setStatus] = useState<Status>('loading');
    const [height, setHeight] = useState(DEFAULT_INLINE_HEIGHT);
    const [navigationStopped, setNavigationStopped] = useState(false);
    const [pendingConsent, setPendingConsent] = useState<PendingConsent | null>(null);

    // Re-read the theme whenever preferences change (theme switches live there).
    const preferences = useSelector<GlobalState, unknown>((state) => state.entities.preferences?.myPreferences);
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const currentUser = useSelector<GlobalState, any>((state) => state.entities.users.profiles[state.entities.users.currentUserId]);
    const [token, setToken] = useState<string | null>(null);
    const [tokenFailed, setTokenFailed] = useState(false);

    // The server would refuse to serve the artifact: it exceeds the render
    // size limit, so it is never loaded.
    const [tooLarge, setTooLarge] = useState(false);
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

    const user = useMemo<ArtifactUser | null>(() => (currentUser ? toArtifactUser(currentUser) : null), [currentUser]);

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

    // The frame is only rendered once the viewer's bridge token is known.
    useEffect(() => {
        let cancelled = false;
        setToken(null);
        setTokenFailed(false);
        setTooLarge(false);
        setStatus('loading');
        if (!currentUserId) {
            setTokenFailed(true);
            return undefined; // eslint-disable-line no-undefined
        }
        fetchArtifactToken(fileId, currentUserId).then((info) => {
            if (cancelled) {
                return;
            }
            if (info.tooLarge) {
                setTooLarge(true);
            } else {
                setToken(info.token);
            }
        }, () => {
            if (!cancelled) {
                setTokenFailed(true);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [fileId, currentUserId, loadKey]);

    const broker = useMemo(() => (token ? new ArtifactBroker({
        fileId,
        token,
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
    }) : null),

    // loadKey: every host load gets its own broker, so a disposed one is never reused.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [fileId, token, loadKey]);

    useEffect(() => {
        if (!broker) {
            return undefined; // eslint-disable-line no-undefined
        }
        window.addEventListener('message', broker.handleMessage);
        return () => {
            window.removeEventListener('message', broker.handleMessage);
            broker.dispose();

            // Unanswered prompts are dropped without recording a decision.
            pendingConsentRef.current?.reject();
        };
    }, [broker]);

    useEffect(() => {
        broker?.sendContext();
    }, [broker, context]);

    // Each host load: reset state and arm the ready timeout.
    useEffect(() => {
        if (!broker) {
            return undefined; // eslint-disable-line no-undefined
        }
        broker.reset();
        loadCountRef.current = 0;
        setStatus('loading');
        const timer = window.setTimeout(() => {
            if (!broker.isReady()) {
                setStatus('error');
            }
        }, READY_TIMEOUT_MS);
        return () => window.clearTimeout(timer);
    }, [broker]);

    const reload = useCallback(() => {
        setNavigationStopped(false);
        setHeight(DEFAULT_INLINE_HEIGHT);
        setLoadKey((k) => k + 1);
    }, []);

    useImperativeHandle(ref, () => ({reload}), [reload]);

    const handleLoad = useCallback(() => {
        loadCountRef.current += 1;
        if (loadCountRef.current <= 1) {
            return;
        }

        // The artifact navigated its own frame (reload, meta refresh, link):
        // whatever is in the frame now is not the artifact we served. Stop
        // answering it at once and unload it; remounting would only re-run
        // the same navigation. Unanswered prompts are dropped undecided.
        broker?.dispose();
        pendingConsentRef.current?.reject();
        setPendingConsent(null);
        setNavigationStopped(true);
    }, [broker]);

    const handleError = useCallback(() => setStatus('error'), []);

    const answerConsent = useCallback((allowed: boolean) => {
        pendingConsentRef.current?.resolve(allowed);
        setPendingConsent(null);
    }, []);

    const shownStatus: Status = tokenFailed ? 'error' : status;
    const showStatus = !navigationStopped && !tooLarge;

    const title = intl.formatMessage({defaultMessage: 'Interactive artifact: {fileName}'}, {fileName});

    return (
        <Container
            $displayMode={displayMode}
            $height={height}
            $consentShown={Boolean(pendingConsent)}
            data-testid='html-artifact-frame'
        >
            {!navigationStopped && token && (
                <Frame
                    key={loadKey}
                    ref={iframeRef}
                    src={artifactURL(fileId)}
                    sandbox='allow-scripts'
                    referrerPolicy='no-referrer'
                    title={title}
                    onLoad={handleLoad}
                    onError={handleError}
                    $hidden={shownStatus !== 'ready'}
                />
            )}
            {navigationStopped && (
                <Overlay data-testid='html-artifact-navigation-stopped'>
                    <ErrorBox role='alert'>
                        <Icon
                            size='20'
                            glyph={<AlertCircleOutlineIcon/>}
                        />
                        <FormattedMessage defaultMessage='This artifact tried to navigate away and was stopped.'/>
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
            {showStatus && shownStatus === 'loading' && (
                <Overlay>
                    <Spinner
                        size='24'
                        aria-label={intl.formatMessage({defaultMessage: 'Loading artifact'})}
                    />
                </Overlay>
            )}
            {showStatus && shownStatus === 'error' && (
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
            {tooLarge && (
                <Overlay data-testid='html-artifact-too-large'>
                    <ErrorBox role='alert'>
                        <Icon
                            size='20'
                            glyph={<AlertCircleOutlineIcon/>}
                        />
                        <FormattedMessage defaultMessage='This artifact is too large to preview. Download it instead.'/>
                        <Button
                            emphasis='tertiary'
                            size='x-small'
                            leadingIcon={<Icon glyph={<DownloadOutlineIcon/>}/>}
                            onClick={() => downloadArtifact(fileId)}
                        >
                            <FormattedMessage defaultMessage='Download'/>
                        </Button>
                    </ErrorBox>
                </Overlay>
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

// Tall enough for the consent prompt, which would otherwise be clipped by a
// short inline frame.
export const CONSENT_MIN_HEIGHT = 240;

const Container = styled.div<{$displayMode: DisplayMode; $height: number; $consentShown: boolean}>`
    position: relative;
    width: 100%;
    background: var(--center-channel-bg);
    ${(props) => (props.$displayMode === 'inline' ? css`
        height: ${props.$consentShown ? Math.max(props.$height, CONSENT_MIN_HEIGHT) : props.$height}px;
        transition: height 0.15s ease-out;
    ` : css`
        flex: 1;
        min-height: 0;
        overflow: hidden;
        border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
        border-radius: 4px;
        box-shadow: 0 2px 6px rgba(0, 0, 0, 0.08);
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
