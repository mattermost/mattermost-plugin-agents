// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestPostCache_addPost(t *testing.T) {
	t.Run("adds post to cache", func(t *testing.T) {
		cache := newPostCache()
		post := &model.Post{Id: "post1"}

		cache.addPost(post)

		assert.Len(t, cache.cache, 1)
		assert.Equal(t, post, cache.cache["post1"].post)
	})

	t.Run("ignores post with empty ID", func(t *testing.T) {
		cache := newPostCache()
		post := &model.Post{Id: ""}

		cache.addPost(post)

		assert.Len(t, cache.cache, 0)
	})

	t.Run("overwrites existing post", func(t *testing.T) {
		cache := newPostCache()
		post1 := &model.Post{Id: "post1", Message: "first"}
		post2 := &model.Post{Id: "post1", Message: "second"}

		cache.addPost(post1)
		cache.addPost(post2)

		assert.Len(t, cache.cache, 1)
		assert.Equal(t, "second", cache.cache["post1"].post.Message)
	})
}

func TestPostCache_getPost(t *testing.T) {
	t.Run("returns cached post", func(t *testing.T) {
		cache := newPostCache()
		post := &model.Post{Id: "post1", Message: "test"}
		cache.addPost(post)

		api := &plugintest.API{}
		result, err := cache.getPost(api, "post1")

		assert.NoError(t, err)
		assert.Equal(t, post, result)
		api.AssertNotCalled(t, "GetPost")
	})

	t.Run("fetches post from API if not cached", func(t *testing.T) {
		cache := newPostCache()
		post := &model.Post{Id: "post1", Message: "test"}

		api := &plugintest.API{}
		api.On("GetPost", "post1").Return(post, nil)

		result, err := cache.getPost(api, "post1")

		assert.NoError(t, err)
		assert.Equal(t, post, result)
		assert.Len(t, cache.cache, 1)
		assert.Equal(t, post, cache.cache["post1"].post)
	})

	t.Run("returns error for empty post ID", func(t *testing.T) {
		cache := newPostCache()
		api := &plugintest.API{}

		result, err := cache.getPost(api, "")

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "post ID cannot be empty")
	})

	t.Run("returns error when API call fails", func(t *testing.T) {
		cache := newPostCache()

		api := &plugintest.API{}
		apiErr := &model.AppError{Message: "not found"}
		api.On("GetPost", "post1").Return(nil, apiErr)

		result, err := cache.getPost(api, "post1")

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, apiErr, err)
	})

	t.Run("returns error when API returns nil post", func(t *testing.T) {
		cache := newPostCache()

		api := &plugintest.API{}
		api.On("GetPost", "post1").Return(nil, nil)

		result, err := cache.getPost(api, "post1")

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "post not found")
	})

	t.Run("caches post fetched from API", func(t *testing.T) {
		cache := newPostCache()
		post := &model.Post{Id: "post1", Message: "test"}

		api := &plugintest.API{}
		api.On("GetPost", "post1").Return(post, nil).Once()

		// First call should fetch from API
		result1, err := cache.getPost(api, "post1")
		assert.NoError(t, err)
		assert.Equal(t, post, result1)

		// Second call should use cache
		result2, err := cache.getPost(api, "post1")
		assert.NoError(t, err)
		assert.Equal(t, post, result2)

		// Verify API was only called once
		api.AssertExpectations(t)
	})

	t.Run("singleflight deduplicates concurrent requests", func(t *testing.T) {
		cache := newPostCache()
		post := &model.Post{Id: "post1", Message: "test"}

		api := &plugintest.API{}
		// API should be called exactly once despite multiple concurrent requests
		api.On("GetPost", "post1").Return(post, nil).Once()

		// Launch 10 concurrent goroutines requesting the same post
		const numGoroutines = 10
		resultCh := make(chan *model.Post, numGoroutines)
		errCh := make(chan error, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				result, err := cache.getPost(api, "post1")
				resultCh <- result
				errCh <- err
			}()
		}

		// Collect all results
		for i := 0; i < numGoroutines; i++ {
			result := <-resultCh
			err := <-errCh
			assert.NoError(t, err)
			assert.Equal(t, post, result)
		}

		// Verify API was called exactly once (singleflight worked)
		api.AssertExpectations(t)
	})
}

func TestPostCache_cleanup(t *testing.T) {
	t.Run("removes expired entries", func(t *testing.T) {
		cache := newPostCache()
		cache.cacheTTL = 100 * time.Millisecond

		cache.addPost(&model.Post{Id: "post1"})
		cache.addPost(&model.Post{Id: "post2"})

		assert.Len(t, cache.cache, 2)

		time.Sleep(150 * time.Millisecond)
		cache.cleanup()

		assert.Len(t, cache.cache, 0)
	})

	t.Run("preserves non-expired entries", func(t *testing.T) {
		cache := newPostCache()
		cache.cacheTTL = 100 * time.Millisecond

		// Add expired entry
		cache.addPost(&model.Post{Id: "expired"})
		time.Sleep(150 * time.Millisecond)

		// Add fresh entry
		cache.addPost(&model.Post{Id: "fresh"})

		assert.Len(t, cache.cache, 2)

		cache.cleanup()

		assert.Len(t, cache.cache, 1)
		assert.Contains(t, cache.cache, "fresh")
		assert.NotContains(t, cache.cache, "expired")
	})

	t.Run("handles empty cache", func(t *testing.T) {
		cache := newPostCache()

		assert.NotPanics(t, func() {
			cache.cleanup()
		})
	})
}

func TestPlugin_initializePostCache(t *testing.T) {
	t.Run("initializes cache and starts cleanup goroutine", func(t *testing.T) {
		p := &Plugin{}

		p.initializePostCache()

		assert.NotNil(t, p.postCache)
		assert.NotNil(t, p.postCacheCleanupTicker)
		assert.NotNil(t, p.postCacheCleanupDone)

		// Cleanup
		p.deinitializePostCache()
	})

	t.Run("cleanup goroutine removes expired entries", func(t *testing.T) {
		p := &Plugin{}
		p.initializePostCache()

		// Set a short TTL for testing
		p.postCache.cacheTTL = 50 * time.Millisecond
		p.postCache.addPost(&model.Post{Id: "test"})

		assert.Len(t, p.postCache.cache, 1)

		// Wait for expiry
		time.Sleep(100 * time.Millisecond)

		// Trigger cleanup by sending tick
		p.postCache.cleanup()

		assert.Len(t, p.postCache.cache, 0)

		// Cleanup
		p.deinitializePostCache()
	})
}

func TestPlugin_deinitializePostCache(t *testing.T) {
	t.Run("stops ticker and closes done channel", func(t *testing.T) {
		p := &Plugin{}
		p.initializePostCache()

		assert.NotPanics(t, func() {
			p.deinitializePostCache()
		})
	})

	t.Run("handles nil ticker and channel", func(t *testing.T) {
		p := &Plugin{}

		assert.NotPanics(t, func() {
			p.deinitializePostCache()
		})
	})
}

func TestPlugin_shouldBlockBotReplyNotification(t *testing.T) {
	t.Run("does not block non-threaded posts", func(t *testing.T) {
		p := &Plugin{}
		p.initializePostCache()
		defer p.deinitializePostCache()

		api := &plugintest.API{}
		p.API = api

		blocked := p.shouldBlockBotReplyNotification("post1", "", "user1")

		assert.False(t, blocked)
	})

	t.Run("returns false when postCache is nil", func(t *testing.T) {
		p := &Plugin{}

		api := &plugintest.API{}
		p.API = api

		blocked := p.shouldBlockBotReplyNotification("post1", "root1", "user1")

		assert.False(t, blocked)
	})

	t.Run("does not block non-bot posts", func(t *testing.T) {
		p := &Plugin{}
		p.initializePostCache()
		defer p.deinitializePostCache()

		api := &plugintest.API{}
		post := &model.Post{
			Id:     "post1",
			RootId: "root1",
			Props:  map[string]any{},
		}
		api.On("GetPost", "post1").Return(post, nil)
		p.API = api

		blocked := p.shouldBlockBotReplyNotification("post1", "root1", "user1")

		assert.False(t, blocked)
	})

	t.Run("does not block when parent post author is not recipient", func(t *testing.T) {
		p := &Plugin{}
		p.initializePostCache()
		defer p.deinitializePostCache()

		api := &plugintest.API{}
		post := &model.Post{
			Id:     "post1",
			RootId: "root1",
			Props:  map[string]any{"from_bot": "true"},
		}
		parentPost := &model.Post{
			Id:       "root1",
			UserId:   "other_user",
			CreateAt: model.GetMillis(),
		}
		api.On("GetPost", "post1").Return(post, nil)
		api.On("GetPost", "root1").Return(parentPost, nil)
		p.API = api

		blocked := p.shouldBlockBotReplyNotification("post1", "root1", "user1")

		assert.False(t, blocked)
	})

	t.Run("blocks bot reply within debounce window", func(t *testing.T) {
		p := &Plugin{}
		p.initializePostCache()
		defer p.deinitializePostCache()

		api := &plugintest.API{}
		post := &model.Post{
			Id:     "post1",
			RootId: "root1",
			Props:  map[string]any{"from_bot": "true"},
		}
		parentPost := &model.Post{
			Id:       "root1",
			UserId:   "user1",
			CreateAt: model.GetMillis(), // Just created
		}
		api.On("GetPost", "post1").Return(post, nil)
		api.On("GetPost", "root1").Return(parentPost, nil)
		p.API = api

		blocked := p.shouldBlockBotReplyNotification("post1", "root1", "user1")

		assert.True(t, blocked)
	})

	t.Run("does not block bot reply outside debounce window", func(t *testing.T) {
		p := &Plugin{}
		p.initializePostCache()
		defer p.deinitializePostCache()

		api := &plugintest.API{}
		post := &model.Post{
			Id:     "post1",
			RootId: "root1",
			Props:  map[string]any{"from_bot": "true"},
		}
		parentPost := &model.Post{
			Id:       "root1",
			UserId:   "user1",
			CreateAt: model.GetMillis() - 2000, // Created 2 seconds ago
		}
		api.On("GetPost", "post1").Return(post, nil)
		api.On("GetPost", "root1").Return(parentPost, nil)
		p.API = api

		blocked := p.shouldBlockBotReplyNotification("post1", "root1", "user1")

		assert.False(t, blocked)
	})

	t.Run("uses cached posts to avoid API calls", func(t *testing.T) {
		p := &Plugin{}
		p.initializePostCache()
		defer p.deinitializePostCache()

		api := &plugintest.API{}
		post := &model.Post{
			Id:     "post1",
			RootId: "root1",
			Props:  map[string]any{"from_bot": "true"},
		}
		parentPost := &model.Post{
			Id:       "root1",
			UserId:   "user1",
			CreateAt: model.GetMillis(),
		}

		// Add posts to cache
		p.postCache.addPost(post)
		p.postCache.addPost(parentPost)
		p.API = api

		// Should not call API since posts are cached
		blocked := p.shouldBlockBotReplyNotification("post1", "root1", "user1")

		assert.True(t, blocked)
		api.AssertNotCalled(t, "GetPost", mock.Anything)
	})
}
