package store

import "solo-0014-zone-rollout-control/internal/domain"

const schemaVersion = 1

func blankState() domain.State {
	return normalize(domain.State{})
}

func normalize(state domain.State) domain.State {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = schemaVersion
	}
	if state.Plans == nil {
		state.Plans = make(map[string]domain.RolloutPlan)
	}
	if state.Revisions == nil {
		state.Revisions = make(map[string]domain.ZoneRevision)
	}
	if state.Nodes == nil {
		state.Nodes = make(map[string]domain.ResolverNode)
	}
	if state.Leases == nil {
		state.Leases = make(map[string]domain.NodeLease)
	}
	if state.LeaseGroups == nil {
		state.LeaseGroups = make(map[string]domain.LeaseGroup)
	}
	if state.Probes == nil {
		state.Probes = make(map[string]domain.Probe)
	}
	if state.Assessments == nil {
		state.Assessments = make(map[string]domain.RolloutAssessment)
	}
	if state.LeaseRequestKeys == nil {
		state.LeaseRequestKeys = make(map[string]string)
	}
	if state.AssessmentRequestKeys == nil {
		state.AssessmentRequestKeys = make(map[string]string)
	}
	if state.CapabilityCatalogs == nil {
		state.CapabilityCatalogs = make(map[uint64]domain.CapabilityCatalog)
	}
	if state.CatalogRequestKeys == nil {
		state.CatalogRequestKeys = make(map[string]uint64)
	}
	if state.NodeCapabilities == nil {
		state.NodeCapabilities = make(map[string]map[uint64]domain.NodeCapabilityProfile)
	}
	for nodeID, profiles := range state.NodeCapabilities {
		if profiles == nil {
			state.NodeCapabilities[nodeID] = make(map[uint64]domain.NodeCapabilityProfile)
		}
	}
	// Re-derive the latest catalog version defensively from stored versions.
	var latest uint64
	for version := range state.CapabilityCatalogs {
		if version > latest {
			latest = version
		}
	}
	if state.LatestCapabilityVersion < latest {
		state.LatestCapabilityVersion = latest
	}
	if state.AuditEvents == nil {
		state.AuditEvents = []domain.AuditEvent{}
	}
	return state
}
