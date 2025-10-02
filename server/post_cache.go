// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"golang.org/x/sync/singleflight"
)

const postCacheTTL = 5 * time.Minute

func (p *Plugin) initializePostCache() {
	p.postCache = newPostCache()
	p.postCacheCleanupDone = make(chan struct{})
	p.postCacheCleanupTicker = time.NewTicker(1 * time.Minute)

	// Start cache cleanup goroutine
	go func() {
		for {
			select {
			case <-p.postCacheCleanupTicker.C:
				p.postCache.cleanup()
			case <-p.postCacheCleanupDone:
				return
			}
		}
	}()
}

func (p *Plugin) deinitializePostCache() {
	if p.postCacheCleanupTicker != nil {
		p.postCacheCleanupTicker.Stop()
	}
	if p.postCacheCleanupDone != nil {
		close(p.postCacheCleanupDone)
	}
}

type postCache struct {
	cache     map[string]cachedPost
	cacheTTL  time.Duration
	cacheLock sync.Mutex
	group     singleflight.Group
}

type cachedPost struct {
	post      *model.Post
	timestamp time.Time
}

func newPostCache() *postCache {
	return &postCache{
		cache:    make(map[string]cachedPost),
		cacheTTL: postCacheTTL,
	}
}

func (pc *postCache) addPost(post *model.Post) {
	if post.Id == "" {
		return
	}

	pc.cacheLock.Lock()
	defer pc.cacheLock.Unlock()

	pc.cache[post.Id] = cachedPost{
		post:      post,
		timestamp: time.Now(),
	}
}

func (pc *postCache) getPost(api plugin.API, postID string) (*model.Post, error) {
	if postID == "" {
		return nil, fmt.Errorf("post ID cannot be empty")
	}

	// Check cache first (with lock)
	pc.cacheLock.Lock()
	if cached, ok := pc.cache[postID]; ok {
		pc.cacheLock.Unlock()
		return cached.post, nil
	}
	pc.cacheLock.Unlock()

	// Use singleflight to deduplicate concurrent requests for the same post
	v, err, _ := pc.group.Do(postID, func() (interface{}, error) {
		// Double-check cache in case another goroutine already fetched it
		pc.cacheLock.Lock()
		if cached, ok := pc.cache[postID]; ok {
			pc.cacheLock.Unlock()
			return cached.post, nil
		}
		pc.cacheLock.Unlock()

		post, appErr := api.GetPost(postID)
		if appErr != nil {
			return nil, appErr
		}

		if post == nil {
			return nil, fmt.Errorf("post not found")
		}

		// Cache the result
		pc.cacheLock.Lock()
		pc.cache[postID] = cachedPost{
			post:      post,
			timestamp: time.Now(),
		}
		pc.cacheLock.Unlock()

		return post, nil
	})

	if err != nil {
		return nil, err
	}
	return v.(*model.Post), nil
}

func (pc *postCache) cleanup() {
	pc.cacheLock.Lock()
	defer pc.cacheLock.Unlock()

	now := time.Now()
	for id, post := range pc.cache {
		if now.Sub(post.timestamp) > pc.cacheTTL {
			delete(pc.cache, id)
		}
	}
}
