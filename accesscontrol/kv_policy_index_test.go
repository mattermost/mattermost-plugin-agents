// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSystemKV is a map-backed SystemKV with injectable errors.
type fakeSystemKV struct {
	values map[string]string
	getErr error
	setErr error
}

func newFakeSystemKV() *fakeSystemKV {
	return &fakeSystemKV{values: map[string]string{}}
}

func (f *fakeSystemKV) GetSystemValue(key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.values[key], nil
}

func (f *fakeSystemKV) SetSystemValue(key, value string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.values[key] = value
	return nil
}

func newTestKVIndex(kv SystemKV) *KVPolicyIndex {
	return NewKVPolicyIndex(kv, nil)
}

func TestKVPolicyIndexAddRemoveRoundTrip(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		bucketKey    string
	}{
		{name: "agent", resourceType: ResourceTypeAgent, bucketKey: "agents"},
		{name: "service", resourceType: ResourceTypeService, bucketKey: "services"},
		{name: "mcp", resourceType: ResourceTypeMCP, bucketKey: "mcp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv := newFakeSystemKV()
			idx := newTestKVIndex(kv)

			// Empty index: nothing gated.
			has, err := idx.Has(tt.resourceType, "resource1")
			require.NoError(t, err)
			assert.False(t, has)

			require.NoError(t, idx.Add(tt.resourceType, "resource1"))
			require.NoError(t, idx.Add(tt.resourceType, "resource2"))
			require.NoError(t, idx.Add(tt.resourceType, "resource1")) // idempotent

			// Persisted JSON carries the §9.3 shape: the three buckets keyed
			// agents/services/mcp, with IDs only in this type's bucket.
			var data map[string][]string
			require.NoError(t, json.Unmarshal([]byte(kv.values[PolicyIndexKey]), &data))
			assert.ElementsMatch(t, []string{"resource1", "resource2"}, data[tt.bucketKey])
			for key, ids := range data {
				if key != tt.bucketKey {
					assert.Empty(t, ids, "bucket %s must stay empty", key)
				}
			}

			has, err = idx.Has(tt.resourceType, "resource1")
			require.NoError(t, err)
			assert.True(t, has)

			// Other types never see this ID.
			for _, other := range []string{ResourceTypeAgent, ResourceTypeService, ResourceTypeMCP} {
				if other == tt.resourceType {
					continue
				}
				has, err = idx.Has(other, "resource1")
				require.NoError(t, err)
				assert.False(t, has)
			}

			require.NoError(t, idx.Remove(tt.resourceType, "resource1"))
			require.NoError(t, idx.Remove(tt.resourceType, "resource1")) // idempotent

			has, err = idx.Has(tt.resourceType, "resource1")
			require.NoError(t, err)
			assert.False(t, has)

			has, err = idx.Has(tt.resourceType, "resource2")
			require.NoError(t, err)
			assert.True(t, has)
		})
	}
}

func TestKVPolicyIndexHasFailsClosedOnReadError(t *testing.T) {
	kv := newFakeSystemKV()
	kv.getErr = errors.New("kv unreadable")
	idx := newTestKVIndex(kv)

	has, err := idx.Has(ResourceTypeAgent, "resource1")
	require.Error(t, err)
	assert.True(t, has, "unreadable index must report gated (fail closed)")
}

func TestKVPolicyIndexCorruptJSONFailsClosed(t *testing.T) {
	kv := newFakeSystemKV()
	kv.values[PolicyIndexKey] = "{not json"
	idx := newTestKVIndex(kv)

	has, err := idx.Has(ResourceTypeAgent, "resource1")
	require.Error(t, err)
	assert.True(t, has)

	assert.Error(t, idx.Add(ResourceTypeAgent, "resource1"), "updates must not clobber a corrupt index")
}

func TestKVPolicyIndexUnknownResourceType(t *testing.T) {
	idx := newTestKVIndex(newFakeSystemKV())

	has, err := idx.Has("bogus.type", "resource1")
	require.Error(t, err)
	assert.False(t, has)

	assert.Error(t, idx.Add("bogus.type", "resource1"))
	assert.Error(t, idx.Remove("bogus.type", "resource1"))
}

func TestKVPolicyIndexWriteErrorPropagates(t *testing.T) {
	kv := newFakeSystemKV()
	kv.setErr = errors.New("kv write failed")
	idx := newTestKVIndex(kv)

	assert.Error(t, idx.Add(ResourceTypeAgent, "resource1"))
	assert.Error(t, idx.Remove(ResourceTypeAgent, "resource1"))
}
