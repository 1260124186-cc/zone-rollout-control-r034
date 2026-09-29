package httpapi

import (
	"net/http"

	"solo-0014-zone-rollout-control/internal/domain"
)

// registerNodeCapability 为节点登记版本化能力档案。
// 相同内容重复登记返回既有版本（200），新内容产生新版本（201）。
func (s *Server) registerNodeCapability(w http.ResponseWriter, r *http.Request) {
	var input domain.CapabilityProfileInput
	if err := decode(w, r, &input); err != nil {
		sendError(w, r, err)
		return
	}
	result, err := s.capabilities.Register(r.Context(), r.PathValue("id"), input)
	if err != nil {
		sendError(w, r, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	sendData(w, r, status, result)
}

// listNodeCapabilities 返回节点按版本升序排列的全部能力档案。
func (s *Server) listNodeCapabilities(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.capabilities.ListByNode(r.Context(), r.PathValue("id"))
	if err != nil {
		sendError(w, r, err)
		return
	}
	sendData(w, r, http.StatusOK, map[string]any{"items": profiles})
}

// getLease 返回单个租约及其固化的分配解释。
func (s *Server) getLease(w http.ResponseWriter, r *http.Request) {
	lease, err := s.allocations.GetLease(r.Context(), r.PathValue("id"))
	if err != nil {
		sendError(w, r, err)
		return
	}
	sendData(w, r, http.StatusOK, lease)
}
