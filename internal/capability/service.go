// Package capability 管理版本化的节点能力档案目录。
//
// 档案只追加：为同一节点重复登记完全相同的内容时返回既有版本（确定性
// 去重）；登记不同内容时追加新版本。目录升级不会改写旧版本，也不会
// 追溯修改任何已经创建的租约。
package capability

import (
	"context"

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

// RegisterResult 描述一次登记的确定结果。
type RegisterResult struct {
	Profile domain.NodeCapabilityProfile `json:"profile"`
	// Created 为 true 表示产生了新版本，false 表示内容去重后返回既有版本。
	Created bool `json:"created"`
}

// Register 为节点登记能力档案。相同内容重复登记返回既有版本；
// 不同内容追加新版本。并发登记由存储写锁串行化，结果确定。
func (s *Service) Register(
	ctx context.Context,
	nodeID string,
	input domain.CapabilityProfileInput,
) (RegisterResult, error) {
	normalized := domain.NormalizeCapabilityProfile(input)
	if err := domain.ValidateCapabilityProfile(normalized); err != nil {
		return RegisterResult{}, err
	}
	fingerprint := domain.CapabilityProfileFingerprint(normalized)
	now := s.clock.Now().UTC()
	var result RegisterResult

	err := s.store.Write(ctx, func(state *domain.State) error {
		if _, ok := state.Nodes[nodeID]; !ok {
			return domain.NotFound("node", nodeID)
		}
		profiles := state.NodeCapabilities[nodeID]
		for _, profile := range profiles {
			if profile.ContentFingerprint == fingerprint {
				result = RegisterResult{Profile: profile, Created: false}
				return nil
			}
		}

		version := uint64(len(profiles) + 1)
		profile := domain.NodeCapabilityProfile{
			ID:                 s.ids.New("cap"),
			NodeID:             nodeID,
			ProfileVersion:     version,
			ProtocolVersions:   append([]string(nil), normalized.ProtocolVersions...),
			CapacityTier:       domain.CapacityTier(normalized.CapacityTier),
			Tags:               append([]string(nil), normalized.Tags...),
			ContentFingerprint: fingerprint,
			CreatedAt:          now,
		}
		state.NodeCapabilities[nodeID] = append(profiles, profile)
		store.AddAudit(state, s.ids, "node_capability", profile.ID, "registered", now, map[string]any{
			"node_id":         nodeID,
			"profile_version": version,
			"deduped":         false,
		})
		result = RegisterResult{Profile: profile, Created: true}
		return nil
	})
	return result, err
}

// ListByNode 返回某节点按版本号升序排列的全部能力档案版本。
func (s *Service) ListByNode(ctx context.Context, nodeID string) ([]domain.NodeCapabilityProfile, error) {
	profiles := []domain.NodeCapabilityProfile{}
	err := s.store.Read(ctx, func(state *domain.State) error {
		if _, ok := state.Nodes[nodeID]; !ok {
			return domain.NotFound("node", nodeID)
		}
		for _, profile := range state.NodeCapabilities[nodeID] {
			profiles = append(profiles, profile)
		}
		return nil
	})
	return profiles, err
}

// LatestProfile 返回节点当前生效（最新）的能力档案；从未声明能力时
// 返回 false。仅供存储事务内部调用时直接读取 state。
func LatestProfile(state *domain.State, nodeID string) (domain.NodeCapabilityProfile, bool) {
	profiles := state.NodeCapabilities[nodeID]
	if len(profiles) == 0 {
		return domain.NodeCapabilityProfile{}, false
	}
	return profiles[len(profiles)-1], true
}
