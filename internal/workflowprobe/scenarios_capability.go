package workflowprobe

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"solo-0014-zone-rollout-control/internal/domain"
)

// capabilityRegisterResult 对齐 HTTP 返回的登记结果结构。
type capabilityRegisterResult struct {
	Profile domain.NodeCapabilityProfile `json:"profile"`
	Created bool                         `json:"created"`
}

type capabilityProfilesPage struct {
	Items []domain.NodeCapabilityProfile `json:"items"`
}

func registerCapability(
	harness *harness,
	nodeID string,
	protocols []string,
	tier string,
	tags []string,
) (capabilityRegisterResult, error) {
	var result capabilityRegisterResult
	err := harness.call(http.MethodPut, "/v1/nodes/"+nodeID+"/capabilities", domain.CapabilityProfileInput{
		ProtocolVersions: protocols,
		CapacityTier:     tier,
		Tags:             tags,
	}, &result)
	return result, err
}

func listCapabilities(harness *harness, nodeID string) ([]domain.NodeCapabilityProfile, error) {
	var page capabilityProfilesPage
	err := harness.call(http.MethodGet, "/v1/nodes/"+nodeID+"/capabilities", nil, &page)
	return page.Items, err
}

func allocateWithCapability(
	harness *harness,
	planID string,
	requestKey string,
	region string,
	maxNodes int,
	match *domain.CapabilityMatch,
) (domain.LeaseGroup, error) {
	var group domain.LeaseGroup
	err := harness.call(http.MethodPatch, "/v1/plans/"+planID+"/leases", domain.LeaseInput{
		RequestKey:   requestKey,
		Region:       region,
		LeaseSeconds: 600,
		MaxNodes:     maxNodes,
		Capability:   match,
	}, &group)
	return group, err
}

func getLease(harness *harness, leaseID string) (domain.NodeLease, error) {
	var lease domain.NodeLease
	err := harness.call(http.MethodGet, "/v1/leases/"+leaseID, nil, &lease)
	return lease, err
}

func releaseLeaseByID(harness *harness, leaseID string) error {
	return harness.call(http.MethodPatch, "/v1/leases/"+leaseID+"/release", nil, nil)
}

func runNodeCapabilityMatching() error {
	harness, err := newHarness()
	if err != nil {
		return err
	}
	defer harness.close()

	// 一个从未声明能力的旧节点，加两个稍后登记能力的节点，全部同区域。
	legacy, err := enrollNode(harness, "resolver-cap-legacy", "cap-east")
	if err != nil {
		return err
	}
	nodeA, err := enrollNode(harness, "resolver-cap-a", "cap-east")
	if err != nil {
		return err
	}
	nodeB, err := enrollNode(harness, "resolver-cap-b", "cap-east")
	if err != nil {
		return err
	}
	plan, _, err := createActivePlan(harness, "capability.example", 2001, 3, 0.5, 100)
	if err != nil {
		return err
	}

	// 1. 不声明能力条件时，旧节点按现有区域规则参与分配，租约解释不
	//    包含能力版本。
	regionOnly, err := allocateWithCapability(harness, plan.ID, "cap-lease-region-001", "cap-east", 10, nil)
	if err != nil {
		return err
	}
	if len(regionOnly.LeaseIDs) != 3 {
		return ensure(false, fmt.Sprintf("region-only allocation must include all three nodes, got %d", len(regionOnly.LeaseIDs)))
	}
	if regionOnly.CapabilityMatch != nil {
		return ensure(false, "region-only group must not carry capability requirements")
	}
	legacyLease, err := findLeaseForNode(harness, regionOnly.LeaseIDs, legacy.ID)
	if err != nil {
		return err
	}
	if legacyLease.Explanation == nil || legacyLease.Explanation.Capability != nil {
		return ensure(false, "legacy node lease must not carry a capability trace")
	}
	for _, leaseID := range regionOnly.LeaseIDs {
		if err := releaseLeaseByID(harness, leaseID); err != nil {
			return err
		}
	}

	// 2. 为节点 A 登记 v1，为节点 B 登记 v1；旧节点继续无档案。
	registeredA, err := registerCapability(harness, nodeA.ID,
		[]string{"doh-http2", "dns-tcp"}, "medium", []string{"edge", "beta"})
	if err != nil {
		return err
	}
	if !registeredA.Created || registeredA.Profile.ProfileVersion != 1 {
		return ensure(false, "first capability registration must create version 1")
	}
	registeredB, err := registerCapability(harness, nodeB.ID,
		[]string{"doq"}, "large", []string{"edge"})
	if err != nil {
		return err
	}
	if !registeredB.Created || registeredB.Profile.ProfileVersion != 1 {
		return ensure(false, "node B first registration must create version 1")
	}

	// 3. 重复登记完全相同内容（字段顺序不同）必须确定性返回既有版本。
	duplicate, err := registerCapability(harness, nodeA.ID,
		[]string{"DNS-TCP ", "doh-http2"}, "MEDIUM", []string{"beta", "edge", "edge"})
	if err != nil {
		return err
	}
	if duplicate.Created {
		return ensure(false, "identical capability content must not create a new version")
	}
	if duplicate.Profile.ID != registeredA.Profile.ID || duplicate.Profile.ProfileVersion != 1 {
		return ensure(false, "duplicate registration must return the existing version")
	}
	profilesA, err := listCapabilities(harness, nodeA.ID)
	if err != nil {
		return err
	}
	if len(profilesA) != 1 {
		return ensure(false, fmt.Sprintf("node A catalog must keep one version after dedupe, got %d", len(profilesA)))
	}

	// 4. 按能力条件分配：仅节点 A 同时满足 doh-http2 + medium + beta。
	dohMatch := &domain.CapabilityMatch{
		RequiredProtocolVersions: []string{"doh-http2"},
		MinimumCapacityTier:      "medium",
		RequiredTags:             []string{"beta"},
	}
	groupV1, err := allocateWithCapability(harness, plan.ID, "cap-lease-match-v1-001", "cap-east", 10, dohMatch)
	if err != nil {
		return err
	}
	if len(groupV1.LeaseIDs) != 1 {
		return ensure(false, fmt.Sprintf("capability match must select exactly node A, got %d leases", len(groupV1.LeaseIDs)))
	}
	leaseV1, err := getLease(harness, groupV1.LeaseIDs[0])
	if err != nil {
		return err
	}
	if leaseV1.NodeID != nodeA.ID {
		return ensure(false, "capability match selected the wrong node")
	}
	if leaseV1.Explanation == nil || leaseV1.Explanation.Capability == nil {
		return ensure(false, "capability-matched lease must record a capability trace")
	}
	trace := leaseV1.Explanation.Capability
	if trace.ProfileVersion != 1 || trace.ProfileID != registeredA.Profile.ID {
		return ensure(false, "lease explanation must pin capability profile version 1")
	}
	if len(trace.MatchedConditions) != 3 {
		return ensure(false, fmt.Sprintf("trace must list three matched conditions, got %v", trace.MatchedConditions))
	}

	// 5. 目录升级：节点 A 登记 v2。已有租约的解释不得被追溯改写。
	upgraded, err := registerCapability(harness, nodeA.ID,
		[]string{"doq", "doh-http2", "dns-tcp"}, "xlarge", []string{"edge", "ga"})
	if err != nil {
		return err
	}
	if !upgraded.Created || upgraded.Profile.ProfileVersion != 2 {
		return ensure(false, "changed capability content must append version 2")
	}
	profilesA, err = listCapabilities(harness, nodeA.ID)
	if err != nil {
		return err
	}
	if len(profilesA) != 2 || profilesA[1].ID != upgraded.Profile.ID {
		return ensure(false, "catalog must preserve v1 and append v2 in order")
	}
	leaseV1After, err := getLease(harness, leaseV1.ID)
	if err != nil {
		return err
	}
	if leaseV1After.Explanation == nil || leaseV1After.Explanation.Capability == nil {
		return ensure(false, "upgraded catalog must not erase the lease capability trace")
	}
	if leaseV1After.Explanation.Capability.ProfileVersion != 1 ||
		leaseV1After.Explanation.Capability.ProfileID != registeredA.Profile.ID {
		return ensure(false, "catalog upgrade must not retroactively rewrite existing lease explanations")
	}
	if containsStringValue(leaseV1After.Explanation.Capability.Tags, "ga") {
		return ensure(false, "lease trace must keep the v1 tag snapshot")
	}

	// 6. 能力不足与无节点必须可区分：
	//    区域内有 3 个 ready 候选但无人满足条件 -> capability_insufficient。
	impossible := &domain.CapabilityMatch{
		RequiredProtocolVersions: []string{"doq"},
		MinimumCapacityTier:      "xlarge",
		RequiredTags:             []string{"ga"},
	}
	if _, err := allocateWithCapability(harness, plan.ID, "cap-lease-insufficient-1", "cap-east", 10, impossible); err != nil {
		if err := expectFailure(err, http.StatusUnprocessableEntity, string(domain.CodeCapabilityInsufficient)); err != nil {
			return err
		}
	} else {
		return ensure(false, "expected capability_insufficient but allocation succeeded")
	}
	//    区域内没有任何 ready 节点（cap-north 从未有节点存在）。
	if _, err := allocateWithCapability(harness, plan.ID, "cap-lease-nonode-1", "cap-north", 10, dohMatch); err != nil {
		if err := expectFailure(err, http.StatusConflict, string(domain.CodeNodeUnavailable)); err != nil {
			return err
		}
	} else {
		return ensure(false, "expected node_unavailable but allocation succeeded")
	}

	// 7. 幂等：相同请求键+相同能力条件重放返回原组；改条件返回冲突。
	replay, err := allocateWithCapability(harness, plan.ID, "cap-lease-match-v1-001", "cap-east", 10, dohMatch)
	if err != nil {
		return err
	}
	if replay.ID != groupV1.ID {
		return ensure(false, "capability allocation replay returned another group")
	}
	changed := &domain.CapabilityMatch{
		RequiredProtocolVersions: []string{"doq"},
		MinimumCapacityTier:      "large",
		RequiredTags:             []string{"edge"},
	}
	if _, err := allocateWithCapability(harness, plan.ID, "cap-lease-match-v1-001", "cap-east", 10, changed); err != nil {
		if err := expectFailure(err, http.StatusConflict, string(domain.CodeIdempotencyConflict)); err != nil {
			return err
		}
	} else {
		return ensure(false, "same key with different capability conditions must conflict")
	}

	// 8. 并发：相同请求键同内容并发重放必须全部返回同一个组，不产生
	//    重复租约。此时节点 A 已是 v2（doh-http2/xlarge/ga），使用 v2
	//    满足而 B 与旧节点不满足的条件，确保只有 A 能被选中。
	if err := releaseLeaseByID(harness, groupV1.LeaseIDs[0]); err != nil {
		return err
	}
	concurrentMatch := &domain.CapabilityMatch{
		RequiredProtocolVersions: []string{"doh-http2"},
		MinimumCapacityTier:      "xlarge",
		RequiredTags:             []string{"ga"},
	}
	const concurrency = 8
	var (
		wg           sync.WaitGroup
		mu           sync.Mutex
		groupIDs     = make(map[string]struct{})
		failures     []string
		successCount int
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			group, allocErr := allocateWithCapability(harness, plan.ID, "cap-lease-concurrent-1", "cap-east", 10, concurrentMatch)
			mu.Lock()
			defer mu.Unlock()
			if allocErr != nil {
				failures = append(failures, allocErr.Error())
				return
			}
			successCount++
			groupIDs[group.ID] = struct{}{}
		}()
	}
	wg.Wait()
	if len(failures) != 0 || successCount != concurrency {
		return ensure(false, fmt.Sprintf("concurrent identical allocations must all replay, failures=%v", failures))
	}
	if len(groupIDs) != 1 {
		return ensure(false, fmt.Sprintf("concurrent same-key allocations must resolve to one group, got %d", len(groupIDs)))
	}
	// 通过一次重放取回该组，确认只有一个租约、节点仍唯一占用。
	concurrentGroup, err := allocateWithCapability(harness, plan.ID, "cap-lease-concurrent-1", "cap-east", 10, concurrentMatch)
	if err != nil {
		return err
	}
	if len(concurrentGroup.LeaseIDs) != 1 {
		return ensure(false, fmt.Sprintf("concurrent allocation must create one lease, got %d", len(concurrentGroup.LeaseIDs)))
	}

	// 8b. 不同请求键的并发分配也必须确定：仅一个满足条件的节点，因此
	// 恰好一个请求成功，其余全部得到 capability_insufficient，绝不出现
	// 同一节点被重复占用。先释放 8 持有的租约，让 A 重新成为唯一候选。
	if err := releaseLeaseByID(harness, concurrentGroup.LeaseIDs[0]); err != nil {
		return err
	}
	var distinctWG sync.WaitGroup
	var distinctMu sync.Mutex
	createdGroups := make(map[string]struct{})
	insufficientCount := 0
	for i := 0; i < concurrency; i++ {
		distinctWG.Add(1)
		go func(index int) {
			defer distinctWG.Done()
			distinctGroup, distinctErr := allocateWithCapability(
				harness, plan.ID, fmt.Sprintf("cap-lease-distinct-%02d", index),
				"cap-east", 10, concurrentMatch)
			distinctMu.Lock()
			defer distinctMu.Unlock()
			if distinctErr == nil {
				createdGroups[distinctGroup.ID] = struct{}{}
				return
			}
			var failure *apiFailure
			if errors.As(distinctErr, &failure) && failure.Code == string(domain.CodeCapabilityInsufficient) {
				insufficientCount++
			}
		}(i)
	}
	distinctWG.Wait()
	if len(createdGroups) != 1 || insufficientCount != concurrency-1 {
		return ensure(false, fmt.Sprintf(
			"concurrent distinct-key allocations must create one group and %d insufficiencies, got %d/%d",
			concurrency-1, len(createdGroups), insufficientCount))
	}

	// 9. 并发登记：相同内容并发提交至多产生一个新版本；不同内容各自
	//    产生确定的新版本号。
	var registerWG sync.WaitGroup
	registerErrs := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		registerWG.Add(1)
		go func() {
			defer registerWG.Done()
			_, registerErr := registerCapability(harness, nodeB.ID,
				[]string{" doq ", "dot"}, "large", []string{"edge"})
			registerErrs <- registerErr
		}()
	}
	registerWG.Wait()
	close(registerErrs)
	for registerErr := range registerErrs {
		if registerErr != nil {
			return registerErr
		}
	}
	profilesB, err := listCapabilities(harness, nodeB.ID)
	if err != nil {
		return err
	}
	if len(profilesB) != 2 || profilesB[1].ProfileVersion != 2 {
		return ensure(false, fmt.Sprintf("concurrent identical registrations must create exactly v2, got %d versions", len(profilesB)))
	}

	// 10. 未知节点登记能力返回 not_found。
	if _, err := registerCapability(harness, "nod-does-not-exist",
		[]string{"dns-tcp"}, "small", nil); err != nil {
		if err := expectFailure(err, http.StatusNotFound, string(domain.CodeNotFound)); err != nil {
			return err
		}
	} else {
		return ensure(false, "expected not_found for unknown node capability registration")
	}
	return nil
}

func findLeaseForNode(harness *harness, leaseIDs []string, nodeID string) (domain.NodeLease, error) {
	for _, leaseID := range leaseIDs {
		lease, err := getLease(harness, leaseID)
		if err != nil {
			return domain.NodeLease{}, err
		}
		if lease.NodeID == nodeID {
			return lease, nil
		}
	}
	return domain.NodeLease{}, ensure(false, "no lease found for node "+nodeID)
}

func containsStringValue(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
