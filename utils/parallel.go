// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package utils

import (
	"fmt"
	"sync"
)

// ParallelLimit bounds how many tasks RunParallel executes at once for a
// single operation. Concurrent operations each get their own budget.
const ParallelLimit = 32

// ParallelResult holds one task's outcome, stored at the task's input index.
type ParallelResult[T any] struct {
	Value T
	Err   error
}

// RunParallel calls run once per index in [0, count), with at most
// ParallelLimit calls in flight, and returns every outcome at its own index.
// A failing task never cancels its siblings: RunParallel returns only after
// all tasks finish, leaving result merging and error policy to the caller.
// A panicking task is reported as a failed task rather than crashing the
// process, which also keeps the worker pool from stalling.
func RunParallel[T any](count int, run func(index int) (T, error)) []ParallelResult[T] {
	if count <= 0 {
		return nil
	}

	results := make([]ParallelResult[T], count)
	indexes := make(chan int)

	var wg sync.WaitGroup
	for range min(count, ParallelLimit) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indexes {
				value, err := runGuarded(index, run)
				results[index] = ParallelResult[T]{Value: value, Err: err}
			}
		}()
	}

	for index := range count {
		indexes <- index
	}
	close(indexes)
	wg.Wait()

	return results
}

func runGuarded[T any](index int, run func(index int) (T, error)) (value T, err error) {
	defer func() {
		if r := recover(); r != nil {
			var zero T
			value = zero
			err = fmt.Errorf("panic in parallel task %d: %v", index, r)
		}
	}()
	return run(index)
}
