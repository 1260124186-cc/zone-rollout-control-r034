package workflowprobe

import (
	"fmt"
	"net/http"
	"sync"

	"solo-0014-zone-rollout-control/internal/domain"
)

// runNodeCapabilityMatching verifies the full versioned capability surface:
// versioned immutable catalogs, deterministic profile registration, region
// then capability node selection, lease explanations, the distinction between
// no node and insufficient capability, non-retroactive directory upgrades,
// legacy node participation, and deterministic concurrent allocation.
func runNodeCapabilityMatching() error {
	harness, err := newHarness()
	if err != nil {
		return err
	}
	defer harness.close()

	v1Protocols := []string{"dns-tcp", "dns-udp"}
	v1Tiers := []string{"small", "large", "xlarge"}
	v1Tags := []string{"core", "edge"}

	// --- Catalog publication and deterministic replays ----------------------
	catalogV1, err := publishCatalog(harness, "catalog-pub-0001", v1Protocols, v1Tiers, v1Tags)
	if err != nil {
		return err
	}
	if catalogV1.Version != 1 {
		return ensure(false, fmt.Sprintf("first catalog must be version 1, got %d", catalogV1.Version))
	}
	replay, err := publishCatalog(harness, "catalog-pub-0001", v1Protocols, v1Tiers, v1Tags)
	if err != nil {
		return err
	}
	if replay.Version != catalogV1.Version || replay.Fingerprint != catalogV1.Fingerprint {
		return ensure(false, "identical catalog replay must return the same version")
	}
	_, err = publishCatalog(harness, "catalog-pub-0001", []string{"dns-tcp"}, v1Tiers, v1Tags)
	if err := expectFailure(err, http.StatusConflict, string(domain.CodeIdempotencyConflict)); err != nil {
		return err
	}
	_, err = publishCatalog(harness, "catalog-pub-0002", v1Protocols, v1Tiers, v1Tags)
	if err := expectFailure(err, http.StatusConflict, string(domain.CodeDuplicateCatalog)); err != nil {
		return err
	}

	v2Protocols := []string{"dns-tcp", "dns-udp", "dot"}
	v2Tiers := []string{"small", "large", "xlarge", "2xlarge"}
	v2Tags := []string{"core", "edge", "experimental"}
	catalogV2, err := publishCatalog(harness, "catalog-pub-0003", v2Protocols, v2Tiers, v2Tags)
	if err != nil {
		return err
	}
	if catalogV2.Version != 2 {
		return ensure(false, fmt.Sprintf("new catalog content must append version 2, got %d", catalogV2.Version))
	}
	var latest domain.CapabilityCatalog
	if err := harness.call(http.MethodGet, "/v1/capability-catalogs/latest", nil, &latest); err != nil {
		return err
	}
	if latest.Version != 2 {
		return ensure(false, "latest catalog endpoint must return version 2")
	}
	var fetchedV1 domain.CapabilityCatalog
	if err := harness.call(http.MethodGet, "/v1/capability-catalogs/1", nil, &fetchedV1); err != nil {
		return err
	}
	if fetchedV1.Version != 1 || len(fetchedV1.Protocols) != 2 {
		return ensure(false, "catalog version 1 must remain readable after an upgrade")
	}

	// --- Nodes: one legacy node, three capable in east, one in west ---------
	legacy, err := enrollNode(harness, "resolver-cap-legacy", "east")
	if err != nil {
		return err
	}
	nodeB, err := enrollNode(harness, "resolver-cap-east-b", "east")
	if err != nil {
		return err
	}
	nodeC, err := enrollNode(harness, "resolver-cap-east-c", "east")
	if err != nil {
		return err
	}
	nodeD, err := enrollNode(harness, "resolver-cap-east-d", "east")
	if err != nil {
		return err
	}
	nodeW, err := enrollNode(harness, "resolver-cap-west-w", "west")
	if err != nil {
		return err
	}

	// --- Profile registration and duplicate handling ------------------------
	profileB := domain.NodeCapabilityInput{
		CatalogVersion:   1,
		ProtocolVersions: []string{"dns-tcp"},
		CapacityTier:     "small",
		BusinessTags:     []string{"edge"},
	}
	profileC := domain.NodeCapabilityInput{
		CatalogVersion:   1,
		ProtocolVersions: []string{"dns-tcp", "dns-udp"},
		CapacityTier:     "large",
		BusinessTags:     []string{"core", "edge"},
	}
	profileD := domain.NodeCapabilityInput{
		CatalogVersion:   1,
		ProtocolVersions: []string{"dns-udp"},
		CapacityTier:     "xlarge",
		BusinessTags:     []string{"core"},
	}
	profileW := domain.NodeCapabilityInput{
		CatalogVersion:   1,
		ProtocolVersions: []string{"dns-tcp", "dns-udp"},
		CapacityTier:     "small",
		BusinessTags:     []string{"edge"},
	}
	for nodeID, input := range map[string]domain.NodeCapabilityInput{
		nodeB.ID: profileB,
		nodeC.ID: profileC,
		nodeD.ID: profileD,
		nodeW.ID: profileW,
	} {
		if _, err := registerCapability(harness, nodeID, input.CatalogVersion, input.ProtocolVersions, input.CapacityTier, input.BusinessTags); err != nil {
			return err
		}
	}
	if _, err := registerCapability(harness, nodeC.ID, profileC.CatalogVersion, profileC.ProtocolVersions, profileC.CapacityTier, profileC.BusinessTags); err != nil {
		return err
	}
	_, err = registerCapability(harness, nodeC.ID, 1, []string{"dns-tcp"}, "large", []string{"core", "edge"})
	if err := expectFailure(err, http.StatusConflict, string(domain.CodeDuplicateCapability)); err != nil {
		return err
	}
	_, err = registerCapability(harness, nodeC.ID, 99, []string{"dns-tcp"}, "large", []string{"edge"})
	if err := expectFailure(err, http.StatusNotFound, string(domain.CodeNotFound)); err != nil {
		return err
	}
	_, err = registerCapability(harness, nodeC.ID, 1, []string{"doh-3"}, "large", []string{"edge"})
	if err := expectFailure(err, http.StatusBadRequest, string(domain.CodeValidationFailed)); err != nil {
		return err
	}

	plan, _, err := createActivePlan(harness, "capability.example", 4242, 6, 0.5, 100)
	if err != nil {
		return err
	}

	// --- Capability match after region: only node-c qualifies ---------------
	requirementC := &domain.CapabilityRequirement{
		CatalogVersion:  1,
		Protocols:       []string{"dns-tcp"},
		MinCapacityTier: "large",
		BusinessTags:    []string{"edge"},
	}
	group, err := allocateWithCapability(harness, plan.ID, "cap-lease-0001", "east", 1, requirementC)
	if err != nil {
		return err
	}
	if len(group.LeaseIDs) != 1 || len(group.Selections) != 1 {
		return ensure(false, "capability allocation must produce exactly one lease")
	}
	if group.Selections[0].NodeID != nodeC.ID {
		return ensure(false, "only node-c satisfies tcp + large + edge in the east region")
	}
	if group.Requirement == nil || group.Requirement.CatalogVersion != 1 {
		return ensure(false, "lease group must echo the capability requirement")
	}
	explanation := group.Selections[0].Capability
	if explanation == nil {
		return ensure(false, "lease selection must carry a capability explanation")
	}
	if explanation.CatalogVersion != 1 || explanation.CapacityTier != "large" {
		return ensure(false, "lease explanation must record the used catalog version and declared tier")
	}
	if len(explanation.ProtocolVersions) != 2 || len(explanation.BusinessTags) != 2 {
		return ensure(false, "lease explanation must snapshot the declared node profile")
	}

	// --- Capability insufficient is distinct from no node -------------------
	insufficient := &domain.CapabilityRequirement{
		CatalogVersion:  1,
		Protocols:       []string{"dns-udp"},
		MinCapacityTier: "xlarge",
		BusinessTags:    []string{"edge"},
	}
	_, err = allocateWithCapability(harness, plan.ID, "cap-lease-0002", "east", 1, insufficient)
	if err := expectFailure(err, http.StatusConflict, string(domain.CodeCapabilityInsufficient)); err != nil {
		return err
	}
	_, err = allocateWithCapability(harness, plan.ID, "cap-lease-0003", "north", 1, requirementC)
	if err := expectFailure(err, http.StatusConflict, string(domain.CodeNodeUnavailable)); err != nil {
		return err
	}

	// --- Region filter runs before capability matching ----------------------
	westRequirement := &domain.CapabilityRequirement{
		CatalogVersion:  1,
		Protocols:       []string{"dns-tcp"},
		MinCapacityTier: "small",
		BusinessTags:    []string{"edge"},
	}
	westGroup, err := allocateWithCapability(harness, plan.ID, "cap-lease-0004", "west", 1, westRequirement)
	if err != nil {
		return err
	}
	if westGroup.Selections[0].NodeID != nodeW.ID {
		return ensure(false, "region filter must exclude capable east nodes and select node-w")
	}

	// --- Idempotent replay and same-key different-content conflict ----------
	replayGroup, err := allocateWithCapability(harness, plan.ID, "cap-lease-0001", "east", 1, requirementC)
	if err != nil {
		return err
	}
	if replayGroup.ID != group.ID {
		return ensure(false, "capability allocation replay must return the same group")
	}
	changed := *requirementC
	changed.Protocols = []string{"dns-tcp", "dns-udp"}
	_, err = allocateWithCapability(harness, plan.ID, "cap-lease-0001", "east", 1, &changed)
	if err := expectFailure(err, http.StatusConflict, string(domain.CodeIdempotencyConflict)); err != nil {
		return err
	}

	// --- Release returns a lease that still carries its explanation ---------
	var releasedLease domain.NodeLease
	if err := harness.call(http.MethodPatch, "/v1/leases/"+group.LeaseIDs[0]+"/release", nil, &releasedLease); err != nil {
		return err
	}
	if releasedLease.Capability == nil || releasedLease.Capability.CatalogVersion != 1 {
		return ensure(false, "released lease must persist the capability version used at allocation")
	}
	if err := harness.call(http.MethodPatch, "/v1/leases/"+westGroup.LeaseIDs[0]+"/release", nil, nil); err != nil {
		return err
	}

	// --- Requirement validation against the catalog -------------------------
	unknownVersion := &domain.CapabilityRequirement{CatalogVersion: 99, MinCapacityTier: "large"}
	_, err = allocateWithCapability(harness, plan.ID, "cap-lease-0005", "east", 1, unknownVersion)
	if err := expectFailure(err, http.StatusNotFound, string(domain.CodeNotFound)); err != nil {
		return err
	}
	unknownToken := &domain.CapabilityRequirement{CatalogVersion: 1, MinCapacityTier: "huge"}
	_, err = allocateWithCapability(harness, plan.ID, "cap-lease-0006", "east", 1, unknownToken)
	if err := expectFailure(err, http.StatusBadRequest, string(domain.CodeValidationFailed)); err != nil {
		return err
	}

	// --- Directory upgrade must not rewrite existing leases ----------------
	v2Profile := domain.NodeCapabilityInput{
		CatalogVersion:   2,
		ProtocolVersions: []string{"dot"},
		CapacityTier:     "2xlarge",
		BusinessTags:     []string{"experimental"},
	}
	if _, err := registerCapability(harness, nodeC.ID, v2Profile.CatalogVersion, v2Profile.ProtocolVersions, v2Profile.CapacityTier, v2Profile.BusinessTags); err != nil {
		return err
	}
	upgradedGroup, err := allocateWithCapability(harness, plan.ID, "cap-lease-0001", "east", 1, requirementC)
	if err != nil {
		return err
	}
	if upgradedGroup.ID != group.ID {
		return ensure(false, "replay after catalog upgrade must return the original group")
	}
	snapshot := upgradedGroup.Selections[0].Capability
	if snapshot.CatalogVersion != 1 || snapshot.CapacityTier != "large" {
		return ensure(false, "catalog upgrades and new profiles must not rewrite historical lease explanations")
	}
	var nodeCaps domain.NodeCapabilitySet
	if err := harness.call(http.MethodGet, "/v1/nodes/"+nodeC.ID+"/capabilities", nil, &nodeCaps); err != nil {
		return err
	}
	if len(nodeCaps.Profiles) != 2 {
		return ensure(false, "node must keep one immutable profile per catalog version")
	}

	// --- Legacy nodes keep participating under plain region rules -----------
	legacyGroup, err := allocate(harness, plan.ID, "cap-lease-0007", "east", 4)
	if err != nil {
		return err
	}
	if len(legacyGroup.LeaseIDs) != 4 {
		return ensure(false, "legacy region allocation must select every ready east node")
	}
	if legacyGroup.Requirement != nil {
		return ensure(false, "legacy allocation must not record a capability requirement")
	}
	selectedNodes := make(map[string]bool)
	legacyIncluded := false
	for _, selection := range legacyGroup.Selections {
		selectedNodes[selection.NodeID] = true
		if selection.NodeID == legacy.ID {
			legacyIncluded = true
		}
		if selection.Capability != nil {
			return ensure(false, "legacy allocation selections must not carry capability explanations")
		}
	}
	if !legacyIncluded {
		return ensure(false, "a node that declares no capabilities must still participate in region allocation")
	}
	if !selectedNodes[nodeC.ID] || !selectedNodes[nodeB.ID] || !selectedNodes[nodeD.ID] {
		return ensure(false, "capability-declaring nodes must remain eligible for region-only allocation")
	}

	// --- Deterministic concurrent allocation --------------------------------
	// Five south nodes are ready, but only three declare the required
	// capabilities. That keeps the region non-empty after the capable nodes are
	// leased, so losing requests deterministically fail as
	// capability_insufficient instead of node_unavailable.
	southNames := []string{
		"resolver-cap-south-1",
		"resolver-cap-south-2",
		"resolver-cap-south-3",
		"resolver-cap-south-4",
		"resolver-cap-south-5",
	}
	southNodes := make([]domain.ResolverNode, 0, len(southNames))
	for _, name := range southNames {
		node, enrollErr := enrollNode(harness, name, "south")
		if enrollErr != nil {
			return enrollErr
		}
		southNodes = append(southNodes, node)
	}
	for index := 1; index <= 2; index++ {
		if _, regErr := registerCapability(harness, southNodes[index].ID, 1, []string{"dns-tcp"}, "small", []string{"edge"}); regErr != nil {
			return regErr
		}
	}
	// The first capable profile is written through two concurrent identical
	// requests; duplicate registration must converge deterministically.
	var wg sync.WaitGroup
	duplicateResults := make([]domain.NodeCapabilityProfile, 2)
	duplicateErrors := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			profile, regErr := registerCapability(harness, southNodes[0].ID, 1, []string{"dns-tcp"}, "small", []string{"edge"})
			duplicateResults[index] = profile
			duplicateErrors[index] = regErr
		}(i)
	}
	wg.Wait()
	for _, regErr := range duplicateErrors {
		if regErr != nil {
			return ensure(false, "concurrent identical capability registration must converge: "+regErr.Error())
		}
	}
	if duplicateResults[0].CatalogVersion != duplicateResults[1].CatalogVersion ||
		duplicateResults[0].CapacityTier != duplicateResults[1].CapacityTier {
		return ensure(false, "concurrent identical registrations must resolve to the same profile")
	}

	southRequirement := &domain.CapabilityRequirement{
		CatalogVersion:  1,
		Protocols:       []string{"dns-tcp"},
		MinCapacityTier: "small",
		BusinessTags:    []string{"edge"},
	}
	capableCount := 3
	const concurrentRequests = 6
	type concurrentResult struct {
		group domain.LeaseGroup
		err   error
	}
	results := make([]concurrentResult, concurrentRequests)
	wg2 := sync.WaitGroup{}
	for i := 0; i < concurrentRequests; i++ {
		wg2.Add(1)
		go func(index int) {
			defer wg2.Done()
			key := fmt.Sprintf("cap-conc-%04d", index+1)
			groupResult, allocErr := allocateWithCapability(harness, plan.ID, key, "south", 1, southRequirement)
			results[index] = concurrentResult{group: groupResult, err: allocErr}
		}(i)
	}
	wg2.Wait()

	successes := 0
	failures := 0
	leasedNodes := make(map[string]string)
	for _, result := range results {
		if result.err != nil {
			failures++
			if err := expectFailure(result.err, http.StatusConflict, string(domain.CodeCapabilityInsufficient)); err != nil {
				return err
			}
			continue
		}
		successes++
		nodeID := result.group.Selections[0].NodeID
		if other, exists := leasedNodes[nodeID]; exists {
			return ensure(false, fmt.Sprintf("node %s was leased twice (groups %s, %s)", nodeID, other, result.group.ID))
		}
		leasedNodes[nodeID] = result.group.ID
	}
	if successes != capableCount || failures != concurrentRequests-capableCount {
		return ensure(false, fmt.Sprintf("concurrent allocation must have %d unique successes and %d capability_insufficient failures, got %d and %d",
			capableCount, concurrentRequests-capableCount, successes, failures))
	}
	return nil
}
