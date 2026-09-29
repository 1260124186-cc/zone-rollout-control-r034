package domain

import "time"

type PlanState string

const (
	PlanDraft     PlanState = "draft"
	PlanActive    PlanState = "active"
	PlanCompleted PlanState = "completed"
)

type NodeState string

const (
	NodeReady       NodeState = "ready"
	NodeLeased      NodeState = "leased"
	NodeQuarantined NodeState = "quarantined"
)

type LeaseState string

const (
	LeaseClaimed  LeaseState = "claimed"
	LeaseReported LeaseState = "reported"
	LeaseReleased LeaseState = "released"
	LeaseExpired  LeaseState = "expired"
)

type Verdict string

const (
	VerdictConsistent     Verdict = "consistent"
	VerdictReviewRequired Verdict = "review_required"
)

type EntryType string

const (
	EntryA     EntryType = "A"
	EntryAAAA  EntryType = "AAAA"
	EntryCNAME EntryType = "CNAME"
)

// CapacityTier 是节点能力档案声明的容量等级，按小到大排序。
type CapacityTier string

const (
	CapacitySmall  CapacityTier = "small"
	CapacityMedium CapacityTier = "medium"
	CapacityLarge  CapacityTier = "large"
	CapacityXLarge CapacityTier = "xlarge"
)

// NodeCapabilityProfile 是某个节点在一个版本下声明的能力快照。
// 档案只追加不原地改写：重新登记不同内容会产生新版本，旧版本与
// 已经引用旧版本的租约解释保持不变。
type NodeCapabilityProfile struct {
	ID                 string       `json:"id"`
	NodeID             string       `json:"node_id"`
	ProfileVersion     uint64       `json:"profile_version"`
	ProtocolVersions   []string     `json:"protocol_versions"`
	CapacityTier       CapacityTier `json:"capacity_tier"`
	Tags               []string     `json:"tags"`
	ContentFingerprint string       `json:"content_fingerprint"`
	CreatedAt          time.Time    `json:"created_at"`
}

// CapabilityMatch 是分配请求在区域筛选之后提出的明确能力条件。
// 三个字段全部为空时表示不提出能力条件，节点仅按现有区域规则参与。
type CapabilityMatch struct {
	RequiredProtocolVersions []string `json:"required_protocol_versions,omitempty"`
	MinimumCapacityTier      string   `json:"minimum_capacity_tier,omitempty"`
	RequiredTags             []string `json:"required_tags,omitempty"`
}

// CapabilityTrace 是租约对“当时选中了哪份能力”的解释快照。
// 它在租约创建时固化，之后的目录升级不会追溯改写。
type CapabilityTrace struct {
	ProfileID         string       `json:"profile_id"`
	ProfileVersion    uint64       `json:"profile_version"`
	ProtocolVersions  []string     `json:"protocol_versions"`
	CapacityTier      CapacityTier `json:"capacity_tier"`
	Tags              []string     `json:"tags"`
	MatchedConditions []string     `json:"matched_conditions"`
	RecordedAt        time.Time    `json:"recorded_at"`
}

// LeaseExplanation 解释一次租约分配为何选中该节点。
type LeaseExplanation struct {
	Region          string           `json:"region,omitempty"`
	CapabilityMatch *CapabilityMatch `json:"capability_match,omitempty"`
	Capability      *CapabilityTrace `json:"capability,omitempty"`
}

type ZoneEntry struct {
	Owner string    `json:"owner"`
	Type  EntryType `json:"type"`
	Value string    `json:"value"`
}

type ZoneRevision struct {
	ID         string      `json:"id"`
	PlanID     string      `json:"plan_id"`
	Serial     uint32      `json:"serial"`
	Authority  string      `json:"authority"`
	Entries    []ZoneEntry `json:"entries"`
	EntryCount int         `json:"entry_count"`
	CreatedAt  time.Time   `json:"created_at"`
}

type RolloutPlan struct {
	ID                  string     `json:"id"`
	Zone                string     `json:"zone"`
	State               PlanState  `json:"state"`
	MinimumSuccessRatio float64    `json:"minimum_success_ratio"`
	MaximumLatencyMS    int        `json:"maximum_latency_ms"`
	ExpectedNodes       int        `json:"expected_nodes"`
	Version             uint64     `json:"version"`
	LatestRevisionID    string     `json:"latest_revision_id,omitempty"`
	LatestAssessmentID  string     `json:"latest_assessment_id,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	ActivatedAt         *time.Time `json:"activated_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
}

type ResolverNode struct {
	ID        string    `json:"id"`
	NodeName  string    `json:"node_name"`
	Region    string    `json:"region"`
	State     NodeState `json:"state"`
	Version   uint64    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type NodeLease struct {
	ID           string            `json:"id"`
	PlanID       string            `json:"plan_id"`
	NodeID       string            `json:"node_id"`
	AllocationID string            `json:"allocation_id"`
	State        LeaseState        `json:"state"`
	ClaimedAt    time.Time         `json:"claimed_at"`
	ExpiresAt    time.Time         `json:"expires_at"`
	FinishedAt   *time.Time        `json:"finished_at,omitempty"`
	ProbeID      string            `json:"probe_id,omitempty"`
	Explanation  *LeaseExplanation `json:"explanation,omitempty"`
}

type LeaseGroup struct {
	ID                 string           `json:"id"`
	PlanID             string           `json:"plan_id"`
	RequestKey         string           `json:"request_key"`
	RequestFingerprint string           `json:"request_fingerprint"`
	Region             string           `json:"region,omitempty"`
	CapabilityMatch    *CapabilityMatch `json:"capability_match,omitempty"`
	LeaseIDs           []string         `json:"lease_ids"`
	CreatedAt          time.Time        `json:"created_at"`
}

type Probe struct {
	ID                 string    `json:"id"`
	LeaseID            string    `json:"lease_id"`
	PlanID             string    `json:"plan_id"`
	NodeID             string    `json:"node_id"`
	RequestKey         string    `json:"request_key"`
	Success            bool      `json:"success"`
	ObservedSerial     uint32    `json:"observed_serial"`
	LatencyMS          int       `json:"latency_ms"`
	ObservedAt         time.Time `json:"observed_at"`
	StoredAt           time.Time `json:"stored_at"`
	PayloadFingerprint string    `json:"payload_fingerprint"`
}

type RolloutAssessment struct {
	ID               string    `json:"id"`
	PlanID           string    `json:"plan_id"`
	RevisionID       string    `json:"revision_id"`
	RequestKey       string    `json:"request_key"`
	Verdict          Verdict   `json:"verdict"`
	Reasons          []string  `json:"reasons"`
	ProbeCount       int       `json:"probe_count"`
	SuccessCount     int       `json:"success_count"`
	SuccessRatio     float64   `json:"success_ratio"`
	MaximumLatencyMS int       `json:"maximum_latency_ms"`
	CreatedAt        time.Time `json:"created_at"`
}

type AuditEvent struct {
	ID         string         `json:"id"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Action     string         `json:"action"`
	At         time.Time      `json:"at"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type State struct {
	SchemaVersion         int                          `json:"schema_version"`
	Revision              uint64                       `json:"revision"`
	Plans                 map[string]RolloutPlan       `json:"plans"`
	Revisions             map[string]ZoneRevision      `json:"revisions"`
	Nodes                 map[string]ResolverNode      `json:"nodes"`
	Leases                map[string]NodeLease         `json:"leases"`
	LeaseGroups           map[string]LeaseGroup        `json:"lease_groups"`
	Probes                map[string]Probe             `json:"probes"`
	Assessments           map[string]RolloutAssessment `json:"assessments"`
	LeaseRequestKeys      map[string]string            `json:"lease_request_keys"`
	AssessmentRequestKeys map[string]string            `json:"assessment_request_keys"`
	// NodeCapabilities 以节点 ID 为键保存该节点按版本顺序追加的能力档案。
	NodeCapabilities map[string][]NodeCapabilityProfile `json:"node_capabilities"`
	AuditEvents      []AuditEvent                       `json:"audit_events"`
}

type PageMeta struct {
	Limit      int    `json:"limit"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type PlanPage struct {
	Items []RolloutPlan `json:"items"`
	Meta  PageMeta      `json:"meta"`
}

type NodePage struct {
	Items []ResolverNode `json:"items"`
	Meta  PageMeta       `json:"meta"`
}
