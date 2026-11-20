-- morph:nontransactional
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_agents_useragents_project_channel
    ON Agents_UserAgents(ProjectChannelID) WHERE ProjectChannelID <> '' AND DeleteAt = 0;
