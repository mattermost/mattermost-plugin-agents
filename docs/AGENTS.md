# Writing the Agents docs

These pages are published on docs.mattermost.com. Write them as product documentation for the people who use and run Agents, not as notes for contributors.

## How the docs are published

- mattermost/mattermost vendors this repo as the submodule `docs/vendor/mattermost-plugin-agents`. `docs/site/scripts/stage-agents-docs.mjs` copies every `docs/**/*.md` except this file and `CLAUDE.md` into the site at `/agents/docs/<path>`.
- A page is only indexed and in the nav if it is in `NAV_CHILDREN` in that script and placed by `gen-documentation-sidebar.mjs`. Anything else is published as `unlisted` (noindex). A new page needs a PR there.
- `admin_guide.md`, `user_guide.md` and `what_agents_see.md` are inlined into curated pages in that repo. Their content shows up on those pages, not at their own URLs.
- Renaming or deleting a page breaks its URL. Add a redirect in that repo's `docs/site/sidebars/active-redirects.json` and update `NAV_CHILDREN` and the sidebar in the same PR.
- The first paragraph becomes the page's meta description and is what search results and AI tools quote. Lead with what the page is for in about 160 characters.
- Pages are compiled as MDX. Don't write bare `<placeholders>` outside code; use `CHANNEL_ID` style or put them in backticks.

## Audience

Every page is for one audience. Don't mix them.

- **End users** chat with agents, use them in channels, approve tool calls and connect MCP servers. No System Console paths, config keys, permissions or server logs. Say "ask your System Admin" instead.
- **System Admins** set up services, agents, MCP, tool policies, licensing and security.
- **Developers and integrators** connect external AI clients to the Mattermost MCP server or use the LLM Bridge.

## Accuracy

- Check every setting, default, limit, permission, license tier and tool name against the code before writing it. Use labels exactly as the UI shows them and add the config key where an admin might script it.
- If you can't verify something from this repo (another plugin, a third-party client, a server release), say less, or send the reader to that product's docs. Don't guess steps.
- Document what is on master. Docs go live on docs.mattermost.com when the submodule is bumped for a release.
- A PR that changes user-visible behavior updates the docs in the same PR.

## Content

- Be concise but complete. Skip the obvious ("Select **Save** to save"), and state every non-obvious fact: requirements, license gates, defaults, what happens when something isn't configured, and limitations.
- Lead with what agents do: tools, MCP servers, acting across Mattermost and other systems.
- Each fact has one canonical page. Other pages link to it instead of restating it.
- Make sections self-contained. Don't rely on "as mentioned above".
- Use concrete examples, such as a real prompt, config snippet or client command.
- Use tables for settings and comparisons, and numbered steps only for procedures.

## Voice

- Plain declarative sentences, second person, answer first. Write like a pragmatic engineer explaining it to a colleague, in a professional register.
- Say limitations plainly ("Mobile can't start MCP OAuth.").
- No marketing words (seamless, powerful, robust, leverage, unlock, empower, streamline).
- No filler ("Note that", "simply", "just", "easily", "It's worth noting").
- No intros that restate the heading and no summary paragraphs at the end. Avoid "This page covers A, B, and C".
- No em dashes, rhythmic triplets, "It's not X, it's Y", aphorisms, exclamation marks or emoji.
- Use sentence case for headings. Call the product Mattermost Agents, or Agents. Mention the old Copilot name only where it helps someone searching for it.
- The site's Vale rules in mattermost/mattermost `docs/styles/Mattermost/` apply.

## Links and images

- Link between pages here with relative `.md` links, which work on GitHub and on the site. Link to other docs.mattermost.com pages by site path (`/end-user-guide/...`), without `.html`.
- Keep images in `img/`, only when they match the current UI, and delete unused ones.
