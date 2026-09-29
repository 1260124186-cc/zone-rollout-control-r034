package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxCapabilityTokenRunes = 48
	MaxProtocols            = 32
	MaxCapacityTiers        = 32
	MaxBusinessTags         = 32
	MaxProfileTags          = 16
	MaxProfileProtocols     = 16
	MaxRequireTags          = 16
	MaxRequireProtocols     = 16
)

// CapabilityCatalog is an immutable, versioned capability directory. Each
// version defines the complete vocabulary of protocol versions, ordered
// capacity tiers and business tags that node profiles may declare.
type CapabilityCatalog struct {
	Version       uint64    `json:"version"`
	Protocols     []string  `json:"protocols"`
	CapacityTiers []string  `json:"capacity_tiers"`
	BusinessTags  []string  `json:"business_tags"`
	Fingerprint   string    `json:"fingerprint"`
	RequestKey    string    `json:"request_key,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// NodeCapabilityProfile is a node's declared capability set against one exact
// catalog version. It is immutable: re-registration with different content is
// rejected instead of mutating the declaration, so historical allocations keep
// a stable explanation.
type NodeCapabilityProfile struct {
	NodeID           string    `json:"node_id"`
	CatalogVersion   uint64    `json:"catalog_version"`
	ProtocolVersions []string  `json:"protocol_versions"`
	CapacityTier     string    `json:"capacity_tier"`
	BusinessTags     []string  `json:"business_tags"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// NodeCapabilitySet groups every profile declared by one node across catalog
// versions. Keyed by catalog version.
type NodeCapabilitySet struct {
	NodeID   string                           `json:"node_id"`
	Profiles map[uint64]NodeCapabilityProfile `json:"profiles"`
}

type CatalogPublishInput struct {
	RequestKey    string   `json:"request_key"`
	Protocols     []string `json:"protocols"`
	CapacityTiers []string `json:"capacity_tiers"`
	BusinessTags  []string `json:"business_tags"`
}

type NodeCapabilityInput struct {
	CatalogVersion   uint64   `json:"catalog_version"`
	ProtocolVersions []string `json:"protocol_versions"`
	CapacityTier     string   `json:"capacity_tier"`
	BusinessTags     []string `json:"business_tags"`
}

// CapabilityRequirement narrows allocation to nodes that declared a capability
// profile against the exact catalog version and satisfy every condition.
type CapabilityRequirement struct {
	CatalogVersion  uint64   `json:"catalog_version"`
	Protocols       []string `json:"protocols,omitempty"`
	MinCapacityTier string   `json:"min_capacity_tier,omitempty"`
	BusinessTags    []string `json:"business_tags,omitempty"`
}

// LeaseCapability is the immutable capability explanation captured on a lease
// when capability matching was used. Catalog upgrades can never rewrite it.
type LeaseCapability struct {
	CatalogVersion   uint64   `json:"catalog_version"`
	ProtocolVersions []string `json:"protocol_versions"`
	CapacityTier     string   `json:"capacity_tier"`
	BusinessTags     []string `json:"business_tags"`
}

// LeaseSelection explains why a node was selected for a lease group.
type LeaseSelection struct {
	LeaseID    string           `json:"lease_id"`
	NodeID     string           `json:"node_id"`
	Capability *LeaseCapability `json:"capability,omitempty"`
}

// NormalizeCapabilityTokens lower-cases, trims, de-duplicates and sorts a set
// of capability vocabulary tokens (protocols or business tags).
func NormalizeCapabilityTokens(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		token := strings.TrimSpace(strings.ToLower(value))
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		normalized = append(normalized, token)
	}
	sort.Strings(normalized)
	return normalized
}

// NormalizeCapacityTiers trims and lower-cases tiers while preserving order,
// because the position in the list defines the capacity rank.
func NormalizeCapacityTiers(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		token := strings.TrimSpace(strings.ToLower(value))
		if token == "" {
			continue
		}
		normalized = append(normalized, token)
	}
	return normalized
}

func validateCapabilityToken(token string) bool {
	count := utf8.RuneCountInString(token)
	if count < 1 || count > MaxCapabilityTokenRunes {
		return false
	}
	for _, r := range token {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func NormalizeCatalogInput(input CatalogPublishInput) CatalogPublishInput {
	input.RequestKey = strings.TrimSpace(input.RequestKey)
	input.Protocols = NormalizeCapabilityTokens(input.Protocols)
	input.CapacityTiers = NormalizeCapacityTiers(input.CapacityTiers)
	input.BusinessTags = NormalizeCapabilityTokens(input.BusinessTags)
	return input
}

func ValidateCatalogInput(input CatalogPublishInput) error {
	details := make(map[string]any)
	if err := validateRequestKey(input.RequestKey); err != nil {
		details["request_key"] = err.Error()
	}
	if len(input.Protocols) < 1 || len(input.Protocols) > MaxProtocols {
		details["protocols"] = "must contain between 1 and 32 items"
	}
	for _, protocol := range input.Protocols {
		if !validateCapabilityToken(protocol) {
			details["protocols"] = "items must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
			break
		}
	}
	if len(input.CapacityTiers) < 1 || len(input.CapacityTiers) > MaxCapacityTiers {
		details["capacity_tiers"] = "must contain between 1 and 32 items"
	}
	tiers := make(map[string]struct{}, len(input.CapacityTiers))
	for _, tier := range input.CapacityTiers {
		if !validateCapabilityToken(tier) {
			details["capacity_tiers"] = "items must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
			break
		}
		if _, exists := tiers[tier]; exists {
			details["capacity_tiers"] = "must not contain duplicates"
			break
		}
		tiers[tier] = struct{}{}
	}
	if len(input.BusinessTags) < 1 || len(input.BusinessTags) > MaxBusinessTags {
		details["business_tags"] = "must contain between 1 and 32 items"
	}
	for _, tag := range input.BusinessTags {
		if !validateCapabilityToken(tag) {
			details["business_tags"] = "items must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
			break
		}
	}
	if len(details) > 0 {
		return Validation("capability catalog input is invalid", details)
	}
	return nil
}

func CatalogFingerprint(input CatalogPublishInput) string {
	payload := fmt.Sprintf(
		"%s|%s|%s",
		strings.Join(input.Protocols, ","),
		strings.Join(input.CapacityTiers, ","),
		strings.Join(input.BusinessTags, ","),
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// NormalizeNodeCapabilityInput normalizes profile declarations.
func NormalizeNodeCapabilityInput(input NodeCapabilityInput) NodeCapabilityInput {
	input.ProtocolVersions = NormalizeCapabilityTokens(input.ProtocolVersions)
	input.CapacityTier = strings.TrimSpace(strings.ToLower(input.CapacityTier))
	input.BusinessTags = NormalizeCapabilityTokens(input.BusinessTags)
	return input
}

// ValidateNodeCapabilityInput checks shape only. Membership against the
// referenced catalog is validated inside the transactional service.
func ValidateNodeCapabilityInput(input NodeCapabilityInput) error {
	details := make(map[string]any)
	if input.CatalogVersion == 0 {
		details["catalog_version"] = "must be greater than zero"
	}
	if len(input.ProtocolVersions) > MaxProfileProtocols {
		details["protocol_versions"] = "must contain at most 16 items"
	}
	for _, protocol := range input.ProtocolVersions {
		if !validateCapabilityToken(protocol) {
			details["protocol_versions"] = "items must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
			break
		}
	}
	if input.CapacityTier == "" {
		details["capacity_tier"] = "is required"
	} else if !validateCapabilityToken(input.CapacityTier) {
		details["capacity_tier"] = "must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
	}
	if len(input.BusinessTags) > MaxProfileTags {
		details["business_tags"] = "must contain at most 16 items"
	}
	for _, tag := range input.BusinessTags {
		if !validateCapabilityToken(tag) {
			details["business_tags"] = "items must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
			break
		}
	}
	if len(details) > 0 {
		return Validation("node capability input is invalid", details)
	}
	return nil
}

// ValidateNodeCapabilityAgainstCatalog verifies the declared profile only uses
// vocabulary defined by the referenced catalog version.
func ValidateNodeCapabilityAgainstCatalog(input NodeCapabilityInput, catalog CapabilityCatalog) error {
	details := make(map[string]any)
	protocols := make(map[string]struct{}, len(catalog.Protocols))
	for _, protocol := range catalog.Protocols {
		protocols[protocol] = struct{}{}
	}
	for _, protocol := range input.ProtocolVersions {
		if _, ok := protocols[protocol]; !ok {
			details["protocol_versions"] = "must be declared in the referenced capability catalog"
			break
		}
	}
	tierRank := -1
	for index, tier := range catalog.CapacityTiers {
		if tier == input.CapacityTier {
			tierRank = index
			break
		}
	}
	if tierRank < 0 {
		details["capacity_tier"] = "must be declared in the referenced capability catalog"
	}
	tags := make(map[string]struct{}, len(catalog.BusinessTags))
	for _, tag := range catalog.BusinessTags {
		tags[tag] = struct{}{}
	}
	for _, tag := range input.BusinessTags {
		if _, ok := tags[tag]; !ok {
			details["business_tags"] = "must be declared in the referenced capability catalog"
			break
		}
	}
	if len(details) > 0 {
		return Validation("node capability is not covered by the catalog", details)
	}
	return nil
}

// NormalizeCapabilityRequirement canonicalizes an allocation requirement.
func NormalizeCapabilityRequirement(requirement *CapabilityRequirement) *CapabilityRequirement {
	if requirement == nil {
		return nil
	}
	normalized := *requirement
	normalized.Protocols = NormalizeCapabilityTokens(requirement.Protocols)
	normalized.MinCapacityTier = strings.TrimSpace(strings.ToLower(requirement.MinCapacityTier))
	normalized.BusinessTags = NormalizeCapabilityTokens(requirement.BusinessTags)
	return &normalized
}

// ValidateCapabilityRequirementShape checks non-membership constraints.
func ValidateCapabilityRequirementShape(requirement CapabilityRequirement) error {
	details := make(map[string]any)
	if requirement.CatalogVersion == 0 {
		details["catalog_version"] = "must be greater than zero when capability matching is requested"
	}
	if len(requirement.Protocols) > MaxRequireProtocols {
		details["protocols"] = "must contain at most 16 items"
	}
	for _, protocol := range requirement.Protocols {
		if !validateCapabilityToken(protocol) {
			details["protocols"] = "items must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
			break
		}
	}
	if requirement.MinCapacityTier != "" && !validateCapabilityToken(requirement.MinCapacityTier) {
		details["min_capacity_tier"] = "must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
	}
	if len(requirement.BusinessTags) > MaxRequireTags {
		details["business_tags"] = "must contain at most 16 items"
	}
	for _, tag := range requirement.BusinessTags {
		if !validateCapabilityToken(tag) {
			details["business_tags"] = "items must be 1 to 48 lowercase letters, digits, hyphens, or underscores"
			break
		}
	}
	if len(details) > 0 {
		return Validation("capability requirement is invalid", details)
	}
	return nil
}

// ValidateRequirementAgainstCatalog ensures a request only references catalog
// vocabulary, so allocation never silently matches nothing due to a typo.
func ValidateRequirementAgainstCatalog(requirement CapabilityRequirement, catalog CapabilityCatalog) error {
	details := make(map[string]any)
	protocols := make(map[string]struct{}, len(catalog.Protocols))
	for _, protocol := range catalog.Protocols {
		protocols[protocol] = struct{}{}
	}
	for _, protocol := range requirement.Protocols {
		if _, ok := protocols[protocol]; !ok {
			details["protocols"] = "must be declared in the referenced capability catalog"
			break
		}
	}
	if requirement.MinCapacityTier != "" {
		found := false
		for _, tier := range catalog.CapacityTiers {
			if tier == requirement.MinCapacityTier {
				found = true
				break
			}
		}
		if !found {
			details["min_capacity_tier"] = "must be declared in the referenced capability catalog"
		}
	}
	tags := make(map[string]struct{}, len(catalog.BusinessTags))
	for _, tag := range catalog.BusinessTags {
		tags[tag] = struct{}{}
	}
	for _, tag := range requirement.BusinessTags {
		if _, ok := tags[tag]; !ok {
			details["business_tags"] = "must be declared in the referenced capability catalog"
			break
		}
	}
	if len(details) > 0 {
		return Validation("capability requirement is not covered by the catalog", details)
	}
	return nil
}

// ProfileSatisfies reports whether a declared node profile meets every explicit
// capability condition. Tier ranks must be resolved against the catalog order
// (higher index means more capacity). Empty condition fields impose no
// constraint beyond the exact catalog version.
func ProfileSatisfies(
	profile NodeCapabilityProfile,
	requirement CapabilityRequirement,
	tierRanks map[string]int,
) bool {
	if profile.CatalogVersion != requirement.CatalogVersion {
		return false
	}
	supported := make(map[string]struct{}, len(profile.ProtocolVersions))
	for _, protocol := range profile.ProtocolVersions {
		supported[protocol] = struct{}{}
	}
	for _, required := range requirement.Protocols {
		if _, ok := supported[required]; !ok {
			return false
		}
	}
	if requirement.MinCapacityTier != "" {
		requestedRank, requestedOK := tierRanks[requirement.MinCapacityTier]
		declaredRank, declaredOK := tierRanks[profile.CapacityTier]
		if !requestedOK || !declaredOK || declaredRank < requestedRank {
			return false
		}
	}
	tagSet := make(map[string]struct{}, len(profile.BusinessTags))
	for _, tag := range profile.BusinessTags {
		tagSet[tag] = struct{}{}
	}
	for _, required := range requirement.BusinessTags {
		if _, ok := tagSet[required]; !ok {
			return false
		}
	}
	return true
}
