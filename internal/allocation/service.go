package allocation

import (
	"context"
	"sort"
	"strconv"
	"time"

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

func (s *Service) Allocate(
	ctx context.Context,
	planID string,
	input domain.LeaseInput,
) (domain.LeaseGroup, error) {
	input = domain.NormalizeLeaseInput(input)
	if err := domain.ValidateLeaseInput(input); err != nil {
		return domain.LeaseGroup{}, err
	}
	requestIndex := planID + "\x00" + input.RequestKey
	fingerprint := domain.LeaseFingerprint(planID, input)
	now := s.clock.Now().UTC()
	var group domain.LeaseGroup

	err := s.store.Write(ctx, func(state *domain.State) error {
		plan, ok := state.Plans[planID]
		if !ok {
			return domain.NotFound("plan", planID)
		}
		if plan.State != domain.PlanActive {
			return domain.InvalidState("plan must be active for node leasing", map[string]any{
				"plan_id": planID,
				"state":   plan.State,
			})
		}
		if existingID, exists := state.LeaseRequestKeys[requestIndex]; exists {
			existing, found := state.LeaseGroups[existingID]
			if !found {
				return domain.NewError(domain.CodeStorageFailure, "lease request index is inconsistent", map[string]any{
					"lease_group_id": existingID,
				})
			}
			if existing.RequestFingerprint != fingerprint {
				return domain.NewError(domain.CodeIdempotencyConflict, "request key was reused with different lease parameters", map[string]any{
					"request_key": input.RequestKey,
				})
			}
			group = existing
			return nil
		}

		expireLeases(state, now)

		// Capability requirements are validated against the immutable catalog
		// inside the transaction, so concurrent catalog publication has a
		// deterministic ordering relative to this allocation.
		var tierRanks map[string]int
		if input.Capability != nil {
			catalog, ok := state.CapabilityCatalogs[input.Capability.CatalogVersion]
			if !ok {
				return domain.NotFound("capability catalog", formatVersion(input.Capability.CatalogVersion))
			}
			if err := domain.ValidateRequirementAgainstCatalog(*input.Capability, catalog); err != nil {
				return err
			}
			tierRanks = make(map[string]int, len(catalog.CapacityTiers))
			for index, tier := range catalog.CapacityTiers {
				tierRanks[tier] = index
			}
		}

		// Region filter always runs first and applies to legacy and capable
		// nodes identically.
		regionCandidates := make([]domain.ResolverNode, 0)
		for _, node := range state.Nodes {
			if node.State != domain.NodeReady {
				continue
			}
			if input.Region != "" && node.Region != input.Region {
				continue
			}
			regionCandidates = append(regionCandidates, node)
		}
		if len(regionCandidates) == 0 {
			return domain.NewError(domain.CodeNodeUnavailable, "no eligible node is ready", map[string]any{
				"plan_id": planID,
				"region":  input.Region,
			})
		}

		type chosen struct {
			node    domain.ResolverNode
			profile *domain.NodeCapabilityProfile
			capable bool
		}
		matches := make([]chosen, 0, len(regionCandidates))
		if input.Capability == nil {
			// Legacy region-only selection: nodes that do not declare any
			// capabilities still participate exactly as before.
			for _, node := range regionCandidates {
				matches = append(matches, chosen{node: node})
			}
		} else {
			for _, node := range regionCandidates {
				profiles, declared := state.NodeCapabilities[node.ID]
				if !declared {
					continue
				}
				profile, found := profiles[input.Capability.CatalogVersion]
				if !found {
					continue
				}
				if !domain.ProfileSatisfies(profile, *input.Capability, tierRanks) {
					continue
				}
				profileCopy := profile
				matches = append(matches, chosen{node: node, profile: &profileCopy, capable: true})
			}
			if len(matches) == 0 {
				// Ready nodes exist in the region, but none satisfies the
				// explicit capability conditions. This is distinct from the
				// no-node case above.
				return domain.NewError(domain.CodeCapabilityInsufficient, "no ready node satisfies the capability requirements", map[string]any{
					"plan_id":         planID,
					"region":          input.Region,
					"catalog_version": input.Capability.CatalogVersion,
				})
			}
		}
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].node.Region != matches[j].node.Region {
				return matches[i].node.Region < matches[j].node.Region
			}
			return matches[i].node.ID < matches[j].node.ID
		})
		if len(matches) > input.MaxNodes {
			matches = matches[:input.MaxNodes]
		}

		groupID := s.ids.New("grp")
		leaseIDs := make([]string, 0, len(matches))
		selections := make([]domain.LeaseSelection, 0, len(matches))
		expiresAt := now.Add(time.Duration(input.LeaseSeconds) * time.Second)
		for _, match := range matches {
			candidate := match.node
			leaseID := s.ids.New("lse")
			candidate.State = domain.NodeLeased
			candidate.Version++
			candidate.UpdatedAt = now
			state.Nodes[candidate.ID] = candidate
			lease := domain.NodeLease{
				ID:           leaseID,
				PlanID:       planID,
				NodeID:       candidate.ID,
				AllocationID: groupID,
				State:        domain.LeaseClaimed,
				ClaimedAt:    now,
				ExpiresAt:    expiresAt,
			}
			selection := domain.LeaseSelection{LeaseID: leaseID, NodeID: candidate.ID}
			if match.capable && match.profile != nil {
				explanation := &domain.LeaseCapability{
					CatalogVersion:   match.profile.CatalogVersion,
					ProtocolVersions: append([]string{}, match.profile.ProtocolVersions...),
					CapacityTier:     match.profile.CapacityTier,
					BusinessTags:     append([]string{}, match.profile.BusinessTags...),
				}
				lease.Capability = explanation
				selection.Capability = explanation
			}
			state.Leases[leaseID] = lease
			leaseIDs = append(leaseIDs, leaseID)
			selections = append(selections, selection)
		}
		group = domain.LeaseGroup{
			ID:                 groupID,
			PlanID:             planID,
			RequestKey:         input.RequestKey,
			RequestFingerprint: fingerprint,
			Region:             input.Region,
			Requirement:        input.Capability,
			Selections:         selections,
			LeaseIDs:           leaseIDs,
			CreatedAt:          now,
		}
		state.LeaseGroups[group.ID] = group
		state.LeaseRequestKeys[requestIndex] = group.ID
		attributes := map[string]any{
			"plan_id":     planID,
			"lease_count": len(leaseIDs),
		}
		if input.Capability != nil {
			attributes["catalog_version"] = input.Capability.CatalogVersion
		}
		store.AddAudit(state, s.ids, "lease_group", group.ID, "created", now, attributes)
		return nil
	})
	return group, err
}

func formatVersion(version uint64) string {
	return "v" + strconv.FormatUint(version, 10)
}

func (s *Service) Release(ctx context.Context, leaseID string) (domain.NodeLease, error) {
	now := s.clock.Now().UTC()
	var result domain.NodeLease
	err := s.store.Write(ctx, func(state *domain.State) error {
		lease, ok := state.Leases[leaseID]
		if !ok {
			return domain.NotFound("lease", leaseID)
		}
		if lease.State == domain.LeaseReleased || lease.State == domain.LeaseExpired {
			result = lease
			return nil
		}
		if lease.State != domain.LeaseClaimed {
			return domain.InvalidState("only a claimed lease can be released", map[string]any{
				"lease_id": leaseID,
				"state":    lease.State,
			})
		}
		node, ok := state.Nodes[lease.NodeID]
		if !ok {
			return domain.NewError(domain.CodeStorageFailure, "lease references a missing node", map[string]any{
				"node_id": lease.NodeID,
			})
		}
		finishedAt := now
		lease.State = domain.LeaseReleased
		lease.FinishedAt = &finishedAt
		state.Leases[leaseID] = lease
		if node.State == domain.NodeLeased {
			node.State = domain.NodeReady
			node.Version++
			node.UpdatedAt = now
			state.Nodes[node.ID] = node
		}
		store.AddAudit(state, s.ids, "lease", leaseID, "released", now, nil)
		result = lease
		return nil
	})
	return result, err
}

func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	now := s.clock.Now().UTC()
	expired := 0
	err := s.store.Write(ctx, func(state *domain.State) error {
		expired = expireLeases(state, now)
		return nil
	})
	return expired, err
}

func expireLeases(state *domain.State, now time.Time) int {
	count := 0
	for leaseID, lease := range state.Leases {
		if lease.State != domain.LeaseClaimed || lease.ExpiresAt.After(now) {
			continue
		}
		finishedAt := now
		lease.State = domain.LeaseExpired
		lease.FinishedAt = &finishedAt
		state.Leases[leaseID] = lease
		if node, ok := state.Nodes[lease.NodeID]; ok && node.State == domain.NodeLeased {
			node.State = domain.NodeReady
			node.Version++
			node.UpdatedAt = now
			state.Nodes[node.ID] = node
		}
		count++
	}
	return count
}
