// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {LinkDomainRule, isHostAllowed, linkAllowlist, linkRendering, restrictLinks} from './link_allowlist';

const SITE_URL = 'https://mm.example.org';

const plain = (host: string): LinkDomainRule => ({host, wildcard: false});
const wildcard = (host: string): LinkDomainRule => ({host, wildcard: true});

// Mirrors the anchor markup core's markdown renderer emits for links.
const externalLink = (href: string, text: string) =>
    `<a class="theme markdown__link" href="${href}" rel="noreferrer" target="_blank">${text}</a>`;

describe('isHostAllowed', () => {
    test.each([
        ['exact match', 'example.com', [plain('example.com')], true],
        ['hostnames are case-insensitive', 'Example.COM', [plain('example.com')], true],
        ['trailing dot on the host is ignored', 'example.com.', [plain('example.com')], true],
        ['plain rule does not match subdomains', 'sub.example.com', [plain('example.com')], false],
        ['wildcard matches a subdomain', 'sub.example.com', [wildcard('example.com')], true],
        ['wildcard matches nested subdomains', 'a.b.example.com', [wildcard('example.com')], true],
        ['wildcard does not match the bare domain', 'example.com', [wildcard('example.com')], false],
        ['suffix without a dot boundary is rejected', 'evilexample.com', [wildcard('example.com')], false],
        ['allowed domain as a prefix of another host is rejected', 'example.com.evil.com', [plain('example.com')], false],
        ['empty host is rejected', '', [plain('example.com')], false],
        ['empty allowlist matches nothing', 'example.com', [], false],
        ['any matching rule is enough', 'docs.example.com', [plain('other.com'), wildcard('example.com')], true],
    ])('%s', (_name, hostname, allowedDomains, expected) => {
        expect(isHostAllowed(hostname, allowedDomains)).toBe(expected);
    });
});

describe('linkAllowlist', () => {
    const allowed = [plain('example.com'), plain('xn--bcher-kva.example')];

    test.each([
        ['allowed https host', 'https://example.com/page', SITE_URL, true],
        ['allowed http host', 'http://example.com', SITE_URL, true],
        ['unicode host matches its punycode rule', 'https://bücher.example/', SITE_URL, true],
        ['other host', 'https://evil.com', SITE_URL, false],
        ['allowed host as userinfo', 'https://example.com@evil.com/', SITE_URL, false],
        ['protocol-relative other host', '//evil.com/x', SITE_URL, false],
        ['backslash before allowed host', 'https://evil.com\\@example.com', SITE_URL, false],

        // eslint-disable-next-line no-script-url
        ['javascript scheme', 'javascript:alert(1)', SITE_URL, false],
        ['mailto scheme', 'mailto:someone@example.com', SITE_URL, false],
        ['non-http scheme on allowed host', 'ftp://example.com/file', SITE_URL, false],
        ['unparseable URL', 'http://[invalid', SITE_URL, false],
        ['permalink on this server', 'https://mm.example.org/team/pl/abc', SITE_URL, true],
        ['relative link on this server', '/team/channels/town-square', SITE_URL, true],
        ['site URL as a prefix of another host', 'https://mm.example.org.evil.com/', SITE_URL, false],
        ['subpath site: link under the subpath', 'https://host.org/mm/team/pl/abc', 'https://host.org/mm', true],
        ['subpath site: link outside the subpath', 'https://host.org/other', 'https://host.org/mm', false],
        ['subpath site: root-relative route is routed in-app', '/team/channels/town-square', 'https://host.org/mm', true],
        ['backslash protocol-relative other host', '/\\evil.com/x', SITE_URL, false],
        ['scheme-less link with userinfo-like path stays on this server', '/example.com@evil.com', SITE_URL, true],
        ['subpath site: sibling path sharing a prefix', 'https://host.org/mmevil', 'https://host.org/mm', false],
        ['unusable site URL: absolute allowed link', 'https://example.com', '', true],
        ['unusable site URL: relative link', '/team/channels/town-square', '', false],
    ])('%s', (_name, href, siteURL, expected) => {
        expect(linkAllowlist(allowed, siteURL)(href)).toBe(expected);
    });
});

describe('restrictLinks', () => {
    const isAllowed = linkAllowlist([plain('example.com')], SITE_URL);

    const render = (html: string) => {
        const container = document.createElement('div');
        container.innerHTML = restrictLinks(html, isAllowed);
        return container;
    };

    test('keeps allowed links clickable', () => {
        const container = render(`<p>${externalLink('https://example.com/docs', 'docs')}</p>`);

        const anchor = container.querySelector('a');
        expect(anchor?.getAttribute('href')).toBe('https://example.com/docs');
        expect(anchor?.textContent).toBe('docs');
    });

    test.each([
        ['shows the text followed by the destination', `See ${externalLink('https://evil.com/login', 'the docs')} now`, 'See the docs : https://evil.com/login now'],
        ['does not repeat a destination equal to the text', externalLink('https://evil.com/login', 'https://evil.com/login'), 'https://evil.com/login'],
        ['does not repeat the destination of a scheme-less autolink', externalLink('http://www.evil.com', 'www.evil.com'), 'www.evil.com'],
        ['renders the destination as inert text', externalLink('https://evil.com/&lt;img src=x&gt;', 'x'), 'x : https://evil.com/<img src=x>'],
    ])('disallowed link: %s', (_name, html, expectedText) => {
        const container = render(`<p>${html}</p>`);

        expect(container.querySelector('a')).toBeNull();
        expect(container.querySelector('img')).toBeNull();
        expect(container.textContent).toBe(expectedText);
    });

    test('preserves formatting inside a disallowed link', () => {
        const container = render(`<p>${externalLink('https://evil.com', '<strong>Click</strong>')}</p>`);

        expect(container.querySelector('a')).toBeNull();
        expect(container.querySelector('strong')?.textContent).toBe('Click');
        expect(container.textContent).toBe('Click : https://evil.com');
    });

    test('filters each link independently', () => {
        const container = render(`<ul><li>${externalLink('https://example.com', 'good')}</li><li>${externalLink('https://evil.com', 'bad')}</li></ul>`);

        const anchors = container.querySelectorAll('a');
        expect(anchors).toHaveLength(1);
        expect(anchors[0].getAttribute('href')).toBe('https://example.com');
        expect(container.textContent).toContain('bad : https://evil.com');
    });

    test('keeps links back into this Mattermost server', () => {
        const container = render('<p><a class="mention-link" href="/team/channels/town-square" data-channel-mention="town-square">~town-square</a></p>');

        expect(container.querySelector('a')?.getAttribute('data-channel-mention')).toBe('town-square');
    });

    test('returns HTML without disallowed links unchanged', () => {
        const html = `<p>No <strong>bad</strong> ${externalLink('https://example.com', 'links')} here</p>`;
        expect(restrictLinks(html, isAllowed)).toBe(html);
    });
});

describe('linkRendering', () => {
    const html = `<p>${externalLink('https://example.com', 'good')} ${externalLink('https://evil.com', 'bad')}</p>`;

    test.each([
        {name: 'plain text policy lets core render links as text', policy: {kind: 'plain_text' as const}, unsafeLinks: true, expectedHrefs: ['https://example.com', 'https://evil.com']},
        {name: 'any domain policy keeps every link', policy: {kind: 'any_domain' as const}, unsafeLinks: false, expectedHrefs: ['https://example.com', 'https://evil.com']},
        {name: 'allowlist policy keeps only allowed links', policy: {kind: 'allowlist' as const, allowedDomains: [plain('example.com')]}, unsafeLinks: false, expectedHrefs: ['https://example.com']},
        {name: 'empty allowlist keeps no external links', policy: {kind: 'allowlist' as const, allowedDomains: []}, unsafeLinks: false, expectedHrefs: []},
    ])('$name', ({policy, unsafeLinks, expectedHrefs}) => {
        const rendering = linkRendering(policy, SITE_URL);

        const container = document.createElement('div');
        container.innerHTML = rendering.restrictLinks(html);

        expect(rendering.unsafeLinks).toBe(unsafeLinks);
        expect(Array.from(container.querySelectorAll('a')).map((a) => a.getAttribute('href'))).toEqual(expectedHrefs);
    });
});

describe('linkRendering citations', () => {
    test.each([
        {name: 'plain text policy leaves citations alone', policy: {kind: 'plain_text' as const}, url: 'https://evil.com/x', expected: true},
        {name: 'any domain policy leaves citations alone', policy: {kind: 'any_domain' as const}, url: 'https://evil.com/x', expected: true},
        {name: 'allowlist allows citations to allowed hosts', policy: {kind: 'allowlist' as const, allowedDomains: [plain('example.com')]}, url: 'https://example.com/x', expected: true},
        {name: 'allowlist blocks citations to other hosts', policy: {kind: 'allowlist' as const, allowedDomains: [plain('example.com')]}, url: 'https://evil.com/x', expected: false},
        {name: 'allowlist blocks non-http citations', policy: {kind: 'allowlist' as const, allowedDomains: [plain('example.com')]}, url: 'ftp://example.com/x', expected: false},
    ])('$name', ({policy, url, expected}) => {
        expect(linkRendering(policy, SITE_URL).isCitationUrlAllowed(url)).toBe(expected);
    });
});
