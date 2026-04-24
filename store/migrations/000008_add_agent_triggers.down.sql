ALTER TABLE Agents_UserAgents
    DROP COLUMN IF EXISTS Subscriptions,
    DROP COLUMN IF EXISTS Schedules;
