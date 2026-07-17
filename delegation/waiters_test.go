// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWaiterRegistry(t *testing.T) {
	t.Run("signal with no waiter reports absent", func(t *testing.T) {
		r := newWaiterRegistry()
		require.False(t, r.signal("conv-1"))
	})

	t.Run("signal wakes a registered waiter", func(t *testing.T) {
		r := newWaiterRegistry()
		ch := r.register("conv-1")
		require.True(t, r.signal("conv-1"))
		select {
		case <-ch:
		default:
			t.Fatal("expected a pending wake signal")
		}
	})

	t.Run("signals coalesce while the waiter is busy", func(t *testing.T) {
		r := newWaiterRegistry()
		ch := r.register("conv-1")
		require.True(t, r.signal("conv-1"))
		require.True(t, r.signal("conv-1"))
		require.True(t, r.signal("conv-1"))
		<-ch
		select {
		case <-ch:
			t.Fatal("coalesced signals must deliver exactly one pending wake")
		default:
		}
	})

	t.Run("re-registration returns the same channel", func(t *testing.T) {
		r := newWaiterRegistry()
		ch1 := r.register("conv-1")
		ch2 := r.register("conv-1")
		require.True(t, ch1 == ch2, "same delegation must share one wake channel")
	})

	t.Run("deregister removes the waiter", func(t *testing.T) {
		r := newWaiterRegistry()
		r.register("conv-1")
		r.deregister("conv-1")
		require.False(t, r.signal("conv-1"))
	})

	t.Run("independent delegations do not cross-signal", func(t *testing.T) {
		r := newWaiterRegistry()
		ch1 := r.register("conv-1")
		r.register("conv-2")
		require.True(t, r.signal("conv-2"))
		select {
		case <-ch1:
			t.Fatal("conv-1 must not receive conv-2 signals")
		default:
		}
	})
}
