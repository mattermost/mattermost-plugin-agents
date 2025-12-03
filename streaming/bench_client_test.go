// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package streaming

import (
	"io"
	"net/http"
	"sort"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// benchmarkClient implements mmapi.Client for benchmarks with minimal overhead.
// It tracks timing metrics without performing actual operations.
type benchmarkClient struct {
	wsEventCount    int64
	updatePostCount int64
	createPostCount int64
	lastWSTime      time.Time
	firstWSTime     time.Time
	wsLatencies     []time.Duration
}

func newBenchmarkClient() *benchmarkClient {
	return &benchmarkClient{
		wsLatencies: make([]time.Duration, 0, 1000),
	}
}

func (c *benchmarkClient) PublishWebSocketEvent(_ string, _ map[string]interface{}, _ *model.WebsocketBroadcast) {
	now := time.Now()
	if c.firstWSTime.IsZero() {
		c.firstWSTime = now
	}
	if !c.lastWSTime.IsZero() {
		c.wsLatencies = append(c.wsLatencies, now.Sub(c.lastWSTime))
	}
	c.lastWSTime = now
	atomic.AddInt64(&c.wsEventCount, 1)
}

func (c *benchmarkClient) UpdatePost(_ *model.Post) error {
	atomic.AddInt64(&c.updatePostCount, 1)
	return nil
}

func (c *benchmarkClient) CreatePost(post *model.Post) error {
	atomic.AddInt64(&c.createPostCount, 1)
	// Assign a post ID like the real implementation would
	if post.Id == "" {
		post.Id = "bench-post-id"
	}
	return nil
}

func (c *benchmarkClient) DM(_, _ string, post *model.Post) error {
	if post.Id == "" {
		post.Id = "bench-dm-post-id"
	}
	return nil
}

func (c *benchmarkClient) GetUser(userID string) (*model.User, error) {
	return &model.User{Id: userID, Locale: "en"}, nil
}

func (c *benchmarkClient) GetChannel(channelID string) (*model.Channel, error) {
	return &model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, nil
}

func (c *benchmarkClient) GetConfig() *model.Config {
	locale := "en"
	return &model.Config{
		LocalizationSettings: model.LocalizationSettings{
			DefaultServerLocale: &locale,
		},
	}
}

func (c *benchmarkClient) LogError(_ string, _ ...interface{}) {}
func (c *benchmarkClient) LogWarn(_ string, _ ...interface{})  {}
func (c *benchmarkClient) LogDebug(_ string, _ ...interface{}) {}

// Remaining interface methods as no-ops

func (c *benchmarkClient) GetPost(_ string) (*model.Post, error) {
	return &model.Post{}, nil
}

func (c *benchmarkClient) AddReaction(_ *model.Reaction) error {
	return nil
}

func (c *benchmarkClient) GetPostThread(_ string) (*model.PostList, error) {
	return model.NewPostList(), nil
}

func (c *benchmarkClient) GetPostsSince(_ string, _ int64) (*model.PostList, error) {
	return model.NewPostList(), nil
}

func (c *benchmarkClient) GetPostsBefore(_, _ string, _, _ int) (*model.PostList, error) {
	return model.NewPostList(), nil
}

func (c *benchmarkClient) GetDirectChannel(_, _ string) (*model.Channel, error) {
	return &model.Channel{Type: model.ChannelTypeDirect}, nil
}

func (c *benchmarkClient) KVGet(_ string, _ interface{}) error {
	return nil
}

func (c *benchmarkClient) KVSet(_ string, _ interface{}) error {
	return nil
}

func (c *benchmarkClient) KVDelete(_ string) error {
	return nil
}

func (c *benchmarkClient) GetUserByUsername(_ string) (*model.User, error) {
	return &model.User{}, nil
}

func (c *benchmarkClient) GetUserStatus(_ string) (*model.Status, error) {
	return &model.Status{}, nil
}

func (c *benchmarkClient) HasPermissionTo(_ string, _ *model.Permission) bool {
	return true
}

func (c *benchmarkClient) GetPluginStatus(_ string) (*model.PluginStatus, error) {
	return &model.PluginStatus{}, nil
}

func (c *benchmarkClient) PluginHTTP(_ *http.Request) *http.Response {
	return nil
}

func (c *benchmarkClient) GetChannelByName(_, _ string, _ bool) (*model.Channel, error) {
	return &model.Channel{}, nil
}

func (c *benchmarkClient) HasPermissionToChannel(_, _ string, _ *model.Permission) bool {
	return true
}

func (c *benchmarkClient) GetFileInfo(_ string) (*model.FileInfo, error) {
	return &model.FileInfo{}, nil
}

func (c *benchmarkClient) GetFile(_ string) (io.ReadCloser, error) {
	return io.NopCloser(nil), nil
}

func (c *benchmarkClient) SendEphemeralPost(_ string, _ *model.Post) {}

// Metrics helper methods

func (c *benchmarkClient) GetWSEventCount() int64 {
	return atomic.LoadInt64(&c.wsEventCount)
}

func (c *benchmarkClient) GetUpdatePostCount() int64 {
	return atomic.LoadInt64(&c.updatePostCount)
}

func (c *benchmarkClient) GetCreatePostCount() int64 {
	return atomic.LoadInt64(&c.createPostCount)
}

func (c *benchmarkClient) GetFirstWSTime() time.Time {
	return c.firstWSTime
}

func (c *benchmarkClient) GetLatencyP50() time.Duration {
	return percentile(c.wsLatencies, 50)
}

func (c *benchmarkClient) GetLatencyP95() time.Duration {
	return percentile(c.wsLatencies, 95)
}

func (c *benchmarkClient) GetLatencyP99() time.Duration {
	return percentile(c.wsLatencies, 99)
}

func percentile(latencies []time.Duration, p int) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (p * len(sorted)) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
