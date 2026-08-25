// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package delegation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWaiterRegistry(t *testing.T) {
	tests := []struct {
		name  string
		run   func(t *testing.T, r *waiterRegistry)
		check func(t *testing.T, r *waiterRegistry)
	}{
		{
			name: "signal with no waiter reports absent",
			run:  func(t *testing.T, r *waiterRegistry) {},
			check: func(t *testing.T, r *waiterRegistry) {
				require.False(t, r.signal("conv-1"))
			},
		},
		{
			name: "signal wakes a registered waiter",
			run: func(t *testing.T, r *waiterRegistry) {
				r.register("conv-1")
			},
			check: func(t *testing.T, r *waiterRegistry) {
				require.True(t, r.signal("conv-1"))
				select {
				case <-r.register("conv-1"):
				default:
					t.Fatal("expected a pending wake signal")
				}
			},
		},
		{
			name: "signals coalesce while the waiter is busy",
			run: func(t *testing.T, r *waiterRegistry) {
				r.register("conv-1")
				require.True(t, r.signal("conv-1"))
				require.True(t, r.signal("conv-1"))
				require.True(t, r.signal("conv-1"))
			},
			check: func(t *testing.T, r *waiterRegistry) {
				ch := r.register("conv-1")
				<-ch
				select {
				case <-ch:
					t.Fatal("coalesced signals must deliver exactly one pending wake")
				default:
				}
			},
		},
		{
			name: "re-registration returns the same channel",
			run:  func(t *testing.T, r *waiterRegistry) {},
			check: func(t *testing.T, r *waiterRegistry) {
				first := r.register("conv-1")
				second := r.register("conv-1")
				require.True(t, first == second, "same delegation must share one wake channel")
			},
		},
		{
			name: "deregister removes the waiter",
			run: func(t *testing.T, r *waiterRegistry) {
				r.register("conv-1")
				r.deregister("conv-1")
			},
			check: func(t *testing.T, r *waiterRegistry) {
				require.False(t, r.signal("conv-1"))
			},
		},
		{
			name: "independent delegations do not cross-signal",
			run: func(t *testing.T, r *waiterRegistry) {
				r.register("conv-1")
				r.register("conv-2")
				require.True(t, r.signal("conv-2"))
			},
			check: func(t *testing.T, r *waiterRegistry) {
				select {
				case <-r.register("conv-1"):
					t.Fatal("conv-1 must not receive conv-2 signals")
				default:
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newWaiterRegistry()
			tc.run(t, r)
			tc.check(t, r)
		})
	}
}
