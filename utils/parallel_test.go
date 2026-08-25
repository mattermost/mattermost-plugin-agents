// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package utils

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunParallelReturnsResultsInInputOrder(t *testing.T) {
	testCases := []struct {
		name  string
		count int
	}{
		{name: "zero tasks", count: 0},
		{name: "single task", count: 1},
		{name: "fewer than the limit", count: ParallelLimit - 1},
		{name: "exactly the limit", count: ParallelLimit},
		{name: "more than the limit", count: ParallelLimit*3 + 7},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Later indexes finish first so completion order cannot be
			// mistaken for input order.
			results := RunParallel(tc.count, func(index int) (int, error) {
				time.Sleep(time.Duration(tc.count-index) * time.Millisecond)
				return index * 10, nil
			})

			require.Len(t, results, tc.count)
			for index, result := range results {
				require.NoError(t, result.Err)
				require.Equal(t, index*10, result.Value)
			}
		})
	}
}

func TestRunParallelRunsEveryTaskDespiteFailures(t *testing.T) {
	const count = 50
	var executed atomic.Int64

	results := RunParallel(count, func(index int) (string, error) {
		executed.Add(1)
		if index%2 == 0 {
			return "", fmt.Errorf("task %d failed", index)
		}
		return fmt.Sprintf("ok-%d", index), nil
	})

	require.Equal(t, int64(count), executed.Load())
	require.Len(t, results, count)
	for index, result := range results {
		if index%2 == 0 {
			require.EqualError(t, result.Err, fmt.Sprintf("task %d failed", index))
			require.Empty(t, result.Value)
			continue
		}
		require.NoError(t, result.Err)
		require.Equal(t, fmt.Sprintf("ok-%d", index), result.Value)
	}
}

func TestRunParallelNeverExceedsParallelLimit(t *testing.T) {
	const count = ParallelLimit * 4

	var mu sync.Mutex
	inFlight := 0
	peak := 0

	results := RunParallel(count, func(int) (struct{}, error) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		// Hold the slot long enough that the pool saturates.
		time.Sleep(5 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return struct{}{}, nil
	})

	require.Len(t, results, count)
	require.LessOrEqual(t, peak, ParallelLimit)
	require.Greater(t, peak, 1, "expected tasks to actually overlap")
}

// A slow task must not delay unrelated tasks: with count <= ParallelLimit
// every task starts immediately, so wall-clock time tracks the slowest task
// rather than the sum of all of them.
func TestRunParallelWallClockTracksSlowestTask(t *testing.T) {
	const count = 8
	const taskDuration = 100 * time.Millisecond

	start := time.Now()
	results := RunParallel(count, func(int) (struct{}, error) {
		time.Sleep(taskDuration)
		return struct{}{}, nil
	})
	elapsed := time.Since(start)

	require.Len(t, results, count)
	require.Less(t, elapsed, taskDuration*count/2,
		"expected concurrent execution, took %s for %d tasks of %s", elapsed, count, taskDuration)
}

func TestRunParallelReportsPanicAsTaskError(t *testing.T) {
	results := RunParallel(3, func(index int) (int, error) {
		if index == 1 {
			panic("boom")
		}
		return index, nil
	})

	require.Len(t, results, 3)
	require.NoError(t, results[0].Err)
	require.Equal(t, 0, results[0].Value)
	require.Error(t, results[1].Err)
	require.Contains(t, results[1].Err.Error(), "panic in parallel task 1")
	require.Contains(t, results[1].Err.Error(), "boom")
	require.NoError(t, results[2].Err)
	require.Equal(t, 2, results[2].Value)
}

func TestRunParallelWaitsForEveryTask(t *testing.T) {
	const count = ParallelLimit * 2
	var finished atomic.Int64

	results := RunParallel(count, func(index int) (int, error) {
		time.Sleep(time.Millisecond)
		finished.Add(1)
		if index == 0 {
			return 0, errors.New("first task failed")
		}
		return index, nil
	})

	require.Len(t, results, count)
	require.Equal(t, int64(count), finished.Load(),
		"RunParallel must not return before every task completes")
}
