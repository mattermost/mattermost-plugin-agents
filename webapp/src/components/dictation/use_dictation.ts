// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';

import {DraftWriter} from '@/dictation/draft_insertion';
import {DictationErrorReason, DictationTransport, TranscriptUpdate} from '@/dictation/types';

export const MAX_SESSION_MS = 10 * 60 * 1000;

export type DictationStatus = 'idle' | 'requesting_permission' | 'connecting' | 'listening' | 'stopping' | 'error';

// The subset of the host's draft that dictation reads.
export type EditorDraft = {
    message: string;
    channelId: string;
    rootId?: string;
};

export type SelectedText = {start?: number; end?: number};

type Options = {
    draft: EditorDraft;
    getSelectedText?: () => SelectedText;
    updateText: (message: string) => void;
    createTransport: (channelId: string) => DictationTransport;
    maxSessionMs?: number;
};

// A session moves only forward through these phases. Every way dictation
// ends goes through stopSession, which is the only move to "stopping".
type Phase = 'starting' | 'listening' | 'stopping' | 'done';

type Session = {
    phase: Phase;
    transport: DictationTransport;
    writer: DraftWriter;
    channelId: string;
    rootId: string;
    latest: TranscriptUpdate;
    pendingText: string | null;
    frame: number | null;
    capTimer: ReturnType<typeof setTimeout> | null;

    // Whether the finalized text replaces the draft once the transport has
    // stopped. A user edit, an editor switch or an unmount revokes it, even
    // while stopping.
    keepFinal: boolean;
    error: DictationErrorReason | null;
};

type StopOptions = {
    keepFinal: boolean;
    error?: DictationErrorReason;
};

const isSameEditor = (session: Session, draft: EditorDraft) =>
    draft.channelId === session.channelId && (draft.rootId ?? '') === session.rootId;

const isRecording = (session: Session) => session.phase === 'starting' || session.phase === 'listening';

// scheduleWrite coalesces transcript updates into one draft write per frame.
// canWrite is checked when the frame runs: a frame can land after the editor
// switched or the user edited the draft, but before that stops the session.
const scheduleWrite = (session: Session, text: string, canWrite: () => boolean, write: (message: string) => void) => {
    session.pendingText = text;
    if (session.frame !== null) {
        return;
    }
    session.frame = requestAnimationFrame(() => {
        session.frame = null;
        if (!canWrite() || session.pendingText === null) {
            return;
        }
        const message = session.writer.render(session.pendingText);
        session.pendingText = null;
        write(message);
    });
};

export function useDictation({draft, getSelectedText, updateText, createTransport, maxSessionMs = MAX_SESSION_MS}: Options) {
    const [status, setStatus] = useState<DictationStatus>('idle');
    const [errorReason, setErrorReason] = useState<DictationErrorReason | null>(null);

    // The session from start until it is done, stopping included, so a new
    // one can't start while the previous transport tears down.
    const sessionRef = useRef<Session | null>(null);

    // The live microphone level while listening, read by the level indicator
    // once per frame rather than through state, so levels don't re-render.
    const levelRef = useRef(0);

    // The host's updateText spreads the draft it captured at render time, so
    // every write must go through the latest one.
    const updateTextRef = useRef(updateText);
    const draftRef = useRef(draft);
    const getSelectedTextRef = useRef(getSelectedText);
    useLayoutEffect(() => {
        updateTextRef.current = updateText;
        draftRef.current = draft;
        getSelectedTextRef.current = getSelectedText;
    });

    const stopSession = useCallback(async (session: Session, {keepFinal, error}: StopOptions) => {
        if (!isRecording(session)) {
            if (!keepFinal) {
                session.keepFinal = false;
            }
            return;
        }
        session.phase = 'stopping';
        session.keepFinal = keepFinal;
        session.error = error ?? null;
        if (session.frame !== null) {
            cancelAnimationFrame(session.frame);
            session.frame = null;
        }
        if (session.capTimer !== null) {
            clearTimeout(session.capTimer);
        }
        setStatus('stopping');
        levelRef.current = 0;

        // Stop delivers the last segment's final text through onTranscript.
        // A segment that didn't finalize in time keeps its streamed text.
        await session.transport.stop({waitForFinal: keepFinal});

        const current = draftRef.current;
        if (session.keepFinal && isSameEditor(session, current) && session.writer.isOwnWrite(current.message ?? '')) {
            updateTextRef.current(session.writer.render(session.latest.text));
        }
        session.phase = 'done';
        if (sessionRef.current === session) {
            sessionRef.current = null;
            setStatus(session.error ? 'error' : 'idle');
            setErrorReason(session.error);
        }
    }, []);

    const start = useCallback(async () => {
        const current = draftRef.current;
        const caret = getSelectedTextRef.current?.()?.start;
        const transport = createTransport(current.channelId);
        const session: Session = {
            phase: 'starting',
            transport,
            writer: new DraftWriter(current.message ?? '', caret),
            channelId: current.channelId,
            rootId: current.rootId ?? '',
            latest: {text: ''},
            pendingText: null,
            frame: null,
            capTimer: null,
            keepFinal: true,
            error: null,
        };
        sessionRef.current = session;
        setErrorReason(null);
        setStatus('requesting_permission');

        transport.onTranscript((update: TranscriptUpdate) => {
            session.latest = update;
            if (isRecording(session)) {
                scheduleWrite(
                    session,
                    update.text,
                    () => isRecording(session) && isSameEditor(session, draftRef.current) && session.writer.isOwnWrite(draftRef.current.message ?? ''),
                    (message) => updateTextRef.current(message),
                );
            }
        });
        transport.onConnecting(() => {
            if (session.phase === 'starting') {
                setStatus('connecting');
            }
        });
        transport.onError((err) => stopSession(session, {keepFinal: true, error: err.reason}));
        transport.onIdle(() => stopSession(session, {keepFinal: true}));
        transport.onLevel((level) => {
            if (session.phase === 'listening') {
                levelRef.current = level;
            }
        });

        try {
            await transport.start();
        } catch (err) {
            if (session.phase !== 'starting') {
                return;
            }
            session.phase = 'done';
            sessionRef.current = null;
            setStatus('error');
            setErrorReason((err as {reason?: DictationErrorReason})?.reason ?? 'provider_unreachable');
            return;
        }
        if (session.phase !== 'starting') {
            return;
        }
        session.phase = 'listening';
        session.capTimer = setTimeout(() => stopSession(session, {keepFinal: true}), maxSessionMs);
        setStatus('listening');
    }, [createTransport, stopSession, maxSessionMs]);

    const stop = useCallback(() => {
        const session = sessionRef.current;
        if (session) {
            stopSession(session, {keepFinal: true});
        }
    }, [stopSession]);

    const toggle = useCallback(() => {
        const session = sessionRef.current;
        if (!session) {
            start();
        } else if (isRecording(session)) {
            stop();
        }
    }, [start, stop]);

    // A draft change dictation didn't write is the user typing, sending (the
    // draft is cleared) or switching to another channel or thread: stop, or
    // keep stopping, without writing again.
    useEffect(() => {
        const session = sessionRef.current;
        if (!session || (isSameEditor(session, draft) && session.writer.isOwnWrite(draft.message ?? ''))) {
            return;
        }
        stopSession(session, {keepFinal: false});
    }, [draft, stopSession]);

    const active = status === 'requesting_permission' || status === 'connecting' || status === 'listening';
    useEffect(() => {
        if (!active) {
            return undefined; // eslint-disable-line no-undefined
        }

        // Captured on window so the editor never sees this Escape: while
        // editing a post, Escape would also cancel the edit.
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault();
                e.stopPropagation();
                stop();
            }
        };
        window.addEventListener('keydown', onKeyDown, true);
        return () => window.removeEventListener('keydown', onKeyDown, true);
    }, [active, stop]);

    useEffect(() => () => {
        const session = sessionRef.current;
        if (session) {
            stopSession(session, {keepFinal: false});
        }
    }, [stopSession]);

    return {status, errorReason, toggle, active, levelRef};
}
