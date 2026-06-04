// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Code generated for loadtest tests - panic stubs for ltuser.User. DO NOT EDIT.

package controller

import (
	"encoding/json"

	"github.com/mattermost/mattermost-load-test-ng/loadtest/store"
	"github.com/mattermost/mattermost-load-test-ng/loadtest/user"
	"github.com/mattermost/mattermost/server/public/model"
)

// panicLTUser satisfies unused user.User methods.
type panicLTUser struct{}

func (panicLTUser) Client() *model.Client4 { panic("unexpected call: Client") }

func (panicLTUser) ClearUserData() { panic("unexpected call: ClearUserData") }

func (panicLTUser) Connect() (<-chan error, error) { panic("unexpected call: Connect") }

func (panicLTUser) Disconnect() error { panic("unexpected call: Disconnect") }

func (panicLTUser) Events() <-chan *model.WebSocketEvent { panic("unexpected call: Events") }

func (panicLTUser) SendTypingEvent(channelId, parentId string) error {
	panic("unexpected call: SendTypingEvent")
}

func (panicLTUser) UpdateActiveChannel(channelId string) error {
	panic("unexpected call: UpdateActiveChannel")
}

func (panicLTUser) UpdateActiveThread(channelId string) error {
	panic("unexpected call: UpdateActiveThread")
}

func (panicLTUser) UpdateActiveTeam(teamId string) error { panic("unexpected call: UpdateActiveTeam") }

func (panicLTUser) PostedAck(postId string, status string, reason string, postedData string) error {
	panic("unexpected call: PostedAck")
}

func (panicLTUser) GetConfig() error { panic("unexpected call: GetConfig") }

func (panicLTUser) GetClientConfig() error { panic("unexpected call: GetClientConfig") }

func (panicLTUser) FetchStaticAssets() error { panic("unexpected call: FetchStaticAssets") }

func (panicLTUser) GetClientLicense() error { panic("unexpected call: GetClientLicense") }

func (panicLTUser) SignUp(email, username, password string) error { panic("unexpected call: SignUp") }

func (panicLTUser) Login() error { panic("unexpected call: Login") }

func (panicLTUser) Logout() error { panic("unexpected call: Logout") }

func (panicLTUser) GetMe() (string, error) { panic("unexpected call: GetMe") }

func (panicLTUser) GetPreferences() error { panic("unexpected call: GetPreferences") }

func (panicLTUser) UpdatePreferences(pref model.Preferences) error {
	panic("unexpected call: UpdatePreferences")
}

func (panicLTUser) CreateUser(user *model.User) (string, error) { panic("unexpected call: CreateUser") }

func (panicLTUser) UpdateUser(user *model.User) error { panic("unexpected call: UpdateUser") }

func (panicLTUser) UpdateUserRoles(userId, roles string) error {
	panic("unexpected call: UpdateUserRoles")
}

func (panicLTUser) PatchUser(userId string, patch *model.UserPatch) error {
	panic("unexpected call: PatchUser")
}

func (panicLTUser) GetUserStatus() error { panic("unexpected call: GetUserStatus") }

func (panicLTUser) GetUsersStatusesByIds(userIds []string) error {
	panic("unexpected call: GetUsersStatusesByIds")
}

func (panicLTUser) GetUsersInChannel(channelId string, page, perPage int) ([]string, error) {
	panic("unexpected call: GetUsersInChannel")
}

func (panicLTUser) GetUsers(page, perPage int) ([]string, error) { panic("unexpected call: GetUsers") }

func (panicLTUser) GetUsersNotInChannel(teamId, channelId string, page, perPage int) ([]string, error) {
	panic("unexpected call: GetUsersNotInChannel")
}

func (panicLTUser) GetUsersForReporting(options *model.UserReportOptions) ([]*model.UserReport, error) {
	panic("unexpected call: GetUsersForReporting")
}

func (panicLTUser) SetProfileImage(data []byte) error { panic("unexpected call: SetProfileImage") }

func (panicLTUser) GetProfileImageForUser(userId string, lastPictureUpdate int) error {
	panic("unexpected call: GetProfileImageForUser")
}

func (panicLTUser) SearchUsers(search *model.UserSearch) ([]*model.User, error) {
	panic("unexpected call: SearchUsers")
}

func (panicLTUser) AutocompleteUsersInChannel(teamId, channelId, username string, limit int) (map[string]bool, error) {
	panic("unexpected call: AutocompleteUsersInChannel")
}

func (panicLTUser) AutocompleteUsersInTeam(teamId, username string, limit int) (map[string]bool, error) {
	panic("unexpected call: AutocompleteUsersInTeam")
}

func (panicLTUser) GetDrafts(teamId string) error { panic("unexpected call: GetDrafts") }

func (panicLTUser) UpsertDraft(teamId string, draft *model.Draft) error {
	panic("unexpected call: UpsertDraft")
}

func (panicLTUser) DeleteDraft(channelId, rootId string) error { panic("unexpected call: DeleteDraft") }

func (panicLTUser) PatchPost(postId string, patch *model.PostPatch) (string, error) {
	panic("unexpected call: PatchPost")
}

func (panicLTUser) DeletePost(postId string) error { panic("unexpected call: DeletePost") }

func (panicLTUser) SearchPosts(teamId, terms string, isOrSearch bool) (*model.PostList, error) {
	panic("unexpected call: SearchPosts")
}

func (panicLTUser) GetPostsForChannel(channelId string, page, perPage int, collapsedThreads bool) error {
	panic("unexpected call: GetPostsForChannel")
}

func (panicLTUser) GetPostsBefore(channelId, postId string, page, perPage int, collapsedThreads bool) ([]string, error) {
	panic("unexpected call: GetPostsBefore")
}

func (panicLTUser) GetPostsAfter(channelId, postId string, page, perPage int, collapsedThreads bool) error {
	panic("unexpected call: GetPostsAfter")
}

func (panicLTUser) GetPostsSince(channelId string, time int64, collapsedThreads bool) ([]string, error) {
	panic("unexpected call: GetPostsSince")
}

func (panicLTUser) GetPinnedPosts(channelId string) (*model.PostList, error) {
	panic("unexpected call: GetPinnedPosts")
}

func (panicLTUser) GetPostsAroundLastUnread(channelId string, limitBefore, limitAfter int, collapsedThreads bool) ([]string, error) {
	panic("unexpected call: GetPostsAroundLastUnread")
}

func (panicLTUser) UploadFile(data []byte, channelId, filename string) (*model.FileUploadResponse, error) {
	panic("unexpected call: UploadFile")
}

func (panicLTUser) GetFileInfosForPost(postId string) ([]*model.FileInfo, error) {
	panic("unexpected call: GetFileInfosForPost")
}

func (panicLTUser) GetFileThumbnail(fileId string) error { panic("unexpected call: GetFileThumbnail") }

func (panicLTUser) GetFilePreview(fileId string) error { panic("unexpected call: GetFilePreview") }

func (panicLTUser) CreateChannel(channel *model.Channel) (string, error) {
	panic("unexpected call: CreateChannel")
}

func (panicLTUser) CreateGroupChannel(memberIds []string) (string, error) {
	panic("unexpected call: CreateGroupChannel")
}

func (panicLTUser) GetChannel(channelId string) error { panic("unexpected call: GetChannel") }

func (panicLTUser) GetChannelsForTeam(teamId string, includeDeleted bool) error {
	panic("unexpected call: GetChannelsForTeam")
}

func (panicLTUser) GetPublicChannelsForTeam(teamId string, page, perPage int) error {
	panic("unexpected call: GetPublicChannelsForTeam")
}

func (panicLTUser) SearchChannelsForTeam(teamId string, search *model.ChannelSearch) ([]*model.Channel, error) {
	panic("unexpected call: SearchChannelsForTeam")
}

func (panicLTUser) SearchChannels(search *model.ChannelSearch) (model.ChannelListWithTeamData, error) {
	panic("unexpected call: SearchChannels")
}

func (panicLTUser) SearchGroupChannels(search *model.ChannelSearch) ([]*model.Channel, error) {
	panic("unexpected call: SearchGroupChannels")
}

func (panicLTUser) RemoveUserFromChannel(channelId, userId string) error {
	panic("unexpected call: RemoveUserFromChannel")
}

func (panicLTUser) ViewChannel(view *model.ChannelView) (*model.ChannelViewResponse, error) {
	panic("unexpected call: ViewChannel")
}

func (panicLTUser) GetChannelUnread(channelId string) (*model.ChannelUnread, error) {
	panic("unexpected call: GetChannelUnread")
}

func (panicLTUser) GetChannelMembers(channelId string, page, perPage int) error {
	panic("unexpected call: GetChannelMembers")
}

func (panicLTUser) GetAllChannelMembersForUser(userId string) error {
	panic("unexpected call: GetAllChannelMembersForUser")
}

func (panicLTUser) GetChannelMembersForUser(userId, teamId string) error {
	panic("unexpected call: GetChannelMembersForUser")
}

func (panicLTUser) GetChannelMember(channelId string, userId string) error {
	panic("unexpected call: GetChannelMember")
}

func (panicLTUser) GetChannelStats(channelId string, excludeFileCount bool) error {
	panic("unexpected call: GetChannelStats")
}

func (panicLTUser) AddChannelMember(channelId, userId string) error {
	panic("unexpected call: AddChannelMember")
}

func (panicLTUser) GetChannelsForTeamForUser(teamId, userId string, includeDeleted bool) ([]*model.Channel, error) {
	panic("unexpected call: GetChannelsForTeamForUser")
}

func (panicLTUser) AutocompleteChannelsForTeam(teamId, name string) error {
	panic("unexpected call: AutocompleteChannelsForTeam")
}

func (panicLTUser) AutocompleteChannelsForTeamForSearch(teamId, name string) (map[string]bool, error) {
	panic("unexpected call: AutocompleteChannelsForTeamForSearch")
}

func (panicLTUser) GetChannelsForUser(userID string) ([]*model.Channel, error) {
	panic("unexpected call: GetChannelsForUser")
}

func (panicLTUser) GetAllTeams(page, perPage int) ([]string, error) {
	panic("unexpected call: GetAllTeams")
}

func (panicLTUser) CreateTeam(team *model.Team) (string, error) { panic("unexpected call: CreateTeam") }

func (panicLTUser) GetTeam(teamId string) error { panic("unexpected call: GetTeam") }

func (panicLTUser) GetTeamsForUser(userId string) ([]string, error) {
	panic("unexpected call: GetTeamsForUser")
}

func (panicLTUser) AddTeamMember(teamId, userId string) error {
	panic("unexpected call: AddTeamMember")
}

func (panicLTUser) RemoveTeamMember(teamId, userId string) error {
	panic("unexpected call: RemoveTeamMember")
}

func (panicLTUser) GetTeamMembers(teamId string, page, perPage int) error {
	panic("unexpected call: GetTeamMembers")
}

func (panicLTUser) GetTeamMember(teamId, userId string) error {
	panic("unexpected call: GetTeamMember")
}

func (panicLTUser) GetTeamMembersForUser(userId string) error {
	panic("unexpected call: GetTeamMembersForUser")
}

func (panicLTUser) GetTeamStats(teamId string) error { panic("unexpected call: GetTeamStats") }

func (panicLTUser) GetTeamsUnread(teamIdToExclude string, includeCollapsedThreads bool) ([]*model.TeamUnread, error) {
	panic("unexpected call: GetTeamsUnread")
}

func (panicLTUser) AddTeamMemberFromInvite(token, inviteId string) error {
	panic("unexpected call: AddTeamMemberFromInvite")
}

func (panicLTUser) UpdateTeam(team *model.Team) error { panic("unexpected call: UpdateTeam") }

func (panicLTUser) GetRolesByNames(roleNames []string) ([]string, error) {
	panic("unexpected call: GetRolesByNames")
}

func (panicLTUser) GetEmojiList(page, perPage int) error { panic("unexpected call: GetEmojiList") }

func (panicLTUser) GetEmojiImage(emojiId string) error { panic("unexpected call: GetEmojiImage") }

func (panicLTUser) UploadEmoji(emoji *model.Emoji, image []byte, filename string) error {
	panic("unexpected call: UploadEmoji")
}

func (panicLTUser) SaveReaction(reaction *model.Reaction) error {
	panic("unexpected call: SaveReaction")
}

func (panicLTUser) DeleteReaction(reaction *model.Reaction) error {
	panic("unexpected call: DeleteReaction")
}

func (panicLTUser) GetWebappPlugins() error { panic("unexpected call: GetWebappPlugins") }

func (panicLTUser) IsSysAdmin() (bool, error) { panic("unexpected call: IsSysAdmin") }

func (panicLTUser) IsTeamAdmin() (bool, error) { panic("unexpected call: IsTeamAdmin") }

func (panicLTUser) SetCurrentTeam(team *model.Team) error { panic("unexpected call: SetCurrentTeam") }

func (panicLTUser) SetCurrentChannel(channel *model.Channel) error {
	panic("unexpected call: SetCurrentChannel")
}

func (panicLTUser) GetLogs(page, perPage int) error { panic("unexpected call: GetLogs") }

func (panicLTUser) GetAnalytics() error { panic("unexpected call: GetAnalytics") }

func (panicLTUser) GetClusterStatus() error { panic("unexpected call: GetClusterStatus") }

func (panicLTUser) GetPluginStatuses() error { panic("unexpected call: GetPluginStatuses") }

func (panicLTUser) UpdateConfig(cfg *model.Config) error { panic("unexpected call: UpdateConfig") }

func (panicLTUser) MessageExport() error { panic("unexpected call: MessageExport") }

func (panicLTUser) GetUserThreads(teamId string, options *model.GetUserThreadsOpts) ([]*store.ThreadResponseWrapped, error) {
	panic("unexpected call: GetUserThreads")
}

func (panicLTUser) UpdateThreadFollow(teamId, threadId string, state bool) error {
	panic("unexpected call: UpdateThreadFollow")
}

func (panicLTUser) UpdateThreadLastUpdateAt(threadId string, lastUpdateAt int64) error {
	panic("unexpected call: UpdateThreadLastUpdateAt")
}

func (panicLTUser) GetPostThreadWithOpts(threadId, etag string, opts model.GetPostsOptions) ([]string, bool, error) {
	panic("unexpected call: GetPostThreadWithOpts")
}

func (panicLTUser) MarkAllThreadsInTeamAsRead(teamId string) error {
	panic("unexpected call: MarkAllThreadsInTeamAsRead")
}

func (panicLTUser) UpdateThreadRead(teamId, threadId string, timestamp int64) error {
	panic("unexpected call: UpdateThreadRead")
}

func (panicLTUser) GetSidebarCategories(userID, teamID string) error {
	panic("unexpected call: GetSidebarCategories")
}

func (panicLTUser) CreateSidebarCategory(userID, teamID string, category *model.SidebarCategoryWithChannels) (*model.SidebarCategoryWithChannels, error) {
	panic("unexpected call: CreateSidebarCategory")
}

func (panicLTUser) UpdateSidebarCategory(userID, teamID string, categories []*model.SidebarCategoryWithChannels) error {
	panic("unexpected call: UpdateSidebarCategory")
}

func (panicLTUser) UpdateCustomStatus(userID string, status *model.CustomStatus) error {
	panic("unexpected call: UpdateCustomStatus")
}

func (panicLTUser) RemoveCustomStatus(userID string) error {
	panic("unexpected call: RemoveCustomStatus")
}

func (panicLTUser) CreatePostReminder(userID, postID string, targetTime int64) error {
	panic("unexpected call: CreatePostReminder")
}

func (panicLTUser) AckToPost(userID, postID string) error { panic("unexpected call: AckToPost") }

func (panicLTUser) GetInitialDataGQL() error { panic("unexpected call: GetInitialDataGQL") }

func (panicLTUser) GetChannelsAndChannelMembersGQL(teamID string, includeDeleted bool, channelsCursor, channelMembersCursor string) (string, string, error) {
	panic("unexpected call: GetChannelsAndChannelMembersGQL")
}

func (panicLTUser) ObserveClientMetric(t model.MetricType, v float64) error {
	panic("unexpected call: ObserveClientMetric")
}

func (panicLTUser) SubmitPerformanceReport() error { panic("unexpected call: SubmitPerformanceReport") }

func (panicLTUser) GetChannelBookmarks(channelId string, since int64) error {
	panic("unexpected call: GetChannelBookmarks")
}

func (panicLTUser) AddChannelBookmark(bookmark *model.ChannelBookmark) error {
	panic("unexpected call: AddChannelBookmark")
}

func (panicLTUser) UpdateChannelBookmark(bookmark *model.ChannelBookmarkWithFileInfo) error {
	panic("unexpected call: UpdateChannelBookmark")
}

func (panicLTUser) DeleteChannelBookmark(channelId, bookmarkId string) error {
	panic("unexpected call: DeleteChannelBookmark")
}

func (panicLTUser) UpdateChannelBookmarkSortOrder(channelId, bookmarkId string, sortOrder int64) error {
	panic("unexpected call: UpdateChannelBookmarkSortOrder")
}

func (panicLTUser) CreateScheduledPost(teamId string, scheduledPost *model.ScheduledPost) error {
	panic("unexpected call: CreateScheduledPost")
}

func (panicLTUser) UpdateScheduledPost(teamId string, scheduledPost *model.ScheduledPost) error {
	panic("unexpected call: UpdateScheduledPost")
}

func (panicLTUser) DeleteScheduledPost(scheduledPost *model.ScheduledPost) error {
	panic("unexpected call: DeleteScheduledPost")
}

func (panicLTUser) GetTeamScheduledPosts(teamID string) error {
	panic("unexpected call: GetTeamScheduledPosts")
}

func (panicLTUser) GetCPAValues(userId string) (map[string]json.RawMessage, error) {
	panic("unexpected call: GetCPAValues")
}

func (panicLTUser) PatchCPAValues(values map[string]json.RawMessage) error {
	panic("unexpected call: PatchCPAValues")
}

func (panicLTUser) GetCPAFields() error { panic("unexpected call: GetCPAFields") }

func (panicLTUser) CreateCPAField(field *model.PropertyField) (*model.PropertyField, error) {
	panic("unexpected call: CreateCPAField")
}

// ltUserTest embeds simulTestUser (real simulAPI) and panicLTUser for all other User methods.
type ltUserTest struct {
	*simulTestUser
	panicLTUser
}

var _ user.User = (*ltUserTest)(nil)
