/**
 * Minimal typed Mattermost REST session used by the notification lab.
 *
 * The lab needs endpoints that @mattermost/client does not expose (attach a
 * device id to a session, per-thread state) and wants one login per call-site
 * with an explicit, inspectable token, so it talks to the API through fetch.
 */

export type MmUser = {
    id: string;
    username: string;
    email: string;
};

export type MmChannel = {
    id: string;
    name: string;
    display_name: string;
    type: string;
    team_id: string;
    total_msg_count: number;
    total_msg_count_root: number;
};

export type MmChannelMember = {
    channel_id: string;
    user_id: string;
    msg_count: number;
    msg_count_root: number;
    mention_count: number;
    mention_count_root: number;
    urgent_mention_count: number;
    last_viewed_at: number;
    notify_props: Record<string, string>;
};

export type MmPost = {
    id: string;
    create_at: number;
    user_id: string;
    channel_id: string;
    root_id: string;
    message: string;
    type: string;
    props?: Record<string, unknown>;
};

export type MmThread = {
    id: string;
    reply_count: number;
    unread_replies: number;
    unread_mentions: number;
    last_reply_at: number;
    last_viewed_at: number;
};

export type MmUserStatus = 'online' | 'away' | 'dnd' | 'offline';

export class MmApiError extends Error {
    constructor(readonly status: number, readonly method: string, readonly path: string, readonly body: string) {
        super(`${method} ${path} failed with ${status}: ${body.slice(0, 300)}`);
    }
}

export class MmRest {
    private token = '';

    constructor(readonly baseUrl: string) {}

    get authToken(): string {
        return this.token;
    }

    async login(loginId: string, password: string): Promise<MmUser> {
        const response = await fetch(`${this.baseUrl}/api/v4/users/login`, {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({login_id: loginId, password}),
        });
        if (!response.ok) {
            throw new MmApiError(response.status, 'POST', '/users/login', await response.text());
        }
        const token = response.headers.get('token');
        if (!token) {
            throw new Error('Login response did not include a session token');
        }
        this.token = token;
        return (await response.json()) as MmUser;
    }

    async request<T>(method: string, path: string, body?: unknown): Promise<T> {
        const response = await fetch(`${this.baseUrl}/api/v4${path}`, {
            method,
            headers: {
                'Content-Type': 'application/json',
                ...(this.token ? {Authorization: `Bearer ${this.token}`} : {}),
            },
            body: body === undefined ? undefined : JSON.stringify(body),
        });
        const text = await response.text();
        if (!response.ok) {
            throw new MmApiError(response.status, method, path, text);
        }
        return (text ? JSON.parse(text) : {}) as T;
    }

    get<T>(path: string): Promise<T> {
        return this.request<T>('GET', path);
    }

    post<T>(path: string, body?: unknown): Promise<T> {
        return this.request<T>('POST', path, body ?? {});
    }

    put<T>(path: string, body?: unknown): Promise<T> {
        return this.request<T>('PUT', path, body ?? {});
    }

    patch<T>(path: string, body?: unknown): Promise<T> {
        return this.request<T>('PATCH', path, body ?? {});
    }

    /**
     * Marks this session as a mobile device session. The server only sends
     * push notifications to sessions that carry a device id.
     */
    attachDevice(deviceId: string): Promise<unknown> {
        return this.put('/users/sessions/device', {device_id: deviceId});
    }

    setStatus(userId: string, status: MmUserStatus): Promise<unknown> {
        return this.put(`/users/${userId}/status`, {user_id: userId, status});
    }

    /** Null when the user is not a member of the channel. */
    async getChannelMember(channelId: string, userId: string): Promise<MmChannelMember | null> {
        try {
            return await this.get<MmChannelMember>(`/channels/${channelId}/members/${userId}`);
        } catch (error) {
            if (error instanceof MmApiError && error.status === 404) {
                return null;
            }
            throw error;
        }
    }

    getChannel(channelId: string): Promise<MmChannel> {
        return this.get<MmChannel>(`/channels/${channelId}`);
    }

    /**
     * Returns null when the user cannot see or does not follow the thread:
     * the server answers 404 for a thread without membership and 403 when the
     * user is not a member of the thread's channel.
     */
    async getThread(userId: string, teamId: string, threadId: string): Promise<MmThread | null> {
        try {
            return await this.get<MmThread>(`/users/${userId}/teams/${teamId}/threads/${threadId}`);
        } catch (error) {
            if (error instanceof MmApiError && (error.status === 404 || error.status === 403)) {
                return null;
            }
            throw error;
        }
    }

    async getChannelPosts(channelId: string, perPage = 200): Promise<MmPost[]> {
        const list = await this.get<{order: string[]; posts: Record<string, MmPost>}>(
            `/channels/${channelId}/posts?per_page=${perPage}`,
        );
        return list.order.map((id) => list.posts[id]).sort((a, b) => a.create_at - b.create_at);
    }

    createPost(channelId: string, message: string, rootId = ''): Promise<MmPost> {
        return this.post<MmPost>('/posts', {channel_id: channelId, message, root_id: rootId});
    }

    /**
     * Skips the onboarding tours and the "switch your status back to Online?"
     * prompt, which otherwise covers the composer of a browser whose user has
     * a manual non-online status.
     */
    async setSkipOnboarding(userId: string): Promise<void> {
        const pref = (category: string, name: string, value: string) => ({user_id: userId, category, name, value});
        await this.put(`/users/${userId}/preferences`, [
            pref('tutorial_step', userId, '999'),
            pref('onboarding_task_list', 'onboarding_task_list_show', 'false'),
            pref('onboarding_task_list', 'onboarding_task_list_open', 'false'),
            pref('drafts', 'drafts_tour_tip_showed', JSON.stringify({drafts_tour_tip_showed: true})),
            pref('crt_thread_pane_step', userId, '999'),
            pref('auto_reset_manual_status', userId, 'false'),
        ]);
    }
}
