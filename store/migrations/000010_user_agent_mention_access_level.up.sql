ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS MentionAccessLevel INT NOT NULL DEFAULT 0;

-- 0 = Everyone (default, and the legacy behavior). Existing agents keep allowing
-- anyone permitted by the user/channel access rules to @mention them; admins can
-- tighten this to channel-admins-only or disable channel mentions per-agent.
