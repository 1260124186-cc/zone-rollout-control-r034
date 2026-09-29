package domain

import (
	"net"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxZoneRunes       = 253
	MaxAuthorityRunes  = 253
	MaxNodeNameRunes   = 180
	MaxRegionRunes     = 48
	MinRequestKeyRunes = 8
	MaxRequestKeyRunes = 64
	MaxEntries         = 500
	MaxProtocols       = 10
	MaxCapabilityTags  = 20
	MaxTagRunes        = 48
)

type PlanCreateInput struct {
	Zone                string  `json:"zone"`
	MinimumSuccessRatio float64 `json:"minimum_success_ratio"`
	MaximumLatencyMS    int     `json:"maximum_latency_ms"`
	ExpectedNodes       int     `json:"expected_nodes"`
}

type RevisionCreateInput struct {
	Serial    uint32      `json:"serial"`
	Authority string      `json:"authority"`
	Entries   []ZoneEntry `json:"entries"`
}

type NodeCreateInput struct {
	NodeName   string `json:"node_name"`
	Region     string `json:"region"`
	Quarantine bool   `json:"quarantine"`
}

type LeaseInput struct {
	RequestKey   string           `json:"request_key"`
	Region       string           `json:"region"`
	LeaseSeconds int              `json:"lease_seconds"`
	MaxNodes     int              `json:"max_nodes"`
	Capability   *CapabilityMatch `json:"capability,omitempty"`
}

// CapabilityProfileInput 是节点能力档案登记请求。
type CapabilityProfileInput struct {
	ProtocolVersions []string `json:"protocol_versions"`
	CapacityTier     string   `json:"capacity_tier"`
	Tags             []string `json:"tags"`
}

type ProbeInput struct {
	RequestKey     string    `json:"request_key"`
	Success        bool      `json:"success"`
	ObservedSerial uint32    `json:"observed_serial"`
	LatencyMS      int       `json:"latency_ms"`
	ObservedAt     time.Time `json:"observed_at"`
}

type AssessmentInput struct {
	RequestKey string `json:"request_key"`
}

func NormalizePlanInput(input PlanCreateInput) PlanCreateInput {
	input.Zone = canonicalName(input.Zone)
	return input
}

func ValidatePlanInput(input PlanCreateInput) error {
	input = NormalizePlanInput(input)
	details := make(map[string]any)
	if err := validateCanonicalName(input.Zone); err != nil {
		details["zone"] = err.Error()
	}
	if input.MinimumSuccessRatio < 0 || input.MinimumSuccessRatio > 1 {
		details["minimum_success_ratio"] = "must be between 0 and 1"
	}
	if input.MaximumLatencyMS < 1 || input.MaximumLatencyMS > 600000 {
		details["maximum_latency_ms"] = "must be between 1 and 600000"
	}
	if input.ExpectedNodes < 1 || input.ExpectedNodes > 500 {
		details["expected_nodes"] = "must be between 1 and 500"
	}
	if len(details) > 0 {
		return Validation("plan input is invalid", details)
	}
	return nil
}

func NormalizeRevisionInput(input RevisionCreateInput) (RevisionCreateInput, error) {
	input.Authority = canonicalName(input.Authority)
	entries := make([]ZoneEntry, 0, len(input.Entries))
	for _, entry := range input.Entries {
		owner := canonicalName(entry.Owner)
		entryType := EntryType(strings.ToUpper(strings.TrimSpace(string(entry.Type))))
		value := strings.TrimSpace(entry.Value)
		switch entryType {
		case EntryA:
			parsed := net.ParseIP(value)
			if parsed == nil || parsed.To4() == nil {
				return RevisionCreateInput{}, Validation("zone entry is invalid", map[string]any{
					"owner": owner,
					"type":  entryType,
					"value": "must be a valid IPv4 address",
				})
			}
			value = parsed.To4().String()
		case EntryAAAA:
			parsed := net.ParseIP(value)
			if parsed == nil || parsed.To4() != nil {
				return RevisionCreateInput{}, Validation("zone entry is invalid", map[string]any{
					"owner": owner,
					"type":  entryType,
					"value": "must be a valid IPv6 address",
				})
			}
			value = parsed.String()
		case EntryCNAME:
			value = canonicalName(value)
			if err := validateCanonicalName(value); err != nil {
				return RevisionCreateInput{}, Validation("zone entry is invalid", map[string]any{
					"owner": owner,
					"type":  entryType,
					"value": err.Error(),
				})
			}
		default:
			return RevisionCreateInput{}, Validation("zone entry is invalid", map[string]any{
				"owner": owner,
				"type":  entry.Type,
				"value": "type must be A, AAAA, or CNAME",
			})
		}
		if err := validateCanonicalName(owner); err != nil {
			return RevisionCreateInput{}, Validation("zone entry is invalid", map[string]any{
				"owner": owner,
				"value": err.Error(),
			})
		}
		entries = append(entries, ZoneEntry{Owner: owner, Type: entryType, Value: value})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Owner != entries[j].Owner {
			return entries[i].Owner < entries[j].Owner
		}
		if entries[i].Type != entries[j].Type {
			return entries[i].Type < entries[j].Type
		}
		return entries[i].Value < entries[j].Value
	})
	for index := 1; index < len(entries); index++ {
		if entries[index] == entries[index-1] {
			return RevisionCreateInput{}, Validation("zone entry is duplicated", map[string]any{
				"owner": entries[index].Owner,
				"type":  entries[index].Type,
				"value": entries[index].Value,
			})
		}
	}
	input.Entries = entries
	return input, nil
}

func ValidateRevisionInput(input RevisionCreateInput) error {
	if input.Serial == 0 {
		return Validation("revision input is invalid", map[string]any{
			"serial": "must be greater than zero",
		})
	}
	if err := validateCanonicalName(input.Authority); err != nil {
		return Validation("revision input is invalid", map[string]any{
			"authority": err.Error(),
		})
	}
	if len(input.Entries) < 1 || len(input.Entries) > MaxEntries {
		return Validation("revision input is invalid", map[string]any{
			"entries": "must contain between 1 and 500 items",
		})
	}
	return nil
}

func ValidateNodeInput(input NodeCreateInput, now time.Time) error {
	input.NodeName = canonicalName(input.NodeName)
	input.Region = strings.TrimSpace(strings.ToLower(input.Region))
	details := make(map[string]any)
	if err := validateCanonicalName(input.NodeName); err != nil {
		details["node_name"] = err.Error()
	}
	if count := utf8.RuneCountInString(input.Region); count < 2 || count > MaxRegionRunes {
		details["region"] = "must contain between 2 and 48 characters"
	}
	if len(details) > 0 {
		return Validation("node input is invalid", details)
	}
	return nil
}

func NormalizeNodeInput(input NodeCreateInput) NodeCreateInput {
	input.NodeName = canonicalName(input.NodeName)
	input.Region = strings.TrimSpace(strings.ToLower(input.Region))
	return input
}

func ValidateLeaseInput(input LeaseInput) error {
	input.RequestKey = strings.TrimSpace(input.RequestKey)
	input.Region = strings.TrimSpace(strings.ToLower(input.Region))
	details := make(map[string]any)
	if err := validateRequestKey(input.RequestKey); err != nil {
		details["request_key"] = err.Error()
	}
	if count := utf8.RuneCountInString(input.Region); count > MaxRegionRunes {
		details["region"] = "must be at most 48 characters"
	}
	if input.LeaseSeconds < 30 || input.LeaseSeconds > 86400 {
		details["lease_seconds"] = "must be between 30 and 86400"
	}
	if input.MaxNodes < 1 || input.MaxNodes > 100 {
		details["max_nodes"] = "must be between 1 and 100"
	}
	if input.Capability != nil {
		if err := ValidateCapabilityMatch(*input.Capability); err != nil {
			details["capability"] = err.Error()
		}
	}
	if len(details) > 0 {
		return Validation("lease input is invalid", details)
	}
	return nil
}

var allowedProtocols = map[string]struct{}{
	"dns-udp":     {},
	"dns-tcp":     {},
	"doh-http1.1": {},
	"doh-http2":   {},
	"doq":         {},
	"dot":         {},
}

var capacityRanks = map[CapacityTier]int{
	CapacitySmall:  1,
	CapacityMedium: 2,
	CapacityLarge:  3,
	CapacityXLarge: 4,
}

// IsSupportedProtocol 报告协议版本是否在受支持集合内。
func IsSupportedProtocol(protocol string) bool {
	_, ok := allowedProtocols[protocol]
	return ok
}

// CapacityRank 返回容量等级的可比较序号，未知等级返回 false。
func CapacityRank(tier CapacityTier) (int, bool) {
	rank, ok := capacityRanks[tier]
	return rank, ok
}

// NormalizeCapabilityProfile 规范化能力档案登记内容：去空白、小写、
// 去重并稳定排序协议版本与业务标签。
func NormalizeCapabilityProfile(input CapabilityProfileInput) CapabilityProfileInput {
	input.ProtocolVersions = normalizeStringSet(input.ProtocolVersions)
	input.Tags = normalizeStringSet(input.Tags)
	input.CapacityTier = strings.TrimSpace(strings.ToLower(input.CapacityTier))
	return input
}

// NormalizeCapabilityMatch 规范化分配请求中的能力条件。
func NormalizeCapabilityMatch(match CapabilityMatch) CapabilityMatch {
	match.RequiredProtocolVersions = normalizeStringSet(match.RequiredProtocolVersions)
	match.RequiredTags = normalizeStringSet(match.RequiredTags)
	match.MinimumCapacityTier = strings.TrimSpace(strings.ToLower(match.MinimumCapacityTier))
	return match
}

// CapabilityMatchRequested 判断规范化后的能力条件是否提出了任何要求。
func CapabilityMatchRequested(match CapabilityMatch) bool {
	return len(match.RequiredProtocolVersions) > 0 ||
		match.MinimumCapacityTier != "" ||
		len(match.RequiredTags) > 0
}

// ValidateCapabilityProfile 校验能力档案登记内容（调用前应先规范化）。
func ValidateCapabilityProfile(input CapabilityProfileInput) error {
	details := make(map[string]any)
	if len(input.ProtocolVersions) < 1 || len(input.ProtocolVersions) > MaxProtocols {
		details["protocol_versions"] = "must contain between 1 and 10 items"
	}
	for _, protocol := range input.ProtocolVersions {
		if !IsSupportedProtocol(protocol) {
			details["protocol_versions"] = "contains an unsupported protocol version: " + protocol
			break
		}
	}
	if _, ok := CapacityRank(CapacityTier(input.CapacityTier)); !ok {
		details["capacity_tier"] = "must be one of small, medium, large, or xlarge"
	}
	if len(input.Tags) > MaxCapabilityTags {
		details["tags"] = "must contain at most 20 items"
	}
	for _, tag := range input.Tags {
		if err := validateCapabilityTag(tag); err != nil {
			details["tags"] = err.Error()
			break
		}
	}
	if len(details) > 0 {
		return Validation("capability profile input is invalid", details)
	}
	return nil
}

// ValidateCapabilityMatch 校验分配请求中的能力条件（调用前应先规范化）。
func ValidateCapabilityMatch(match CapabilityMatch) error {
	details := make(map[string]any)
	if len(match.RequiredProtocolVersions) > MaxProtocols {
		details["required_protocol_versions"] = "must contain at most 10 items"
	}
	for _, protocol := range match.RequiredProtocolVersions {
		if !IsSupportedProtocol(protocol) {
			details["required_protocol_versions"] = "contains an unsupported protocol version: " + protocol
			break
		}
	}
	if match.MinimumCapacityTier != "" {
		if _, ok := CapacityRank(CapacityTier(match.MinimumCapacityTier)); !ok {
			details["minimum_capacity_tier"] = "must be one of small, medium, large, or xlarge"
		}
	}
	if len(match.RequiredTags) > MaxCapabilityTags {
		details["required_tags"] = "must contain at most 20 items"
	}
	for _, tag := range match.RequiredTags {
		if err := validateCapabilityTag(tag); err != nil {
			details["required_tags"] = err.Error()
			break
		}
	}
	if len(details) > 0 {
		return Validation("capability match is invalid", details)
	}
	return nil
}

func validateCapabilityTag(tag string) error {
	count := utf8.RuneCountInString(tag)
	if count < 1 || count > MaxTagRunes {
		return Validation("tags must contain between 1 and 48 characters", nil)
	}
	for _, r := range tag {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return Validation("tags may contain only lowercase letters, digits, hyphens, and underscores", nil)
	}
	return nil
}

func normalizeStringSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(strings.ToLower(value))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result
}

func ValidateProbeInput(input ProbeInput, now time.Time) error {
	input.RequestKey = strings.TrimSpace(input.RequestKey)
	details := make(map[string]any)
	if err := validateRequestKey(input.RequestKey); err != nil {
		details["request_key"] = err.Error()
	}
	if input.LatencyMS < 0 || input.LatencyMS > 600000 {
		details["latency_ms"] = "must be between 0 and 600000"
	}
	if input.ObservedAt.IsZero() {
		details["observed_at"] = "is required"
	} else if input.ObservedAt.After(now.Add(5 * time.Minute)) {
		details["observed_at"] = "cannot be more than five minutes in the future"
	}
	if len(details) > 0 {
		return Validation("probe input is invalid", details)
	}
	return nil
}

func ValidateAssessmentInput(input AssessmentInput) error {
	input.RequestKey = strings.TrimSpace(input.RequestKey)
	if err := validateRequestKey(input.RequestKey); err != nil {
		return Validation("assessment input is invalid", map[string]any{
			"request_key": err.Error(),
		})
	}
	return nil
}

func validateRequestKey(value string) error {
	count := utf8.RuneCountInString(value)
	if count < MinRequestKeyRunes || count > MaxRequestKeyRunes {
		return Validation("must contain between 8 and 64 characters", nil)
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if r == '-' || r == '_' {
			continue
		}
		return Validation("may contain only letters, digits, hyphens, and underscores", nil)
	}
	return nil
}

func canonicalName(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(strings.ToLower(value)), ".")
}

func validateCanonicalName(value string) error {
	if value == "" {
		return Validation("is required", nil)
	}
	if len(value) > MaxZoneRunes {
		return Validation("is too long", nil)
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 {
			return Validation("contains an invalid label length", nil)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return Validation("labels cannot start or end with a hyphen", nil)
		}
		for _, r := range label {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				continue
			}
			return Validation("contains unsupported characters", nil)
		}
	}
	return nil
}
