-- morph:nontransactional
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agents_projectthreads_channel
    ON Agents_ProjectThreads(ChannelID, CreateAt);
