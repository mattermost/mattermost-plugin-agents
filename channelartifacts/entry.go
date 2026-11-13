// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelartifacts

const (
	// DefaultMaxPerChannel caps how many artifacts a channel may keep when
	// the site config leaves the limit unset (0).
	DefaultMaxPerChannel = 50

	// MaxPerChannel is the historical name for DefaultMaxPerChannel.
	MaxPerChannel = DefaultMaxPerChannel

	// MaxTitleRunes matches core channel bookmark display name limits.
	MaxTitleRunes = 64

	// MaxDescriptionRunes caps the optional description field.
	MaxDescriptionRunes = 256

	// MaxChunkBytes is the uncompressed write size of one KV blob chunk
	// before gzip. Compressed chunks stay well under Mattermost KV limits.
	MaxChunkBytes = 512 * 1024

	schemaVersion = 1
)

// VersionSnapshot is the content head shared by the catalog entry and a version
// record: the bytes, where they came from, and the title captured with that write.
// A later rename changes the entry only. Rollback copies the record's snapshot back.
type VersionSnapshot struct {
	Title                  string `json:"title"`
	Description            string `json:"description,omitempty"`
	SourcePostID           string `json:"source_post_id"`
	SourceFileID           string `json:"source_file_id"`
	SourceFileName         string `json:"source_file_name"`
	SourceBotUserID        string `json:"source_bot_user_id"`
	SourceAgentDisplayName string `json:"source_agent_display_name,omitempty"`
	// SourceRootPostID is the thread root (post.RootId, or post.Id when the
	// source post is itself a root). Used for lineage matching across edits.
	SourceRootPostID string `json:"source_root_post_id,omitempty"`
	UpdateAt         int64  `json:"update_at"`
	UpdatedBy        string `json:"updated_by,omitempty"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256"`
	ChunkCount       int    `json:"chunk_count"`
	// Version is the content version. It increments when the HTML body is
	// replaced; rename and description patches leave it alone.
	Version int `json:"version"`
}

// Entry is the catalog metadata for one channel-saved HTML artifact.
// VersionSnapshot always describes the current head content version.
type Entry struct {
	ID            string `json:"id"`
	ChannelID     string `json:"channel_id"`
	TeamID        string `json:"team_id"`
	CreatedBy     string `json:"created_by"`
	CreateAt      int64  `json:"create_at"`
	Encoding      string `json:"encoding"`
	SchemaVersion int    `json:"schema_version"`
	// Purging hides the entry from listings while a failed purge is retried.
	// Content and the SHA lookup stay until that cleanup finishes.
	Purging bool `json:"purging,omitempty"`
	VersionSnapshot
}

// VersionRecord is one content snapshot for an artifact.
// Bodies are stored separately under versioned blob keys.
type VersionRecord struct {
	VersionSnapshot
	// LegacyUnversionedBlobs means chunks live at cart_blob_{id}_{n} rather
	// than cart_blob_{id}_v{version}_{n}. Set when migrating pre-versioned rows.
	LegacyUnversionedBlobs bool `json:"legacy_unversioned_blobs,omitempty"`
}

// VersionHistory is the ordered list of content versions for one artifact.
type VersionHistory struct {
	Versions []VersionRecord `json:"versions"`
	// NextVersion is one past the highest number ever issued. Pruning does not
	// lower it, so a later save cannot reuse a number whose blobs were deleted.
	NextVersion int `json:"next_version,omitempty"`
	// HeadVersion is the version a rollback has selected. Prune keeps it even
	// when the metadata write has not landed yet.
	HeadVersion int `json:"head_version,omitempty"`
	// PendingDelete lists versions removed from Versions whose blobs may still exist.
	PendingDelete []VersionRecord `json:"pending_delete,omitempty"`
}

// ChannelIndex is the ordered list of artifact IDs for one channel.
type ChannelIndex struct {
	Version int      `json:"version"`
	IDs     []string `json:"ids"`
}

// ListResponse is returned by the list API.
type ListResponse struct {
	Entries         []*Entry `json:"entries"`
	CanManage       bool     `json:"can_manage"`
	MaxPerChannel   int      `json:"max_per_channel"`
	DownloadEnabled bool     `json:"download_enabled"`
	RenderEnabled   bool     `json:"render_enabled"`
}

// ContentLimits carries size and retention caps into store writes.
type ContentLimits struct {
	MaxSize        int64
	MaxPerChannel  int
	MaxSitewide    int
	VersionsToKeep int
}

// CreateRequest is the body for saving a post artifact into the channel library.
type CreateRequest struct {
	ChannelID   string `json:"channel_id"`
	PostID      string `json:"post_id"`
	FileID      string `json:"file_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// ReplaceID, when set, replaces that library entry's HTML instead of creating a new one.
	ReplaceID string `json:"replace_id,omitempty"`
}

// PatchRequest is the body for renaming a saved artifact.
type PatchRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}

// ReplaceRequest is the body for replacing a library entry's HTML from a bot post file.
type ReplaceRequest struct {
	PostID string `json:"post_id"`
	FileID string `json:"file_id"`
}

// RollbackRequest selects which content version becomes head.
type RollbackRequest struct {
	Version int `json:"version"`
}

// OrderRequest is the body for reordering a channel's artifacts.
type OrderRequest struct {
	ChannelID string   `json:"channel_id"`
	IDs       []string `json:"ids"`
}

// ExportEntry is one artifact in a system-admin channel export.
type ExportEntry struct {
	Meta                   *Entry            `json:"meta"`
	Versions               []VersionRecord   `json:"versions"`
	ContentBase64ByVersion map[string]string `json:"content_base64_by_version"`
}

// ExportResponse is returned by the channel-artifacts export API.
type ExportResponse struct {
	Entries []ExportEntry `json:"entries"`
}

// TeamListResponse is returned by the team-scoped artifact catalog API.
type TeamListResponse struct {
	Entries []*Entry `json:"entries"`
}
