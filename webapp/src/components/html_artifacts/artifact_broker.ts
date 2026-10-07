// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Host side of the HTML artifact postMessage protocol v1. The artifact runs in
// an opaque-origin sandboxed iframe; everything it sends is untrusted. This
// module validates every message and answers only the v1 methods, gating any
// user data behind the viewer's explicit consent.

export const ARTIFACT_PROTOCOL_VERSION = 1;

export type DisplayMode = 'inline' | 'fullscreen';
export type ColorScheme = 'light' | 'dark';

export interface ArtifactContext {
    theme: Record<string, string>;
    colorScheme: ColorScheme;
    displayMode: DisplayMode;
    locale: string;
}

// The only user fields ever handed to an artifact.
export interface ArtifactUser {
    id: string;
    username: string;
    firstName: string;
    lastName: string;
    nickname: string;
    displayName: string;
    locale: string;
}

export type ArtifactErrorCode = 'permission_denied' | 'unknown_method' | 'busy' | 'unavailable';

export type ArtifactInboundMessage =
    {mmArtifact: 1; type: 'ready'} |
    {mmArtifact: 1; type: 'escape'} |
    {mmArtifact: 1; type: 'resize'; height: number} |
    {mmArtifact: 1; type: 'request'; id: string; method: string; params?: Record<string, unknown>};

export type ArtifactOutboundMessage =
    {mmArtifact: 1; type: 'context'; context: ArtifactContext} |
    {mmArtifact: 1; type: 'response'; id: string; result?: unknown; error?: {code: ArtifactErrorCode; message: string}};

const MAX_REQUEST_ID_LENGTH = 128;
const MAX_METHOD_LENGTH = 64;

function isPlainObject(v: unknown): v is Record<string, unknown> {
    return typeof v === 'object' && v !== null && !Array.isArray(v);
}

// parseArtifactMessage returns a well-formed inbound message or null.
export function parseArtifactMessage(data: unknown): ArtifactInboundMessage | null {
    if (!isPlainObject(data) || data.mmArtifact !== ARTIFACT_PROTOCOL_VERSION) {
        return null;
    }
    switch (data.type) {
    case 'ready':
        return {mmArtifact: 1, type: 'ready'};
    case 'escape':
        return {mmArtifact: 1, type: 'escape'};
    case 'resize':
        if (typeof data.height !== 'number' || !Number.isFinite(data.height) || data.height < 0) {
            return null;
        }
        return {mmArtifact: 1, type: 'resize', height: data.height};
    case 'request': {
        if (typeof data.id !== 'string' || data.id === '' || data.id.length > MAX_REQUEST_ID_LENGTH) {
            return null;
        }
        if (typeof data.method !== 'string' || data.method === '' || data.method.length > MAX_METHOD_LENGTH) {
            return null;
        }
        if (typeof data.params !== 'undefined' && !isPlainObject(data.params)) {
            return null;
        }
        const msg: ArtifactInboundMessage = {mmArtifact: 1, type: 'request', id: data.id, method: data.method};
        if (data.params) {
            msg.params = data.params;
        }
        return msg;
    }
    default:
        return null;
    }
}

// ---- Consent store -------------------------------------------------------
// Module level so the inline card and the fullscreen viewer share decisions.
// Decisions are cached per fileId for the page lifetime only. That is per
// user: Mattermost logout performs a full page reload, which clears this map.

export type ConsentDecision = 'allow' | 'deny';

const consentDecisions = new Map<string, ConsentDecision>();

// At most one prompt is shown at a time, across all artifact frames.
let pendingPrompt: {fileId: string; cancel: () => void} | null = null;

export function getConsentDecision(fileId: string): ConsentDecision | undefined {
    return consentDecisions.get(fileId);
}

export function setConsentDecision(fileId: string, decision: ConsentDecision) {
    consentDecisions.set(fileId, decision);
}

// Test-only reset.
export function resetConsentStore() {
    consentDecisions.clear();
    pendingPrompt = null;
}

export function isConsentPromptPending(): boolean {
    return pendingPrompt !== null;
}

// cancelPendingConsent drops a pending prompt for fileId without recording a
// decision; the waiting request is answered with permission_denied. Used when
// the prompt would become hidden (e.g. fullscreen opening over the card) so
// another, visible frame can ask instead.
export function cancelPendingConsent(fileId: string) {
    if (pendingPrompt?.fileId === fileId) {
        pendingPrompt.cancel();
    }
}

// ---- Data mapping ----------------------------------------------------------

export interface UserProfileLike {
    id: string;
    username: string;
    first_name?: string;
    last_name?: string;
    nickname?: string;
    locale?: string;
}

export function toArtifactUser(profile: UserProfileLike, fallbackLocale: string): ArtifactUser {
    const firstName = profile.first_name ?? '';
    const lastName = profile.last_name ?? '';
    const nickname = profile.nickname ?? '';
    const fullName = [firstName, lastName].filter(Boolean).join(' ');
    return {
        id: profile.id,
        username: profile.username,
        firstName,
        lastName,
        nickname,
        displayName: fullName || nickname || profile.username,
        locale: profile.locale || fallbackLocale,
    };
}

// Theme keys exposed to artifacts, mapped to the CSS custom properties the
// Mattermost webapp sets on :root for the active theme.
export const THEME_CSS_VARIABLES: Record<string, string> = {
    sidebarBg: '--sidebar-bg',
    sidebarText: '--sidebar-text',
    sidebarUnreadText: '--sidebar-unread-text',
    sidebarTextHoverBg: '--sidebar-text-hover-bg',
    sidebarTextActiveBorder: '--sidebar-text-active-border',
    sidebarTextActiveColor: '--sidebar-text-active-color',
    sidebarHeaderBg: '--sidebar-header-bg',
    sidebarHeaderTextColor: '--sidebar-header-text-color',
    sidebarTeamBarBg: '--sidebar-teambar-bg',
    onlineIndicator: '--online-indicator',
    awayIndicator: '--away-indicator',
    dndIndicator: '--dnd-indicator',
    mentionBg: '--mention-bg',
    mentionColor: '--mention-color',
    centerChannelBg: '--center-channel-bg',
    centerChannelColor: '--center-channel-color',
    newMessageSeparator: '--new-message-separator',
    linkColor: '--link-color',
    buttonBg: '--button-bg',
    buttonColor: '--button-color',
    errorTextColor: '--error-text',
    mentionHighlightBg: '--mention-highlight-bg',
    mentionHighlightLink: '--mention-highlight-link',
};

export function readThemeFromCSS(style: Pick<CSSStyleDeclaration, 'getPropertyValue'>): Record<string, string> {
    const theme: Record<string, string> = {};
    for (const [key, cssVar] of Object.entries(THEME_CSS_VARIABLES)) {
        const value = style.getPropertyValue(cssVar).trim();
        if (value) {
            theme[key] = value;
        }
    }
    return theme;
}

function parseColor(color: string): [number, number, number] | null {
    const c = color.trim();
    let m = (/^#([0-9a-f]{3})$/i).exec(c);
    if (m) {
        return [0, 1, 2].map((i) => parseInt(m![1][i] + m![1][i], 16)) as [number, number, number];
    }
    m = (/^#([0-9a-f]{6})([0-9a-f]{2})?$/i).exec(c);
    if (m) {
        return [0, 2, 4].map((i) => parseInt(m![1].slice(i, i + 2), 16)) as [number, number, number];
    }
    m = (/^rgba?\(\s*(\d+)[\s,]+(\d+)[\s,]+(\d+)/i).exec(c);
    if (m) {
        return [Number(m[1]), Number(m[2]), Number(m[3])];
    }
    return null;
}

export function colorSchemeFor(centerChannelBg: string | undefined): ColorScheme {
    const rgb = centerChannelBg ? parseColor(centerChannelBg) : null;
    if (!rgb) {
        return 'light';
    }
    const [r, g, b] = rgb.map((v) => {
        const s = v / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
    });
    const luminance = (0.2126 * r) + (0.7152 * g) + (0.0722 * b);
    return luminance < 0.4 ? 'dark' : 'light';
}

// ---- Broker -------------------------------------------------------------

export interface ArtifactBrokerOptions {
    fileId: string;

    // The viewer's bridge token for this file. Only the genuine bridge script
    // knows it; a document that replaced the artifact in the frame does not.
    token: string;
    getTargetWindow: () => Window | null | undefined;
    getContext: () => ArtifactContext;
    getCurrentUser: () => ArtifactUser | null;

    // Shows the consent prompt; resolves true when the viewer allows.
    requestConsent: () => Promise<boolean>;

    // Hides a prompt shown by requestConsent after it was cancelled.
    dismissConsent?: () => void;

    // False while this frame's prompt would not be visible to the viewer
    // (e.g. the inline card is covered by the fullscreen viewer).
    canPrompt?: () => boolean;
    onReady?: () => void;
    onResize?: (height: number) => void;

    // Escape pressed inside the artifact (focus there never reaches the host).
    onEscape?: () => void;
}

export class ArtifactBroker {
    private opts: ArtifactBrokerOptions;
    private ready = false;
    private disposed = false;

    constructor(opts: ArtifactBrokerOptions) {
        this.opts = opts;
    }

    // Called when the host (re)loads the iframe; context is resent after the next ready.
    reset() {
        this.ready = false;
    }

    dispose() {
        this.disposed = true;
    }

    isReady() {
        return this.ready;
    }

    // handleMessage returns true when the event was accepted as a message from this artifact.
    handleMessage = (event: MessageEvent): boolean => {
        if (this.disposed) {
            return false;
        }
        const target = this.opts.getTargetWindow();
        if (!target || event.source !== target) {
            return false;
        }

        // Sandboxed (opaque-origin) documents always report the origin 'null'.
        if (event.origin !== 'null') {
            return false;
        }

        // Every message must carry the bridge token: a document that replaced
        // the artifact in the same frame passes the source and origin checks.
        const data: unknown = event.data;
        if (!isPlainObject(data) || typeof data.token !== 'string' || data.token !== this.opts.token) {
            return false;
        }
        const msg = parseArtifactMessage(data);
        if (!msg) {
            return false;
        }

        switch (msg.type) {
        case 'ready':
            this.ready = true;
            this.opts.onReady?.();
            this.sendContext();
            break;
        case 'resize':
            this.opts.onResize?.(msg.height);
            break;
        case 'escape':
            this.opts.onEscape?.();
            break;
        case 'request':
            this.handleRequest(msg.id, msg.method);
            break;
        }
        return true;
    };

    sendContext() {
        if (!this.ready) {
            return;
        }
        this.post({mmArtifact: 1, type: 'context', context: this.opts.getContext()});
    }

    private post(msg: ArtifactOutboundMessage) {
        if (this.disposed) {
            return;
        }

        // An opaque origin can only be targeted with '*'; we only ever send
        // theme context and answers to the artifact's own requests.
        this.opts.getTargetWindow()?.postMessage(msg, '*');
    }

    private respondError(id: string, code: ArtifactErrorCode, message: string) {
        this.post({mmArtifact: 1, type: 'response', id, error: {code, message}});
    }

    private handleRequest(id: string, method: string) {
        switch (method) {
        case 'getCurrentUser':
            this.handleGetCurrentUser(id);
            return;
        default:
            this.respondError(id, 'unknown_method', `Unknown method: ${method}`);
        }
    }

    private respondCurrentUser(id: string) {
        const user = this.opts.getCurrentUser();
        if (!user) {
            this.respondError(id, 'unavailable', 'The current user is unavailable');
            return;
        }

        // Copy the allowlisted fields explicitly so nothing else can leak.
        const result: ArtifactUser = {
            id: user.id,
            username: user.username,
            firstName: user.firstName,
            lastName: user.lastName,
            nickname: user.nickname,
            displayName: user.displayName,
            locale: user.locale,
        };
        this.post({mmArtifact: 1, type: 'response', id, result});
    }

    private handleGetCurrentUser(id: string) {
        const {fileId} = this.opts;
        const decision = getConsentDecision(fileId);
        if (decision === 'allow') {
            this.respondCurrentUser(id);
            return;
        }
        if (decision === 'deny') {
            this.respondError(id, 'permission_denied', 'The viewer declined access to their profile');
            return;
        }
        if (pendingPrompt || (this.opts.canPrompt && !this.opts.canPrompt())) {
            this.respondError(id, 'busy', 'Another permission prompt is pending');
            return;
        }

        const prompt = {fileId, cancel: () => {}}; // eslint-disable-line no-empty-function
        pendingPrompt = prompt;
        new Promise<boolean>((resolve, reject) => {
            prompt.cancel = () => {
                this.opts.dismissConsent?.();
                reject(new Error('cancelled'));
            };
            this.opts.requestConsent().then(resolve, reject);
        }).then((allowed) => {
            setConsentDecision(fileId, allowed ? 'allow' : 'deny');
            return allowed;
        }, () => {
            // Dismissed without an answer: no decision is recorded.
            return false;
        }).then((allowed) => {
            if (pendingPrompt === prompt) {
                pendingPrompt = null;
            }
            if (allowed) {
                this.respondCurrentUser(id);
            } else {
                this.respondError(id, 'permission_denied', 'The viewer declined access to their profile');
            }
        });
    }
}
