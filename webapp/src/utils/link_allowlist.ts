// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * One allowlist entry, already canonicalized by the server: host is lowercase
 * ASCII (punycode), and a wildcard rule matches subdomains of host only.
 */
export type LinkDomainRule = {
    host: string;
    wildcard: boolean;
};

/** Which AI-generated links render as clickable, as served by GET /ai_bots. */
export type LinkPolicy =
    | {kind: 'plain_text'}
    | {kind: 'any_domain'}
    | {kind: 'allowlist'; allowedDomains: LinkDomainRule[]};

export const PLAIN_TEXT_LINK_POLICY: LinkPolicy = {kind: 'plain_text'};

export type LinkRendering = {

    /** Core formatText's unsafeLinks option: render external links as plain text. */
    unsafeLinks: boolean;

    /** Applied to formatText's HTML output before it becomes React elements. */
    restrictLinks: (html: string) => string;

    /**
     * Whether a citation chip may open this URL. Citations are not Markdown
     * links, so only an allowlist restricts them; the other policies leave
     * them clickable as they were before link policies existed.
     */
    isCitationUrlAllowed: (url: string) => boolean;
};

const keepAllLinks = (html: string) => html;
const allowAnyUrl = () => true;

const PLAIN_TEXT_RENDERING: LinkRendering = {unsafeLinks: true, restrictLinks: keepAllLinks, isCitationUrlAllowed: allowAnyUrl};

/**
 * Turns a link policy into the options PostText renders with. siteURL is the
 * Mattermost server's own URL; links into it are always kept.
 */
export function linkRendering(policy: LinkPolicy, siteURL: string): LinkRendering {
    switch (policy.kind) {
    case 'plain_text':
        return PLAIN_TEXT_RENDERING;
    case 'any_domain':
        return {unsafeLinks: false, restrictLinks: keepAllLinks, isCitationUrlAllowed: allowAnyUrl};
    case 'allowlist': {
        const isAllowed = linkAllowlist(policy.allowedDomains, siteURL);
        return {unsafeLinks: false, restrictLinks: (html) => restrictLinks(html, isAllowed), isCitationUrlAllowed: isAllowed};
    }
    default: {
        // A kind this bundle doesn't know about fails closed.
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const unhandled: never = policy;
        return PLAIN_TEXT_RENDERING;
    }
    }
}

export function isHostAllowed(hostname: string, allowedDomains: LinkDomainRule[]): boolean {
    const host = hostname.toLowerCase().replace(/\.$/, '');
    return host !== '' && allowedDomains.some((rule) => (rule.wildcard ? host.endsWith(`.${rule.host}`) : host === rule.host));
}

/**
 * Builds the predicate deciding whether a link stays clickable: links into
 * the Mattermost server are always allowed, and any other link must be
 * http(s) to an allowed host. URLs are resolved with the browser's own parser,
 * so the host checked is the host a click would navigate to.
 */
export function linkAllowlist(allowedDomains: LinkDomainRule[], siteURL: string): (href: string) => boolean {
    let site: URL | null = null;
    try {
        site = new URL(siteURL);
    } catch {
        // Without a usable site URL nothing counts as internal.
    }
    const siteRoot = site?.href.replace(/\/$/, '');

    // Mirrors core: scheme-less links are in-app routes (the webapp router
    // applies any subpath), and absolute links must start with the site URL.
    // Resolving relative links keeps "//host" and "/\host" external.
    const isInternal = (href: string, url: URL) => {
        if (!site || !siteRoot) {
            return false;
        }
        if (!(/^[a-z][a-z0-9+.-]*:/i).test(href)) {
            return url.origin === site.origin;
        }
        return url.href === siteRoot || ['/', '?', '#'].some((sep) => url.href.startsWith(siteRoot + sep));
    };

    return (href) => {
        let url: URL;
        try {
            url = siteRoot ? new URL(href, siteRoot + '/') : new URL(href);
        } catch {
            return false;
        }
        if (url.protocol !== 'http:' && url.protocol !== 'https:') {
            return false;
        }
        return isInternal(href, url) || isHostAllowed(url.hostname, allowedDomains);
    };
}

// Core's markdown renderer prefixes "http://" onto scheme-less autolinks
// (www.example.com) and "mailto:" onto emails, so those hrefs repeat the text.
function isRedundantHref(text: string, href: string): boolean {
    return href === text || href === `http://${text}` || href === `mailto:${text}`;
}

/**
 * Turns every link in rendered post HTML that isAllowed rejects into plain
 * text, keeping the destination visible the way core does for unsafe links
 * ("text : href").
 */
export function restrictLinks(html: string, isAllowed: (href: string) => boolean): string {
    if (!html) {
        return html;
    }

    // A template's content is inert, so parsing never loads images or runs scripts.
    const template = document.createElement('template');
    template.innerHTML = html;

    let changed = false;
    template.content.querySelectorAll('a[href]').forEach((anchor) => {
        const href = anchor.getAttribute('href') ?? '';
        if (isAllowed(href)) {
            return;
        }

        const text = anchor.textContent ?? '';
        const replacement = document.createDocumentFragment();
        replacement.append(...Array.from(anchor.childNodes));
        if (!isRedundantHref(text, href)) {
            replacement.append(` : ${href}`);
        }
        anchor.replaceWith(replacement);
        changed = true;
    });

    return changed ? template.innerHTML : html;
}
