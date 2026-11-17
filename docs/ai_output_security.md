# Secure AI-generated links and HTML

Two settings under **System Console > Plugins > Agents > AI Functions**, both off by default, decide whether agent replies can contain clickable links (with an optional domain allowlist) and run sandboxed HTML artifacts. A model can be steered by a prompt injection or a poisoned document into writing a link or HTML page that harms the reader.

## Render AI-generated links

| Setting | Config key | Default |
|---|---|---|
| **Render AI-generated links** | `allowUnsafeLinks` | Off |
| **Allowed Link Domains (csv)** | `allowedLinkDomains` | Empty |

With **Render AI-generated links** off, Markdown links in agent replies show as plain text followed by their destination, for example `Sign in : https://unlisted.example/login`. People can read where a link points, but they can't click it. Citations from web search stay clickable.

Turning it on lets links be clickable. This is a security tradeoff, because a model can write a link to a phishing or data-exfiltration site. Turn it on only where users are trained to check links and you have endpoint and URL controls. To keep the risk down, also set an allowlist.

### Limit links to specific domains

**Allowed Link Domains (csv)** appears when the toggle is on. Enter comma-separated hostnames, for example `mattermost.atlassian.net, *.mycompany.com`.

- `example.com` matches only that exact host, not `docs.example.com`.
- `*.example.com` matches any subdomain, such as `docs.example.com` and `a.b.example.com`, but not `example.com` itself. To allow both, list both.
- Matching ignores case, and ignores ports and paths in entries.
- Saving fails if an entry isn't a valid domain, such as `*` or `my domain.com`, so a typo can't silently block links you meant to allow.
- With an allowlist, only `http` and `https` links can be clickable. Others, such as `mailto:`, show as plain text.
- Links to your own Mattermost Server, such as permalinks and channel links, are always clickable.
- Web search citations follow the allowlist too.

A link to a domain that isn't on the list shows as plain text with its destination, the same as when links are off. Leave the field empty to allow any domain.

## HTML artifacts

HTML artifacts render HTML files that an agent attaches to its reply as interactive pages in the conversation, with a fullscreen viewer. They cover files created with the `CreateFile` tool and HTML files from the code execution sandbox. This feature is experimental.

Turn on **Enable HTML artifacts** (`enableHTMLArtifacts`). Only HTML files (`.html`, `.htm`) on posts written by Agents' own bots render. Artifacts larger than 5 MiB, or the server's maximum file size if that's smaller, aren't rendered, and viewers can download them instead.

### Requirements

| Requirement | Why |
|---|---|
| **Chromium 152 or later** for full network isolation | Mattermost sends a `Connection-Allowlist: ()` header that blocks every network connection an artifact starts, including WebRTC. Chrome, Edge, and the Mattermost desktop app honor it from Chromium 152. Firefox and Safari ignore it. |
| HTTPS, or `localhost` | Browsers send the fetch metadata headers the viewer relies on only to secure origins. Over plain HTTP, viewers see "can only be viewed inside Mattermost". |
| **Site URL** matching the address users open | The artifact's bridge posts messages only to the Site URL's origin. If users reach Mattermost at another origin, artifacts fail with "This artifact could not be loaded." If **Site URL** is unset, the bridge posts to any origin, which weakens this protection. |

If you need full isolation, enable artifacts only where everyone uses a supported browser.

### How artifacts are contained

- Nothing runs on its own. Everyone, including the person who asked for the artifact, sees a placeholder and must select **Run**. This prevents an injected artifact from acting without a human decision. Once run, an artifact keeps running in that browser session, but a transparent **Click to interact** layer covers it until the viewer clicks.
- The page is served in an opaque origin with `sandbox allow-scripts`, so it can't read Mattermost cookies or local storage, or call the Mattermost API. It's served only for HTML files on posts by Agents' bots, and only to users who can read the channel.
- The content security policy blocks network requests and external scripts, styles, fonts, and images. Only inline code and `data:` and `blob:` resources work.
- The viewer embeds each artifact in a wrapper frame that allows only that artifact's URL, so an artifact can't navigate its frame to another site.
- A permissions policy denies the camera, microphone, geolocation, clipboard read, payment, USB, serial, HID, and Bluetooth.
- Artifacts can't open the URL in a new tab and see content. Any load that Mattermost's own page didn't start gets a static page that says the artifact can only be viewed inside Mattermost.
- The only data an artifact can reach is the viewer's theme and locale, and, after the viewer explicitly allows it, the viewer's name and username through `window.mattermost.getCurrentUser()`. It never gets the user ID, email, roles, or tokens.
- A bordered header that the artifact can't draw over always tells viewers the content is AI-generated and that they should never enter passwords or sensitive information.
- The **Download** button gives the original file. Opened from disk, it has none of these protections, so treat it like any untrusted HTML file.

### Known limitations

- In browsers other than Chromium 152 or later, an artifact can open WebRTC connections and send data out. That data can include any profile data the viewer allowed, and anything the viewer types into it. Mattermost removes WebRTC constructors from the page as best effort, but child frames and `blob:` or `data:` documents get fresh ones.
- A known Chromium issue, seen in Chrome 155, crashes the tab when an artifact creates a WebRTC connection in a nested frame while `Connection-Allowlist` is on. No data leaves, but the viewer must reload Mattermost.
- An artifact can write to the clipboard on a click. What it copies can differ from what it shows. Treat anything copied out of an artifact, especially commands, as untrusted.
- An artifact can imitate a sign-in dialog inside its frame. The always-visible header is the countermeasure.
- Artifact content can include conversation or channel content the model saw, and a prompt injection could make the model write an artifact that tries to leak it. This is why artifacts run only after a click and the feature is off by default.
