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
    createTransport: () => DictationTransport;
};

type Session = {
    transport: DictationTransport;
    writer: DraftWriter;
    channelId: string;
    rootId: string;
    latest: TranscriptUpdate;
    pendingText: string | null;
    frame: number | null;
    capTimer: ReturnType<typeof setTimeout> | null;
};

type FinishOptions = {

    // Rewrite the draft with only finalized text once the transport stops.
    writeFinal: boolean;
    error?: DictationErrorReason;
};

const isSameEditor = (session: Session, draft: EditorDraft) =>
    draft.channelId === session.channelId && (draft.rootId ?? '') === session.rootId;

// scheduleWrite coalesces transcript updates into one draft write per frame.
const scheduleWrite = (
    session: Session,
    text: string,
    isCurrent: () => boolean,
    write: (message: string) => void,
) => {
    session.pendingText = text;
    if (session.frame !== null) {
        return;
    }
    session.frame = requestAnimationFrame(() => {
        session.frame = null;
        if (!isCurrent() || session.pendingText === null) {
            return;
        }
        const message = session.writer.render(session.pendingText);
        session.pendingText = null;
        write(message);
    });
};

export function useDictation({draft, getSelectedText, updateText, createTransport}: Options) {
    const [status, setStatus] = useState<DictationStatus>('idle');
    const [errorReason, setErrorReason] = useState<DictationErrorReason | null>(null);
    const sessionRef = useRef<Session | null>(null);

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

    const finish = useCallback(async (session: Session, {writeFinal, error}: FinishOptions) => {
        if (sessionRef.current !== session) {
            return;
        }
        sessionRef.current = null;
        if (session.frame !== null) {
            cancelAnimationFrame(session.frame);
            session.frame = null;
        }
        if (session.capTimer !== null) {
            clearTimeout(session.capTimer);
        }

        if (writeFinal) {
            setStatus('stopping');
        }
        await session.transport.stop();

        // The draft may have moved on while the last segment was finalized.
        if (writeFinal && isSameEditor(session, draftRef.current) && session.writer.isOwnWrite(draftRef.current.message ?? '')) {
            updateTextRef.current(session.writer.render(session.latest.finalText));
        }
        if (sessionRef.current === null) {
            setStatus(error ? 'error' : 'idle');
            setErrorReason(error ?? null);
        }
    }, []);

    const start = useCallback(async () => {
        const current = draftRef.current;
        const caret = getSelectedTextRef.current?.()?.start;
        const transport = createTransport();
        const session: Session = {
            transport,
            writer: new DraftWriter(current.message ?? '', caret),
            channelId: current.channelId,
            rootId: current.rootId ?? '',
            latest: {text: '', finalText: ''},
            pendingText: null,
            frame: null,
            capTimer: null,
        };
        sessionRef.current = session;
        setErrorReason(null);
        setStatus('requesting_permission');

        const onTranscript = (update: TranscriptUpdate) => {
            if (sessionRef.current !== session) {
                return;
            }
            session.latest = update;
            scheduleWrite(
                session,
                update.text,
                () => sessionRef.current === session,
                (message) => updateTextRef.current(message),
            );
        };
        transport.onPartial(onTranscript);
        transport.onFinal(onTranscript);
        transport.onConnecting(() => {
            if (sessionRef.current === session) {
                setStatus('connecting');
            }
        });
        transport.onError((err) => finish(session, {writeFinal: true, error: err.reason}));
        transport.onIdle(() => finish(session, {writeFinal: true}));

        try {
            await transport.start();
        } catch (err) {
            if (sessionRef.current !== session) {
                return;
            }
            sessionRef.current = null;
            setStatus('error');
            setErrorReason((err as {reason?: DictationErrorReason})?.reason ?? 'provider_unreachable');
            return;
        }
        if (sessionRef.current !== session) {
            return;
        }
        session.capTimer = setTimeout(() => finish(session, {writeFinal: true}), MAX_SESSION_MS);
        setStatus('listening');
    }, [createTransport, finish]);

    const stop = useCallback(() => {
        const session = sessionRef.current;
        if (session) {
            finish(session, {writeFinal: true});
        }
    }, [finish]);

    const toggle = useCallback(() => {
        if (sessionRef.current) {
            stop();
        } else {
            start();
        }
    }, [start, stop]);

    // A draft change dictation didn't write is the user typing, sending (the
    // draft is cleared) or switching to another channel or thread: stop
    // without writing again.
    useEffect(() => {
        const session = sessionRef.current;
        if (!session) {
            return;
        }
        if (!isSameEditor(session, draft) || !session.writer.isOwnWrite(draft.message ?? '')) {
            finish(session, {writeFinal: false});
        }
    }, [draft, finish]);

    const active = status === 'requesting_permission' || status === 'connecting' || status === 'listening';
    useEffect(() => {
        if (!active) {
            return undefined; // eslint-disable-line no-undefined
        }
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                stop();
            }
        };
        document.addEventListener('keydown', onKeyDown);
        return () => document.removeEventListener('keydown', onKeyDown);
    }, [active, stop]);

    useEffect(() => () => {
        const session = sessionRef.current;
        if (session) {
            finish(session, {writeFinal: false});
        }
    }, [finish]);

    return {status, errorReason, toggle, active};
}
