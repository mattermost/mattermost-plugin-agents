-- morph:nontransactional
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agents_projects_next_checkin
    ON Agents_Projects(NextCheckinAt) WHERE NextCheckinAt > 0;
