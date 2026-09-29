package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"solo-0014-zone-rollout-control/internal/domain"
)

func (s *Server) publishCapabilityCatalog(w http.ResponseWriter, r *http.Request) {
	var input domain.CatalogPublishInput
	if err := decode(w, r, &input); err != nil {
		sendError(w, r, err)
		return
	}
	catalog, err := s.capabilities.PublishCatalog(r.Context(), input)
	if err != nil {
		sendError(w, r, err)
		return
	}
	sendData(w, r, http.StatusCreated, catalog)
}

func (s *Server) latestCapabilityCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.capabilities.GetCatalog(r.Context(), 0)
	if err != nil {
		sendError(w, r, err)
		return
	}
	sendData(w, r, http.StatusOK, catalog)
}

func (s *Server) getCapabilityCatalog(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.PathValue("version"))
	version, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || version == 0 {
		sendError(w, r, domain.Validation("catalog version must be a positive integer", map[string]any{
			"version": raw,
		}))
		return
	}
	catalog, getErr := s.capabilities.GetCatalog(r.Context(), version)
	if getErr != nil {
		sendError(w, r, getErr)
		return
	}
	sendData(w, r, http.StatusOK, catalog)
}

func (s *Server) registerNodeCapability(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	var input domain.NodeCapabilityInput
	if err := decode(w, r, &input); err != nil {
		sendError(w, r, err)
		return
	}
	profile, err := s.capabilities.RegisterProfile(r.Context(), nodeID, input)
	if err != nil {
		sendError(w, r, err)
		return
	}
	sendData(w, r, http.StatusCreated, profile)
}

func (s *Server) getNodeCapabilities(w http.ResponseWriter, r *http.Request) {
	result, err := s.capabilities.GetNodeCapabilities(r.Context(), r.PathValue("id"))
	if err != nil {
		sendError(w, r, err)
		return
	}
	sendData(w, r, http.StatusOK, result)
}
