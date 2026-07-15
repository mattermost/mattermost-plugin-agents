// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

import (
	"encoding/json"
	"fmt"
	"slices"
)

// PolicyIndexKey is the Agents_System key holding the policy index JSON.
const PolicyIndexKey = "abac_policy_index"

// PolicyIndexMutexKey is the cluster mutex guarding read-modify-write updates.
const PolicyIndexMutexKey = "ai_abac_policy_index"

// SystemKV is the narrow store surface the index needs (satisfied by *store.Store).
type SystemKV interface {
	GetSystemValue(key string) (string, error) // "" when absent
	SetSystemValue(key, value string) error
}

// policyIndexData is the persisted JSON shape (contract §9.3):
// {"agents":[ids],"services":[ids],"mcp":[ids]}.
type policyIndexData struct {
	Agents   []string `json:"agents"`
	Services []string `json:"services"`
	MCP      []string `json:"mcp"`
}

func (d *policyIndexData) bucket(resourceType string) (*[]string, error) {
	switch resourceType {
	case ResourceTypeAgent:
		return &d.Agents, nil
	case ResourceTypeService:
		return &d.Services, nil
	case ResourceTypeMCP:
		return &d.MCP, nil
	default:
		return nil, fmt.Errorf("unknown policy index resource type %q", resourceType)
	}
}

// KVPolicyIndex persists the policy index in the Agents_System KV table.
//
// Add/Remove perform an unguarded read-modify-write: callers MUST serialize
// mutations under the PolicyIndexMutexKey cluster mutex. In production the
// only mutator is Checker.SavePolicy/DeletePolicy, which holds that mutex
// across the whole policy+index mutation (see pap.go).
type KVPolicyIndex struct {
	kv  SystemKV
	log Logger
}

// NewKVPolicyIndex builds the production PolicyIndex.
func NewKVPolicyIndex(kv SystemKV, log Logger) *KVPolicyIndex {
	return &KVPolicyIndex{kv: kv, log: log}
}

func (i *KVPolicyIndex) load() (policyIndexData, error) {
	var data policyIndexData
	raw, err := i.kv.GetSystemValue(PolicyIndexKey)
	if err != nil {
		return data, fmt.Errorf("failed to read policy index: %w", err)
	}
	if raw == "" {
		return data, nil
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return data, fmt.Errorf("failed to parse policy index: %w", err)
	}
	return data, nil
}

// Has reads without serialization: it is consulted only on unavailable/error
// outcomes (rare) and a torn read is impossible because SetSystemValue
// replaces the value atomically.
func (i *KVPolicyIndex) Has(resourceType, resourceID string) (bool, error) {
	data, err := i.load()
	if err != nil {
		logWarn(i.log, "ABAC policy index unreadable; failing closed", "error", err.Error())
		return true, err
	}
	bucket, err := data.bucket(resourceType)
	if err != nil {
		return false, err
	}
	return slices.Contains(*bucket, resourceID), nil
}

// Add records resourceID under resourceType; no-op success when present.
func (i *KVPolicyIndex) Add(resourceType, resourceID string) error {
	return i.update(resourceType, func(bucket []string) []string {
		if slices.Contains(bucket, resourceID) {
			return bucket
		}
		return append(bucket, resourceID)
	})
}

// Remove deletes resourceID from resourceType; no-op success when absent.
func (i *KVPolicyIndex) Remove(resourceType, resourceID string) error {
	return i.update(resourceType, func(bucket []string) []string {
		return slices.DeleteFunc(bucket, func(id string) bool { return id == resourceID })
	})
}

func (i *KVPolicyIndex) update(resourceType string, transform func([]string) []string) error {
	data, err := i.load()
	if err != nil {
		return err
	}
	bucket, err := data.bucket(resourceType)
	if err != nil {
		return err
	}
	*bucket = transform(*bucket)

	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal policy index: %w", err)
	}
	if err := i.kv.SetSystemValue(PolicyIndexKey, string(raw)); err != nil {
		return fmt.Errorf("failed to persist policy index: %w", err)
	}
	return nil
}
