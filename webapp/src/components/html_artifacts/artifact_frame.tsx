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

import {artifactPath, artifactURL, getArtifactToken} from '@/client';

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
import ArtifactInteractOverlay from './artifact_interact_overlay';
import {newArtifactNonce} from './artifact_token';
import {downloadArtifact} from './download';

export const MIN_INLINE_HEIGHT = 120;
export const MAX_INLINE_HEIGHT = 600;
export const DEFAULT_INLINE_HEIGHT = 320;
const READY_TIMEOUT_MS = 10000;

// The bridge sends a heartbeat every 500 ms. A busy artifact can delay its
// own heartbeat for a moment, so a short lapse only hides the frame; a long
// one means the document in the frame is no longer the bridge's.
export const HEARTBEAT_HIDE_MS = 2000;
export const HEARTBEAT_STOP_MS = 8000;
const HEARTBEAT_CHECK_MS = 250;
const FOCUS_CHECK_MS = 250;

export function clampInlineHeight(height: number): number {
    return Math.min(MAX_INLINE_HEIGHT, Math.max(MIN_INLINE_HEIGHT, Math.ceil(height)));
}

export interface ArtifactFrameHandle {

    // startedByClick: see Props; pass the click's isTrusted.
    reload: (startedByClick: boolean) => void;
}

// One host load of the frame. The server serves the artifact and accepts the
// bridge token only for this nonce.
interface FrameLoad {
    fileId: string;
    nonce: string;
    token: string;
}

function escapeAttribute(value: string): string {
    return value.
        replace(/&/g, '&amp;').
        replace(/"/g, '&quot;').
        replace(/'/g, '&#39;').
        replace(/</g, '&lt;').
        replace(/>/g, '&gt;');
}

// The artifact's sandbox cannot keep it from navigating its own frame, which
// could carry data away in the URL. So the host frame holds a script-free
// wrapper document whose policy lets its child frame load nothing but the
// artifact: any other navigation is blocked before a request goes out. Only
// host-controlled values may appear in this markup.
function wrapperMarkup(fileId: string): string {
    const url = new URL(artifactPath(fileId), window.location.href);
    const csp = `default-src 'none'; style-src 'unsafe-inline'; frame-src ${url.origin}${url.pathname}`;
    return '<!doctype html>' +
        `<meta http-equiv="Content-Security-Policy" content="${escapeAttribute(csp)}">` +
        '<style>html,body,iframe{display:block;width:100%;height:100%;margin:0;padding:0;border:0}</style>';
}

// Like the wrapper, the artifact's own frame is no tab stop until the viewer
// engages it.
function syncInnerFrame(inner: HTMLIFrameElement, title: string, engaged: boolean) {
    inner.setAttribute('title', title);
    if (engaged) {
        inner.removeAttribute('tabindex');
    } else {
        inner.setAttribute('tabindex', '-1');
    }
}

interface Props {
    fileId: string;
    fileName: string;
    displayMode: DisplayMode;
    onEscape?: () => void;

    // True while the frame is covered (the inline card under the fullscreen
    // viewer): it must not show a consent prompt the viewer cannot see.
    obscured?: boolean;

    // The viewer's trusted click (Run, Open fullscreen) started this frame:
    // that click is their first interaction with the artifact, so the load
    // starts engaged and the artifact gets keyboard focus once it is ready,
    // unless the viewer moved on in the meantime. Read on mount only.
    startedByClick?: boolean;
}

type Status = 'loading' | 'ready' | 'error';

type StopReason = 'navigation' | 'focus' | 'unresponsive';

interface PendingConsent {
    resolve: (allowed: boolean) => void;
    reject: () => void;
}

const ArtifactFrame = forwardRef<ArtifactFrameHandle, Props>(({
    fileId,
    fileName,
    displayMode,
    onEscape,
    obscured = false,
    startedByClick = false,
}, ref) => {
    const intl = useIntl();
    const containerRef = useRef<HTMLDivElement>(null);

    // The host's own frame holds the wrapper document; innerRef is the
    // sandboxed frame inside it that runs the artifact. Focus anywhere in
    // either shows up on this page as the wrapper being the active element.
    const iframeRef = useRef<HTMLIFrameElement | null>(null);
    const innerRef = useRef<HTMLIFrameElement | null>(null);

    // Bumping loadKey starts a new host load: a new nonce and token, a fresh
    // broker and a remounted frame. It is the only host-initiated load.
    const [loadKey, setLoadKey] = useState(0);
    const loadCountRef = useRef(0);
    const wrapperLoadCountRef = useRef(0);
    const [status, setStatus] = useState<Status>('loading');
    const [height, setHeight] = useState(DEFAULT_INLINE_HEIGHT);
    const [stopReason, setStopReason] = useState<StopReason | null>(null);
    const [unresponsive, setUnresponsive] = useState(false);
    const [loadFailed, setLoadFailed] = useState(false);
    const [pendingConsent, setPendingConsent] = useState<PendingConsent | null>(null);

    // Engaged: the viewer chose to interact with the artifact, so it may hold
    // keyboard focus. Until then a host overlay takes every click, the iframe
    // is not a tab stop, and an artifact that focuses itself is stopped.
    const startsEngaged = startedByClick && !obscured;
    const [engaged, setEngagedState] = useState(startsEngaged);
    const engagedRef = useRef(startsEngaged);
    const setEngaged = useCallback((value: boolean) => {
        engagedRef.current = value;
        setEngagedState(value);
    }, []);

    // This load was started by the viewer's click: hand the artifact focus
    // when it first shows, if the viewer is still engaged with it by then.
    const focusWhenShownRef = useRef(startsEngaged);

    // Where focus goes back to when it has to leave the artifact.
    const lastHostFocusRef = useRef<HTMLElement | null>(null);
    const lastHeartbeatRef = useRef<number | null>(null);
    const unresponsiveRef = useRef(false);

    // The prompt returns focus to where it was when it opened: true when that
    // was this artifact, engaged.
    const consentReturnsToFrameRef = useRef(false);

    // Re-read the theme whenever preferences change (theme switches live there).
    const preferences = useSelector<GlobalState, unknown>((state) => state.entities.preferences?.myPreferences);
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const currentUser = useSelector<GlobalState, any>((state) => state.entities.users.profiles[state.entities.users.currentUserId]);
    const [load, setLoad] = useState<FrameLoad | null>(null);
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
    const stopReasonRef = useRef(stopReason);
    stopReasonRef.current = stopReason;
    const title = intl.formatMessage({defaultMessage: 'Interactive artifact: {fileName}'}, {fileName});
    const titleRef = useRef(title);
    titleRef.current = title;

    // The frame is only rendered once this load's bridge token is known.
    useEffect(() => {
        let cancelled = false;
        setLoad(null);
        setTokenFailed(false);
        setTooLarge(false);
        setStatus('loading');
        if (!currentUserId) {
            setTokenFailed(true);
            return undefined; // eslint-disable-line no-undefined
        }
        const nonce = newArtifactNonce();
        getArtifactToken(fileId, nonce).then((info) => {
            if (cancelled) {
                return;
            }
            if (info.tooLarge) {
                setTooLarge(true);
            } else {
                setLoad({fileId, nonce, token: info.token});
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

    // Every host load has its own FrameLoad, so a disposed broker is never reused.
    const broker = useMemo(() => (load ? new ArtifactBroker({
        fileId: load.fileId,
        viewerId: currentUserId,
        token: load.token,
        getTargetWindow: () => innerRef.current?.contentWindow,
        getContext: () => contextRef.current,
        getCurrentUser: () => userRef.current,
        requestConsent: () => new Promise<boolean>((resolve, reject) => {
            consentReturnsToFrameRef.current = engagedRef.current && Boolean(iframeRef.current) && document.activeElement === iframeRef.current;
            setPendingConsent({resolve, reject});
        }),
        dismissConsent: () => setPendingConsent(null),
        canPrompt: () => !obscuredRef.current && engagedRef.current,
        onReady: () => setStatus('ready'),
        onHeartbeat: () => {
            lastHeartbeatRef.current = performance.now();
            if (unresponsiveRef.current) {
                unresponsiveRef.current = false;
                setUnresponsive(false);
            }
        },
        onResize: (h) => setHeight(clampInlineHeight(h)),
        onEscape: () => onEscapeRef.current?.(),
    }) : null), [load, currentUserId]);
    const brokerRef = useRef(broker);
    brokerRef.current = broker;

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

    // A frame that never became ready may hold a foreign document with its
    // own network access: unload it rather than leave it hidden.
    const failLoad = useCallback(() => {
        brokerRef.current?.dispose();
        pendingConsentRef.current?.reject();
        setPendingConsent(null);
        setLoadFailed(true);
        setStatus('error');
    }, []);

    // Each host load: reset state and arm the ready timeout.
    useEffect(() => {
        if (!broker) {
            return undefined; // eslint-disable-line no-undefined
        }
        broker.reset();
        setStatus('loading');
        const timer = window.setTimeout(() => {
            if (!broker.isReady()) {
                failLoad();
            }
        }, READY_TIMEOUT_MS);
        return () => window.clearTimeout(timer);
    }, [broker, failLoad]);

    // Whatever is in the frame now is not the artifact we served, or the
    // artifact is acting against the viewer. Stop answering it at once and
    // unload it; remounting would only run it again. Unanswered prompts are
    // dropped undecided.
    const stopFrame = useCallback((reason: StopReason) => {
        brokerRef.current?.dispose();
        pendingConsentRef.current?.reject();
        setPendingConsent(null);
        setStopReason(reason);
    }, []);

    // Takes keyboard focus out of the iframe, back to where the viewer last
    // was on the page. Never onto the interact overlay: a viewer typing there
    // would activate it with Space or Enter.
    const releaseFocus = useCallback(() => {
        const iframe = iframeRef.current;
        if (!iframe || document.activeElement !== iframe) {
            return;
        }
        const last = lastHostFocusRef.current;
        if (last?.isConnected) {
            last.focus({preventScroll: true});
        }
        if (document.activeElement === iframe) {
            iframe.blur();
        }
    }, []);

    // Heartbeat watchdog. A navigation to a response that never finishes
    // fires no second load event, but it does stop the bridge's heartbeat.
    useEffect(() => {
        if (!broker || stopReason) {
            return undefined; // eslint-disable-line no-undefined
        }
        lastHeartbeatRef.current = null;
        unresponsiveRef.current = false;
        setUnresponsive(false);

        // Hidden tabs throttle timers in the frame and here alike, so a
        // lapse means nothing until the page is visible again, and the
        // viewer coming back gets a fresh grace period. A check that itself
        // ran very late (the page was stalled) only defers the unload: a
        // document that stalls the page on purpose must not stay visible.
        let paused = document.visibilityState === 'hidden';
        let lastCheck = performance.now();
        let stopBase = 0;
        const grace = () => {
            if (lastHeartbeatRef.current !== null) {
                lastHeartbeatRef.current = performance.now();
            }
        };
        const onVisibilityChange = () => {
            if (document.visibilityState === 'hidden') {
                paused = true;
            } else if (paused) {
                paused = false;
                grace();
            }
        };
        const check = () => {
            const now = performance.now();
            const late = now - lastCheck > HEARTBEAT_HIDE_MS;
            lastCheck = now;
            onVisibilityChange();
            if (paused) {
                return;
            }
            if (late) {
                stopBase = now;
            }

            // Until ready the frame is hidden anyway, and the ready timeout
            // handles a document that replaced the artifact before that.
            const last = lastHeartbeatRef.current;
            if (last === null || !broker.isReady()) {
                return;
            }
            const lapse = now - last;
            if (now - Math.max(last, stopBase) > HEARTBEAT_STOP_MS) {
                releaseFocus();
                stopFrame('unresponsive');
            } else if (lapse > HEARTBEAT_HIDE_MS && !unresponsiveRef.current) {
                unresponsiveRef.current = true;
                setUnresponsive(true);

                // The document now in the frame may not be the one that
                // asked: drop its prompt undecided.
                pendingConsentRef.current?.reject();
                setPendingConsent(null);

                // Keystrokes must not go to a page the viewer can no longer see.
                releaseFocus();
                setEngaged(false);
            }
        };
        const timer = window.setInterval(check, HEARTBEAT_CHECK_MS);
        document.addEventListener('visibilitychange', onVisibilityChange);
        return () => {
            window.clearInterval(timer);
            document.removeEventListener('visibilitychange', onVisibilityChange);
        };
    }, [broker, stopReason, stopFrame, releaseFocus, setEngaged]);

    // Focus guard. Sandboxed frames can still call focus() on themselves and
    // take keystrokes meant for the page (e.g. the message box), so an
    // artifact holding focus the viewer did not give it is stopped.
    useEffect(() => {
        const isInside = (node: unknown): boolean => node instanceof Node && Boolean(containerRef.current?.contains(node));

        if (document.activeElement instanceof HTMLElement && document.activeElement !== document.body && !isInside(document.activeElement)) {
            lastHostFocusRef.current = document.activeElement;
        }

        // Focus leaving for the page (outside this frame and its own
        // controls) ends the engagement.
        const onFocusIn = (e: FocusEvent) => {
            if (isInside(e.target)) {
                return;
            }
            if (e.target instanceof HTMLElement) {
                lastHostFocusRef.current = e.target;
            }
            if (engagedRef.current) {
                setEngaged(false);
            }
        };

        // Clicking a part of the page that takes no focus focuses the page
        // itself, without a focusin.
        const onWindowFocus = () => {
            if (engagedRef.current && !isInside(document.activeElement)) {
                setEngaged(false);
            }
        };

        // Clicking page content that takes no focus leaves focus on the
        // artifact. Release it before disengaging, or the guard below would
        // take the artifact for one holding focus unasked.
        const onPointerDown = (e: PointerEvent) => {
            if (!engagedRef.current || isInside(e.target)) {
                return;
            }
            releaseFocus();
            setEngaged(false);
        };

        const check = () => {
            const iframe = iframeRef.current;
            if (!iframe || engagedRef.current || stopReasonRef.current || document.activeElement !== iframe) {
                return;
            }
            releaseFocus();
            stopFrame('focus');
        };

        // Focus moving into the frame blurs the page; the interval covers a
        // page that was not focused to begin with.
        const blurTimers = new Set<number>();
        const onWindowBlur = () => {
            const id = window.setTimeout(() => {
                blurTimers.delete(id);
                check();
            }, 0);
            blurTimers.add(id);
        };
        const timer = window.setInterval(check, FOCUS_CHECK_MS);

        document.addEventListener('focusin', onFocusIn, true);
        document.addEventListener('pointerdown', onPointerDown, true);
        document.addEventListener('visibilitychange', check);
        window.addEventListener('focus', onWindowFocus);
        window.addEventListener('blur', onWindowBlur);
        return () => {
            document.removeEventListener('focusin', onFocusIn, true);
            document.removeEventListener('pointerdown', onPointerDown, true);
            document.removeEventListener('visibilitychange', check);
            window.removeEventListener('focus', onWindowFocus);
            window.removeEventListener('blur', onWindowBlur);
            window.clearInterval(timer);
            blurTimers.forEach((id) => window.clearTimeout(id));
        };
    }, [releaseFocus, stopFrame, setEngaged]);

    // While a load the viewer started gets ready, focus waits on the frame's
    // area: the control they clicked may be gone, and focus moving to the
    // page would end the engagement.
    const holdFocus = useCallback(() => {
        containerRef.current?.focus({preventScroll: true});
    }, []);

    useEffect(() => {
        if (focusWhenShownRef.current) {
            holdFocus();
        }

    // Mount only: a load started later holds focus from reload().
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    const reload = useCallback((byClick: boolean) => {
        const engage = byClick && !obscuredRef.current;
        setStopReason(null);
        setLoadFailed(false);
        setUnresponsive(false);
        setEngaged(engage);
        focusWhenShownRef.current = engage;
        if (engage) {
            holdFocus();
        }
        setHeight(DEFAULT_INLINE_HEIGHT);
        setLoad(null);
        setLoadKey((k) => k + 1);
    }, [setEngaged, holdFocus]);

    const reloadOnClick = useCallback((e: React.MouseEvent) => reload(e.nativeEvent.isTrusted), [reload]);

    useImperativeHandle(ref, () => ({reload}), [reload]);

    // A new wrapper element is a new host load: count its loads from zero.
    const setWrapper = useCallback((el: HTMLIFrameElement | null) => {
        iframeRef.current = el;
        innerRef.current = null;
        wrapperLoadCountRef.current = 0;
        loadCountRef.current = 0;
    }, []);

    // The artifact navigated its own frame (reload, meta refresh, link):
    // whatever is in the frame now is not the artifact we served. The wrapper
    // blocks navigations away from the artifact URL, and in some browsers a
    // blocked one still loads an error page into the frame.
    const handleLoad = useCallback(() => {
        loadCountRef.current += 1;
        if (loadCountRef.current > 1) {
            stopFrame('navigation');
        }
    }, [stopFrame]);

    const handleError = failLoad;

    // Firefox reports a navigation the wrapper blocked as an error on the
    // artifact's frame, where Chromium loads an error page into it. After the
    // artifact has loaded, either one is the artifact navigating itself.
    const handleInnerError = useCallback(() => {
        if (loadCountRef.current > 0) {
            stopFrame('navigation');
        } else {
            failLoad();
        }
    }, [stopFrame, failLoad]);

    // Focus can rest on the wrapper document instead of the artifact, where
    // neither this page's key handlers nor the bridge see the keystroke.
    const handleWrapperKeyDown = useCallback((e: KeyboardEvent) => {
        if (e.key === 'Escape' && onEscapeRef.current) {
            e.preventDefault();
            onEscapeRef.current();
        }
    }, []);

    // The artifact's frame is created from here once the wrapper document
    // exists, with its load listener attached before it starts loading, so
    // the host load is always the first one counted.
    const handleWrapperLoad = useCallback(() => {
        wrapperLoadCountRef.current += 1;
        if (wrapperLoadCountRef.current > 1) {
            stopFrame('navigation');
            return;
        }
        const doc = iframeRef.current?.contentDocument;
        if (!load || !doc?.body) {
            failLoad();
            return;
        }
        const inner = doc.createElement('iframe');
        inner.setAttribute('sandbox', 'allow-scripts');
        inner.setAttribute('referrerpolicy', 'no-referrer');
        inner.setAttribute('data-testid', 'html-artifact-content');
        syncInnerFrame(inner, titleRef.current, engagedRef.current);
        inner.addEventListener('load', handleLoad);
        inner.addEventListener('error', handleInnerError);
        inner.setAttribute('src', artifactURL(load.fileId, load.nonce));
        doc.addEventListener('keydown', handleWrapperKeyDown);
        innerRef.current = inner;
        doc.body.appendChild(inner);
    }, [load, stopFrame, failLoad, handleLoad, handleInnerError, handleWrapperKeyDown]);

    // Focusing the wrapper alone leaves keystrokes in the wrapper document,
    // and focus() on an inner frame that is still its document's focused
    // element does nothing, so it is blurred first.
    const focusArtifact = useCallback(() => {
        iframeRef.current?.focus();
        const inner = innerRef.current;
        if (inner) {
            inner.blur();
            inner.focus();
        }
    }, []);

    const engage = useCallback((e: React.MouseEvent) => {
        if (!e.nativeEvent.isTrusted) {
            return;
        }
        setEngaged(true);
        focusArtifact();
    }, [setEngaged, focusArtifact]);

    const refocusAfterConsentRef = useRef(false);
    const answerConsent = useCallback((allowed: boolean) => {
        // The prompt is about to hand focus back to this artifact, which had
        // it when the prompt opened; the viewer answering it is returning there.
        if (consentReturnsToFrameRef.current && !unresponsiveRef.current) {
            setEngaged(true);
            refocusAfterConsentRef.current = true;
        }
        pendingConsentRef.current?.resolve(allowed);
        setPendingConsent(null);
    }, [setEngaged]);

    // The prompt gives focus back to the wrapper element as it unmounts,
    // which leaves keystrokes in the wrapper document; pass them on to the artifact.
    useEffect(() => {
        if (pendingConsent || !refocusAfterConsentRef.current) {
            return;
        }
        refocusAfterConsentRef.current = false;
        if (document.activeElement === iframeRef.current) {
            focusArtifact();
        }
    }, [pendingConsent, focusArtifact]);

    useEffect(() => {
        if (innerRef.current) {
            syncInnerFrame(innerRef.current, title, engaged);
        }
    }, [title, engaged]);

    const shownStatus: Status = tokenFailed ? 'error' : status;
    const showStatus = !stopReason && !tooLarge;
    const visible = showStatus && Boolean(load) && shownStatus === 'ready' && !unresponsive;

    // A hidden frame cannot take focus, so the hand-off waits for it to show.
    useEffect(() => {
        if (!visible || !focusWhenShownRef.current) {
            return;
        }
        focusWhenShownRef.current = false;
        if (engagedRef.current && !obscuredRef.current) {
            focusArtifact();
        }
    }, [visible, focusArtifact]);

    return (
        <Container
            ref={containerRef}
            tabIndex={-1}
            $displayMode={displayMode}
            $height={height}
            $consentShown={Boolean(pendingConsent)}
            data-testid='html-artifact-frame'
            data-engaged={engaged}
        >
            {!stopReason && !loadFailed && load && (
                <Frame
                    key={load.nonce}
                    ref={setWrapper}
                    srcDoc={wrapperMarkup(load.fileId)}
                    title={title}
                    tabIndex={engaged ? undefined : -1} // eslint-disable-line no-undefined
                    onLoad={handleWrapperLoad}
                    onError={handleError}
                    data-testid='html-artifact-wrapper'
                    $hidden={shownStatus !== 'ready' || unresponsive}
                />
            )}
            {visible && !engaged && (
                <ArtifactInteractOverlay onEngage={engage}/>
            )}
            {stopReason && (
                <Overlay
                    data-testid='html-artifact-navigation-stopped'
                    data-stop-reason={stopReason}
                >
                    <ErrorBox role='alert'>
                        <Icon
                            size='20'
                            glyph={<AlertCircleOutlineIcon/>}
                        />
                        {stopReason === 'navigation' && (
                            <FormattedMessage defaultMessage='This artifact tried to navigate away and was stopped.'/>
                        )}
                        {stopReason === 'focus' && (
                            <FormattedMessage defaultMessage='This artifact tried to take keyboard focus and was stopped.'/>
                        )}
                        {stopReason === 'unresponsive' && (
                            <FormattedMessage defaultMessage='This artifact stopped responding and was unloaded.'/>
                        )}
                        <Button
                            emphasis='tertiary'
                            size='x-small'
                            leadingIcon={<Icon glyph={<RefreshIcon/>}/>}
                            onClick={reloadOnClick}
                        >
                            <FormattedMessage defaultMessage='Reload'/>
                        </Button>
                    </ErrorBox>
                </Overlay>
            )}
            {showStatus && shownStatus === 'ready' && unresponsive && (
                <Overlay data-testid='html-artifact-unresponsive'>
                    <StatusBox role='status'>
                        <Spinner
                            size='24'
                            aria-hidden='true'
                        />
                        <FormattedMessage defaultMessage='This artifact stopped responding.'/>
                    </StatusBox>
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
                            onClick={reloadOnClick}
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

    &:focus {
        outline: none;
    }
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

const StatusBox = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    padding: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    text-align: center;
`;
