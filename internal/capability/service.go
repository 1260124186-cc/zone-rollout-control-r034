// Package capability owns the versioned capability directory: publishing
// immutable catalog versions and registering per-node capability profiles
// against one exact catalog version.
package capability

import (
	"context"
	"strconv"

	"solo-0014-zone-rollout-control/internal/domain"
	"solo-0014-zone-rollout-control/internal/platform"
	"solo-0014-zone-rollout-control/internal/store"
)

type Service struct {
	store *store.Store
	clock platform.Clock
	ids   platform.IDGenerator
}

func New(stateStore *store.Store, clock platform.Clock, ids platform.IDGenerator) *Service {
	return &Service{store: stateStore, clock: clock, ids: ids}
}

// PublishCatalog appends a new immutable capability catalog version.
//
// Deterministic outcomes:
//   - Replaying the same request key with identical content returns the
//     catalog created by the first request.
//   - Reusing a request key with different content returns
//     idempotency_conflict.
//   - Publishing content identical to any existing version (regardless of
//     request key) returns duplicate_catalog; directory versions never alias.
func (s *Service) PublishCatalog(
	ctx context.Context,
	input domain.CatalogPublishInput,
) (domain.CapabilityCatalog, error) {
	input = domain.NormalizeCatalogInput(input)
	if err := domain.ValidateCatalogInput(input); err != nil {
		return domain.CapabilityCatalog{}, err
	}
	fingerprint := domain.CatalogFingerprint(input)
	now := s.clock.Now().UTC()
	var catalog domain.CapabilityCatalog

	err := s.store.Write(ctx, func(state *domain.State) error {
		if version, exists := state.CatalogRequestKeys[input.RequestKey]; exists {
			existing, found := state.CapabilityCatalogs[version]
			if !found {
				return domain.NewError(domain.CodeStorageFailure, "catalog request index is inconsistent", map[string]any{
					"catalog_version": version,
				})
			}
			if existing.Fingerprint != fingerprint {
				return domain.NewError(domain.CodeIdempotencyConflict, "request key was reused with different catalog content", map[string]any{
					"request_key": input.RequestKey,
				})
			}
			catalog = existing
			return nil
		}
		for _, existing := range state.CapabilityCatalogs {
			if existing.Fingerprint == fingerprint {
				return domain.NewError(domain.CodeDuplicateCatalog, "capability catalog content already exists", map[string]any{
					"catalog_version": existing.Version,
				})
			}
		}
		version := state.LatestCapabilityVersion + 1
		catalog = domain.CapabilityCatalog{
			Version:       version,
			Protocols:     input.Protocols,
			CapacityTiers: input.CapacityTiers,
			BusinessTags:  input.BusinessTags,
			Fingerprint:   fingerprint,
			RequestKey:    input.RequestKey,
			CreatedAt:     now,
		}
		state.CapabilityCatalogs[version] = catalog
		state.LatestCapabilityVersion = version
		state.CatalogRequestKeys[input.RequestKey] = version
		store.AddAudit(state, s.ids, "capability_catalog", "", "published", now, map[string]any{
			"catalog_version": version,
		})
		return nil
	})
	return catalog, err
}

// GetCatalog returns one catalog version, or the latest when version is zero.
func (s *Service) GetCatalog(ctx context.Context, version uint64) (domain.CapabilityCatalog, error) {
	var catalog domain.CapabilityCatalog
	err := s.store.Read(ctx, func(state *domain.State) error {
		target := version
		if target == 0 {
			target = state.LatestCapabilityVersion
		}
		if target == 0 {
			return domain.NotFound("capability catalog", "latest")
		}
		found, ok := state.CapabilityCatalogs[target]
		if !ok {
			return domain.NotFound("capability catalog", formatVersion(target))
		}
		catalog = found
		return nil
	})
	return catalog, err
}

// RegisterProfile declares one node's capability set against one exact catalog
// version. Re-registration with identical content is an idempotent success;
// different content returns duplicate_capability, so directory upgrades never
// mutate a declaration that existing leases may explain.
func (s *Service) RegisterProfile(
	ctx context.Context,
	nodeID string,
	input domain.NodeCapabilityInput,
) (domain.NodeCapabilityProfile, error) {
	input = domain.NormalizeNodeCapabilityInput(input)
	if err := domain.ValidateNodeCapabilityInput(input); err != nil {
		return domain.NodeCapabilityProfile{}, err
	}
	now := s.clock.Now().UTC()
	var profile domain.NodeCapabilityProfile

	err := s.store.Write(ctx, func(state *domain.State) error {
		node, ok := state.Nodes[nodeID]
		if !ok {
			return domain.NotFound("node", nodeID)
		}
		catalog, ok := state.CapabilityCatalogs[input.CatalogVersion]
		if !ok {
			return domain.NotFound("capability catalog", formatVersion(input.CatalogVersion))
		}
		if err := domain.ValidateNodeCapabilityAgainstCatalog(input, catalog); err != nil {
			return err
		}
		if profiles, exists := state.NodeCapabilities[nodeID]; exists {
			if existing, found := profiles[input.CatalogVersion]; found {
				if !sameProfile(existing, input) {
					return domain.NewError(domain.CodeDuplicateCapability, "node already declared capabilities for this catalog version", map[string]any{
						"node_id":         nodeID,
						"catalog_version": input.CatalogVersion,
					})
				}
				profile = existing
				return nil
			}
		}
		if state.NodeCapabilities[nodeID] == nil {
			state.NodeCapabilities[nodeID] = make(map[uint64]domain.NodeCapabilityProfile)
		}
		profile = domain.NodeCapabilityProfile{
			NodeID:           node.ID,
			CatalogVersion:   input.CatalogVersion,
			ProtocolVersions: input.ProtocolVersions,
			CapacityTier:     input.CapacityTier,
			BusinessTags:     input.BusinessTags,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		state.NodeCapabilities[nodeID][input.CatalogVersion] = profile
		node.Version++
		node.UpdatedAt = now
		state.Nodes[node.ID] = node
		store.AddAudit(state, s.ids, "node_capability", node.ID, "declared", now, map[string]any{
			"catalog_version": input.CatalogVersion,
		})
		return nil
	})
	return profile, err
}

// GetNodeCapabilities returns every profile a node has declared.
func (s *Service) GetNodeCapabilities(ctx context.Context, nodeID string) (domain.NodeCapabilitySet, error) {
	var result domain.NodeCapabilitySet
	err := s.store.Read(ctx, func(state *domain.State) error {
		if _, ok := state.Nodes[nodeID]; !ok {
			return domain.NotFound("node", nodeID)
		}
		profiles := make(map[uint64]domain.NodeCapabilityProfile)
		for version, profile := range state.NodeCapabilities[nodeID] {
			profiles[version] = profile
		}
		result = domain.NodeCapabilitySet{NodeID: nodeID, Profiles: profiles}
		return nil
	})
	return result, err
}

func sameProfile(existing domain.NodeCapabilityProfile, input domain.NodeCapabilityInput) bool {
	if existing.CatalogVersion != input.CatalogVersion ||
		existing.CapacityTier != input.CapacityTier ||
		len(existing.ProtocolVersions) != len(input.ProtocolVersions) ||
		len(existing.BusinessTags) != len(input.BusinessTags) {
		return false
	}
	for index := range input.ProtocolVersions {
		if existing.ProtocolVersions[index] != input.ProtocolVersions[index] {
			return false
		}
	}
	for index := range input.BusinessTags {
		if existing.BusinessTags[index] != input.BusinessTags[index] {
			return false
		}
	}
	return true
}

func formatVersion(version uint64) string {
	return "v" + strconv.FormatUint(version, 10)
}
