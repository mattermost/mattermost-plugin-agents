import type {PostSummary, UserReport} from './types';

function quote(text: string, max = 70): string {
    const flat = text.replace(/\s+/g, ' ').trim();
    const clipped = flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
    return JSON.stringify(clipped);
}

function pad(value: string | number, width: number): string {
    return String(value).padEnd(width);
}

/**
 * Aliases (`P1`, `P2`, ...) keep the tables readable: raw post ids are 26
 * random characters. The map covers every post of the watched channel, in
 * creation order, so an alias is stable across reports of the same channel.
 */
export function buildPostAliases(postIds: string[]): Map<string, string> {
    return new Map(postIds.map((id, index) => [id, `P${index + 1}`]));
}

export function formatReport(
    label: string,
    users: UserReport[],
    posts: PostSummary[],
    aliases: Map<string, string>,
    unattributed: {push: number; email: number},
): string {
    const alias = (id: string) => (id ? (aliases.get(id) ?? `?${id.slice(0, 6)}`) : '-');
    const lines: string[] = [];
    lines.push(`ReceivedReport: ${label}`);
    lines.push('='.repeat(Math.max(40, label.length + 16)));

    if (posts.length > 0) {
        lines.push('Posts since mark:');
        for (const post of posts) {
            const where = post.rootAlias === '-' ? 'root' : `reply to ${post.rootAlias}`;
            const type = post.type ? ` [${post.type}]` : '';
            lines.push(`  ${pad(post.alias, 4)} ${pad(post.author, 12)} ${pad(where, 14)}${type} ${quote(post.message)}`);
        }
        lines.push('');
    }

    const header = [
        pad('user', 12), pad('status', 8), pad('desktop', 8), pad('push', 5), pad('email', 6),
        pad('unread', 7), pad('mention', 8), pad('thread(unread/mention)', 23), 'ui',
    ].join(' ');
    lines.push(header);
    lines.push('-'.repeat(header.length + 12));
    for (const user of users) {
        const pushCount = user.push.filter((p) => p.type === 'message').length;
        const otherPush = user.push.length - pushCount;
        const thread = user.thread ?
            (user.thread.isFollowing ? `${user.thread.unreadReplies}/${user.thread.unreadMentions}` : 'not following') :
            '-';
        const ui = user.ui ? `${user.ui.unread ? 'unread' : 'read'}${user.ui.mentionBadge ? ` @${user.ui.mentionBadge}` : ''}` : '-';
        lines.push([
            pad(user.userKey, 12),
            pad(user.status, 8),
            pad(user.desktop.length, 8),
            pad(otherPush ? `${pushCount}+${otherPush}` : pushCount, 5),
            pad(user.email.length, 6),
            pad(user.channelUnread ? user.channelUnread.unreadMsgs : '-', 7),
            pad(user.channelUnread ? user.channelUnread.mentionCount : '-', 8),
            pad(thread, 23),
            ui,
        ].join(' '));
    }

    lines.push('');
    lines.push('Details:');
    for (const user of users) {
        const nothing = user.desktop.length + user.push.length + user.email.length === 0;
        lines.push(`  ${user.userKey} (@${user.username}, ${user.status})${nothing ? ': nothing received' : ''}`);
        for (const n of user.desktop) {
            lines.push(`    desktop #${n.seq}: title=${quote(n.title, 50)} body=${quote(n.body)} tag=${quote(n.tag, 20)}`);
        }
        for (const p of user.push) {
            lines.push(
                `    push ${pad(p.type, 12)} post=${alias(p.postId)} root=${alias(p.rootId)} sender=${quote(p.senderName, 20)} badge=${p.badge} message=${quote(p.message)}`,
            );
        }
        for (const m of user.email) {
            lines.push(`    email subject=${quote(m.subject, 80)} body=${quote(m.text, 100)}`);
        }
        if (user.channelUnread) {
            const c = user.channelUnread;
            lines.push(
                `    channel: unread=${c.unreadMsgs} (root ${c.unreadMsgsRoot}) mentions=${c.mentionCount} (root ${c.mentionCountRoot}) urgent=${c.urgentMentionCount}`,
            );
        }
        if (user.thread) {
            const t = user.thread;
            lines.push(
                t.isFollowing ?
                    `    thread: following replies=${t.replyCount} unreadReplies=${t.unreadReplies} unreadMentions=${t.unreadMentions}` :
                    '    thread: not following',
            );
        }
    }

    if (unattributed.push || unattributed.email) {
        lines.push(`  unattributed: push=${unattributed.push} email=${unattributed.email} (not addressed to a lab user)`);
    }
    return lines.join('\n');
}
