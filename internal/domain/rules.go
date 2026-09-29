package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func ActivatePlan(plan *RolloutPlan, now time.Time) error {
	if plan.State != PlanDraft {
		return InvalidState("plan can be activated only from draft", map[string]any{
			"plan_id": plan.ID,
			"state":   plan.State,
		})
	}
	if plan.LatestRevisionID == "" {
		return InvalidState("plan needs a zone revision before activation", map[string]any{
			"plan_id": plan.ID,
		})
	}
	plan.State = PlanActive
	plan.ActivatedAt = timePointer(now.UTC())
	plan.Version++
	return nil
}

func CompletePlan(plan *RolloutPlan, now time.Time) error {
	if plan.State != PlanActive {
		return InvalidState("plan can be completed only from active", map[string]any{
			"plan_id": plan.ID,
			"state":   plan.State,
		})
	}
	plan.State = PlanCompleted
	plan.CompletedAt = timePointer(now.UTC())
	plan.Version++
	return nil
}

func LeaseFingerprint(planID string, input LeaseInput) string {
	payload := fmt.Sprintf(
		"%s|%s|%s|%d|%d|%s",
		planID,
		input.RequestKey,
		input.Region,
		input.LeaseSeconds,
		input.MaxNodes,
		CapabilityMatchFingerprint(input.Capability),
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// CapabilityMatchFingerprint 生成能力条件的稳定指纹；空条件得到固定空串，
// 使不提出能力条件的请求与历史行为完全一致。
func CapabilityMatchFingerprint(match *CapabilityMatch) string {
	if match == nil {
		return ""
	}
	payload := strings.Join(match.RequiredProtocolVersions, ",") +
		"|" + match.MinimumCapacityTier +
		"|" + strings.Join(match.RequiredTags, ",")
	if payload == "||" {
		return ""
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// CapabilityProfileFingerprint 生成档案内容指纹，用于重复登记的确定性去重。
func CapabilityProfileFingerprint(input CapabilityProfileInput) string {
	payload := strings.Join(input.ProtocolVersions, ",") +
		"|" + input.CapacityTier +
		"|" + strings.Join(input.Tags, ",")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// ProfileSatisfies 是纯领域匹配：声明档案是否满足给定能力条件。
// 调用方负责先完成规范化与校验。
func ProfileSatisfies(profile NodeCapabilityProfile, match CapabilityMatch) bool {
	for _, required := range match.RequiredProtocolVersions {
		if !containsString(profile.ProtocolVersions, required) {
			return false
		}
	}
	if match.MinimumCapacityTier != "" {
		requiredRank, _ := CapacityRank(CapacityTier(match.MinimumCapacityTier))
		actualRank, ok := CapacityRank(profile.CapacityTier)
		if !ok || actualRank < requiredRank {
			return false
		}
	}
	for _, required := range match.RequiredTags {
		if !containsString(profile.Tags, required) {
			return false
		}
	}
	return true
}

// MatchedConditions 按稳定顺序返回该档案满足条件时实际命中的条件代码。
func MatchedConditions(profile NodeCapabilityProfile, match CapabilityMatch) []string {
	conditions := make([]string, 0)
	for _, required := range match.RequiredProtocolVersions {
		if containsString(profile.ProtocolVersions, required) {
			conditions = append(conditions, "protocol:"+required)
		}
	}
	if match.MinimumCapacityTier != "" {
		if requiredRank, ok := CapacityRank(CapacityTier(match.MinimumCapacityTier)); ok {
			if actualRank, found := CapacityRank(profile.CapacityTier); found && actualRank >= requiredRank {
				conditions = append(conditions, "capacity_tier>="+match.MinimumCapacityTier)
			}
		}
	}
	for _, required := range match.RequiredTags {
		if containsString(profile.Tags, required) {
			conditions = append(conditions, "tag:"+required)
		}
	}
	return conditions
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func ProbeFingerprint(leaseID string, input ProbeInput) string {
	payload := fmt.Sprintf(
		"%s|%s|%t|%d|%d|%s",
		leaseID,
		input.RequestKey,
		input.Success,
		input.ObservedSerial,
		input.LatencyMS,
		input.ObservedAt.UTC().Format(time.RFC3339Nano),
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func BuildAssessment(
	id string,
	plan RolloutPlan,
	revision ZoneRevision,
	probes []Probe,
	now time.Time,
) RolloutAssessment {
	reasons := make(map[string]struct{})
	if len(probes) == 0 {
		reasons["no_probes"] = struct{}{}
	}
	probeCount := len(probes)
	successCount := 0
	maximumLatency := 0
	nodes := make(map[string]struct{})
	for _, probe := range probes {
		nodes[probe.NodeID] = struct{}{}
		if probe.LatencyMS > maximumLatency {
			maximumLatency = probe.LatencyMS
		}
		if probe.Success {
			successCount++
			if probe.ObservedSerial != revision.Serial {
				reasons["serial_mismatch"] = struct{}{}
			}
		}
	}
	if len(nodes) < plan.ExpectedNodes {
		reasons["node_not_observed"] = struct{}{}
	}
	successRatio := 0.0
	if probeCount > 0 {
		successRatio = round(float64(successCount)/float64(probeCount), 6)
	}
	if successRatio < plan.MinimumSuccessRatio {
		reasons["success_ratio_below_minimum"] = struct{}{}
	}
	if maximumLatency > plan.MaximumLatencyMS {
		reasons["latency_limit_exceeded"] = struct{}{}
	}
	reasonList := make([]string, 0, len(reasons))
	for reason := range reasons {
		reasonList = append(reasonList, reason)
	}
	sort.Strings(reasonList)
	verdict := VerdictConsistent
	if len(reasonList) > 0 {
		verdict = VerdictReviewRequired
	}
	return RolloutAssessment{
		ID:               id,
		PlanID:           plan.ID,
		RevisionID:       revision.ID,
		Verdict:          verdict,
		Reasons:          reasonList,
		ProbeCount:       probeCount,
		SuccessCount:     successCount,
		SuccessRatio:     successRatio,
		MaximumLatencyMS: maximumLatency,
		CreatedAt:        now.UTC(),
	}
}

func round(value float64, places int) float64 {
	scale := math.Pow10(places)
	return math.Round(value*scale) / scale
}

func timePointer(value time.Time) *time.Time {
	return &value
}
