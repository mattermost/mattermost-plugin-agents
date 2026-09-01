ALTER TABLE Agents_ChannelAutoReply
    DROP COLUMN IF EXISTS Instructions,
    DROP COLUMN IF EXISTS AnalysisModel;
