import type {MmUserStatus} from './rest';

/** Stable key a spec uses to refer to a lab user, e.g. `requester`. */
export type UserKey = string;

/** A push as received by the mock push proxy, before it is attributed to a user. */
export type CapturedPush = {
    /** Index in the proxy's receive order. */
    seq: number;
    /** Proxy receive time (ms since epoch); 0 if unavailable. */
    at: number;
    /** `message`, `clear`, `update_badge`, ... (model.PushNotification.type). */
    type: string;
    platform: string;
    deviceId: string;
    postId: string;
    channelId: string;
    rootId: string;
    channelName: string;
    message: string;
    senderId: string;
    senderName: string;
    badge: number;
    sound: string;
    category: string;
    isIdLoaded: boolean;
    raw: Record<string, unknown>;
};

export type PushRecord = CapturedPush & {
    /** Lab user owning the device the push was addressed to. */
    userKey: UserKey;
};

export type DesktopRecord = {
    /** Receive order inside this user's browser. */
    seq: number;
    /** Browser time (ms since epoch) when `new Notification()` ran. */
    at: number;
    title: string;
    body: string;
    tag: string;
    silent: boolean;
};

export type CapturedEmail = {
    id: string;
    createdAt: number;
    to: string[];
    from: string;
    subject: string;
    text: string;
};

export type EmailRecord = CapturedEmail & {
    userKey: UserKey;
};

export type ChannelUnreadState = {
    channelId: string;
    /** Messages the user has not viewed (`total_msg_count - member.msg_count`). */
    unreadMsgs: number;
    /** Same for root posts only (what CRT clients count). */
    unreadMsgsRoot: number;
    mentionCount: number;
    mentionCountRoot: number;
    urgentMentionCount: number;
};

export type ThreadUnreadState = {
    rootId: string;
    isFollowing: boolean;
    replyCount: number;
    unreadReplies: number;
    unreadMentions: number;
};

/** What the sidebar of the user's open browser shows for the channel; null when no browser or no sidebar item. */
export type UiBadgeState = {
    unread: boolean;
    mentionBadge: number;
};

export type PostSummary = {
    alias: string;
    id: string;
    author: string;
    rootAlias: string;
    message: string;
    type: string;
};

export type UserReport = {
    userKey: UserKey;
    username: string;
    status: MmUserStatus | 'unknown';
    desktop: DesktopRecord[];
    push: PushRecord[];
    email: EmailRecord[];
    /** Null when no channel was requested via `CollectOptions.channelId` or the user is not a member of it. */
    channelUnread: ChannelUnreadState | null;
    /** Shortcut for `channelUnread?.mentionCount ?? 0`. */
    mentionCount: number;
    /** Null when no thread was requested via `CollectOptions.threadRootId`. */
    thread: ThreadUnreadState | null;
    ui: UiBadgeState | null;
};

export type ReceivedReport = {
    label: string;
    users: Record<UserKey, UserReport>;
    /** Posts in the watched channel created since the mark, in order, with the aliases used in the text tables. */
    posts: PostSummary[];
    /** Pushes and emails since the mark that were not addressed to a lab user (e.g. the admin). */
    unattributed: {push: CapturedPush[]; email: CapturedEmail[]};
    /** Readable text table of the whole report. */
    toTable(): string;
};
