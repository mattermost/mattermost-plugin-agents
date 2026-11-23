import type {AIMockFixture, AIMockFixtureFile} from '../aimock-fixtures';
import {buildTitleFixture} from '../aimock-fixtures';

/**
 * Scripting the mock LLM for notification scenarios.
 *
 * aimock (ghcr.io/copilotkit/aimock) replays fixtures matched on the last user
 * message (substring), tool name or tool-call id. Per fixture it can shape the
 * stream: `chunkSize` characters per SSE chunk, `latency` ms before every chunk,
 * `streamingProfile {ttft, tps, jitter}` for a first-token delay and a rate,
 * `truncateAfterChunks` to cut the stream short and `disconnectAfterMs` to drop
 * the connection mid-stream. Errors are `response.error` + `status`.
 *
 * Tool calls and ask-user flows are plain fixtures too; reuse the builders in
 * `helpers/aimock-fixtures` (`buildToolCallAndTextResponse`, `buildMultiTurnToolSequence`).
 * Whether a call waits for approval is decided by the plugin's tool policy, not by aimock.
 *
 * Every builder returns an `AIMockFixtureFile`; pass it to `lab.script(...)`.
 */

/** Stream-shaping fields aimock supports that the shared `AIMockFixture` type does not list yet. */
export type StreamShape = {
    /** Cut the stream after this many chunks without a finish chunk or [DONE]. */
    truncateAfterChunks?: number;
    /** Drop the connection this many ms after the response starts. */
    disconnectAfterMs?: number;
};

type ScriptedFixture = AIMockFixture & StreamShape;

export type SlowReplyOptions = {
    /** Substring of the user message that selects this reply. Must be unique per scenario. */
    prompt: string;
    /** Full text the agent finally says. */
    content: string;
    /** Characters per SSE chunk. Default 12. */
    chunkSize?: number;
    /** Milliseconds between chunks. Default 400, so a 120 character reply streams for ~4 s. */
    chunkDelayMs?: number;
    /** First-token delay in ms (applied before the first chunk). */
    firstTokenDelayMs?: number;
    /** Title the plugin generates for the conversation. Default 'Notification lab'. */
    title?: string;
};

/** Expected wall-clock duration of the stream for `slowReply`, excluding plugin/post overhead. */
export function slowReplyDurationMs(options: Pick<SlowReplyOptions, 'content' | 'chunkSize' | 'chunkDelayMs' | 'firstTokenDelayMs'>): number {
    const chunkSize = options.chunkSize ?? 12;
    const chunks = Math.ceil(options.content.length / chunkSize);
    return (options.firstTokenDelayMs ?? 0) + (chunks * (options.chunkDelayMs ?? 400));
}

function withTitle(title: string | undefined, fixture: ScriptedFixture): AIMockFixtureFile {
    return {fixtures: [buildTitleFixture(title ?? 'Notification lab'), fixture]};
}

/** A reply that streams slowly enough for a test to navigate away, blur the window or reply in the thread mid-stream. */
export function slowReply(options: SlowReplyOptions): AIMockFixtureFile {
    const chunkDelayMs = options.chunkDelayMs ?? 400;
    return withTitle(options.title, {
        match: {userMessage: options.prompt},
        response: {content: options.content},
        chunkSize: options.chunkSize ?? 12,
        ...(options.firstTokenDelayMs === undefined ?
            {latency: chunkDelayMs} :
            {streamingProfile: {ttft: options.firstTokenDelayMs, tps: 1000 / chunkDelayMs}}),
    });
}

/** A reply that arrives (almost) immediately in a single chunk. */
export function instantReply(options: {prompt: string; content: string; title?: string}): AIMockFixtureFile {
    return withTitle(options.title, {
        match: {userMessage: options.prompt},
        response: {content: options.content},
        chunkSize: Math.max(options.content.length, 1),
    });
}

/** The LLM request fails with an HTTP error before any text streams. */
export function failingReply(options: {prompt: string; status?: number; message?: string; title?: string}): AIMockFixtureFile {
    return withTitle(options.title, {
        match: {userMessage: options.prompt},
        response: {
            status: options.status ?? 500,
            error: {message: options.message ?? 'Scripted LLM failure', type: 'server_error', code: 'scripted_failure'},
        },
    });
}

/** The stream starts, then is cut off after `afterChunks` chunks (no finish chunk, no [DONE]). */
export function truncatedReply(options: SlowReplyOptions & {afterChunks: number}): AIMockFixtureFile {
    const file = slowReply(options);
    const fixture = file.fixtures[file.fixtures.length - 1] as ScriptedFixture;
    fixture.truncateAfterChunks = options.afterChunks;
    return file;
}

/** The connection drops `afterMs` after the response starts, mid-stream. */
export function droppedConnectionReply(options: SlowReplyOptions & {afterMs: number}): AIMockFixtureFile {
    const file = slowReply(options);
    const fixture = file.fixtures[file.fixtures.length - 1] as ScriptedFixture;
    fixture.disconnectAfterMs = options.afterMs;
    return file;
}
