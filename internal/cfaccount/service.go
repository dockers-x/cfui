package cfaccount

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cfui/internal/cfoauth"

	cloudflare "github.com/cloudflare/cloudflare-go"
)

type Service struct {
	auth            *cfoauth.Service
	httpClient      *http.Client
	graphQLEndpoint string
	restEndpoint    string
	statusEndpoint  string
}

type EndpointOverrides struct {
	GraphQL string
	REST    string
	Status  string
}

const (
	defaultGraphQLEndpoint        = "https://api.cloudflare.com/client/v4/graphql"
	defaultRESTEndpoint           = "https://api.cloudflare.com/client/v4"
	defaultStatusPageEndpoint     = "https://www.cloudflarestatus.com/api/v2"
	maxStatusPageResponseBytes    = 4 << 20
	maxKVTextValueBytes           = 1024 * 1024
	maxR2ObjectKeyBytes           = 1024
	maxR2ObjectTextValueBytes     = 1024 * 1024
	maxR2ObjectBinaryPreviewBytes = 1024
	maxR2ObjectWriteBytes         = 1024 * 1024
	maxR2ObjectUploadBytes        = 128 * 1024 * 1024
	maxD1SQLBytes                 = 64 * 1024
	maxD1Parameters               = 100
	defaultD1TableLimit           = 50
	maxD1TableLimit               = 100
	maxD1CellValueBytes           = 64 * 1024
	maxD1IdentifierLen            = 512
	d1RowIDDefaultKey             = "_cfui_rowid_"
	maxSnippetNameLen             = 128
	maxSnippetCodeBytes           = 512 * 1024
	maxSnippetContentRespBytes    = maxSnippetCodeBytes + 64*1024
	maxSnippetFileLen             = 128
	maxWAFExpressionLen           = 4096
	maxWAFDescriptionLen          = 256
	maxWAFAdvancedJSONBytes       = 64 * 1024
	maxBrowserCacheTTLSeconds     = 31536000
	maxWorkerScriptNameLen        = 128
	maxWorkerScriptContentBytes   = 512 * 1024
	maxTunnelNameLen              = 128
	overviewDNSZoneCountLimit     = 50
)

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type Zone struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	Type                string     `json:"type,omitempty"`
	Paused              bool       `json:"paused,omitempty"`
	AccountID           string     `json:"account_id,omitempty"`
	Account             Account    `json:"account,omitempty"`
	Plan                ZonePlan   `json:"plan,omitempty"`
	NameServers         []string   `json:"name_servers,omitempty"`
	OriginalNameServers []string   `json:"original_name_servers,omitempty"`
	CreatedOn           *time.Time `json:"created_on,omitempty"`
	ModifiedOn          *time.Time `json:"modified_on,omitempty"`
}

type ZonePlan struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	LegacyID  string `json:"legacy_id,omitempty"`
	Frequency string `json:"frequency,omitempty"`
	Currency  string `json:"currency,omitempty"`
	Price     int    `json:"price,omitempty"`
}

type ZoneDetailResponse struct {
	Zone         Zone                     `json:"zone"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type StatusPageResponse struct {
	Page             StatusPageInfo        `json:"page"`
	Overall          StatusPageOverall     `json:"overall"`
	ActiveIncidents  []StatusPageIncident  `json:"active_incidents"`
	Maintenances     []StatusPageIncident  `json:"maintenances"`
	AffectedProducts []StatusPageComponent `json:"affected_products"`
	ProductTotal     int                   `json:"product_total"`
	Regions          []StatusPageRegion    `json:"regions"`
	RecentIncidents  []StatusPageIncident  `json:"recent_incidents"`
	FetchedAt        time.Time             `json:"fetched_at"`
}

type StatusPageInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	TimeZone  string `json:"time_zone"`
	UpdatedAt string `json:"updated_at"`
}

type StatusPageOverall struct {
	Indicator   string `json:"indicator"`
	Description string `json:"description"`
}

type StatusPageComponent struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Group   bool   `json:"group,omitempty"`
	GroupID string `json:"group_id,omitempty"`
}

type StatusPageRegion struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Total    int    `json:"total"`
	Impacted int    `json:"impacted"`
}

type StatusPageIncident struct {
	ID              string                     `json:"id"`
	Name            string                     `json:"name"`
	Status          string                     `json:"status"`
	Impact          string                     `json:"impact"`
	CreatedAt       string                     `json:"created_at,omitempty"`
	UpdatedAt       string                     `json:"updated_at,omitempty"`
	ScheduledFor    string                     `json:"scheduled_for,omitempty"`
	Shortlink       string                     `json:"shortlink,omitempty"`
	IncidentUpdates []StatusPageIncidentUpdate `json:"incident_updates,omitempty"`
}

type StatusPageIncidentUpdate struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Body      string `json:"body"`
	DisplayAt string `json:"display_at,omitempty"`
}

type OverviewResponse struct {
	Account      *Account                 `json:"account,omitempty"`
	Zone         *Zone                    `json:"zone,omitempty"`
	Metrics      []OverviewMetric         `json:"metrics"`
	Status       *StatusPageOverall       `json:"status,omitempty"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
	FetchedAt    time.Time                `json:"fetched_at"`
}

type OverviewMetric struct {
	ID        string `json:"id"`
	Feature   string `json:"feature,omitempty"`
	Value     int    `json:"value"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	Limited   bool   `json:"limited,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type DNSRecord struct {
	ID         string     `json:"id"`
	Type       string     `json:"type"`
	Name       string     `json:"name"`
	Content    string     `json:"content"`
	TTL        int        `json:"ttl"`
	Proxied    *bool      `json:"proxied,omitempty"`
	Proxiable  bool       `json:"proxiable"`
	Comment    string     `json:"comment,omitempty"`
	CreatedOn  *time.Time `json:"created_on,omitempty"`
	ModifiedOn *time.Time `json:"modified_on,omitempty"`
}

type DNSRecordCountResponse struct {
	Count        int                      `json:"count"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type DNSRecordRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied *bool  `json:"proxied,omitempty"`
	Comment string `json:"comment,omitempty"`
}

type ZoneSettingUpdateRequest struct {
	Value any `json:"value"`
}

type CachePurgeResult struct {
	ID      string `json:"id,omitempty"`
	Success bool   `json:"success"`
}

type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

func NewService(auth *cfoauth.Service) *Service {
	return NewServiceWithEndpoints(auth, EndpointOverrides{})
}

func NewServiceWithEndpoints(auth *cfoauth.Service, endpoints EndpointOverrides) *Service {
	return &Service{
		auth:            auth,
		httpClient:      http.DefaultClient,
		graphQLEndpoint: strings.TrimSpace(endpoints.GraphQL),
		restEndpoint:    strings.TrimSpace(endpoints.REST),
		statusEndpoint:  strings.TrimSpace(endpoints.Status),
	}
}

func (s *Service) currentClient(ctx context.Context) (*cloudflare.API, cfoauth.SessionSummary, error) {
	client, session, err := s.auth.CurrentClient(ctx)
	if err != nil {
		return nil, cfoauth.SessionSummary{}, err
	}
	if endpoint := strings.TrimRight(strings.TrimSpace(s.restEndpoint), "/"); endpoint != "" {
		client.BaseURL = endpoint
	}
	return client, session, nil
}

type Tunnel struct {
	ID                    string             `json:"id"`
	Name                  string             `json:"name"`
	Status                string             `json:"status,omitempty"`
	Type                  string             `json:"type,omitempty"`
	RemoteConfig          bool               `json:"remote_config"`
	CreatedAt             *time.Time         `json:"created_at,omitempty"`
	DeletedAt             *time.Time         `json:"deleted_at,omitempty"`
	ConnectionsActiveAt   *time.Time         `json:"connections_active_at,omitempty"`
	ConnectionsInactiveAt *time.Time         `json:"connections_inactive_at,omitempty"`
	ConnectionCount       int                `json:"connection_count"`
	Connections           []TunnelConnection `json:"connections,omitempty"`
}

type TunnelCreateRequest struct {
	Name string `json:"name"`
}

type TunnelCreateResult struct {
	Tunnel       Tunnel                   `json:"tunnel"`
	Token        string                   `json:"-"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type TunnelConnection struct {
	ID                 string `json:"id,omitempty"`
	ColoName           string `json:"colo_name,omitempty"`
	ClientID           string `json:"client_id,omitempty"`
	ClientVersion      string `json:"client_version,omitempty"`
	OpenedAt           string `json:"opened_at,omitempty"`
	OriginIP           string `json:"origin_ip,omitempty"`
	IsPendingReconnect bool   `json:"is_pending_reconnect"`
}

type WorkerScript struct {
	ID               string     `json:"id"`
	Size             int        `json:"size"`
	CreatedOn        *time.Time `json:"created_on,omitempty"`
	ModifiedOn       *time.Time `json:"modified_on,omitempty"`
	Logpush          *bool      `json:"logpush,omitempty"`
	LastDeployedFrom string     `json:"last_deployed_from,omitempty"`
	DeploymentID     string     `json:"deployment_id,omitempty"`
}

type WorkerDetailResponse struct {
	Worker       WorkerScript             `json:"worker"`
	Content      WorkerScriptContent      `json:"content"`
	Settings     WorkerScriptSettings     `json:"settings"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type WorkerScriptContent struct {
	Value     string `json:"value,omitempty"`
	Encoding  string `json:"encoding"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

type WorkerScriptSettings struct {
	ETag            string               `json:"etag,omitempty"`
	Logpush         *bool                `json:"logpush,omitempty"`
	TailConsumers   []WorkerTailConsumer `json:"tail_consumers,omitempty"`
	PlacementMode   string               `json:"placement_mode,omitempty"`
	PlacementStatus string               `json:"placement_status,omitempty"`
}

type WorkerTailConsumer struct {
	Service     string `json:"service"`
	Environment string `json:"environment,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}

type WorkerTailSession struct {
	ID        string     `json:"id"`
	URL       string     `json:"-"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type WorkerMetricsResponse struct {
	Range           string                   `json:"range"`
	Since           time.Time                `json:"since"`
	Until           time.Time                `json:"until"`
	Summary         WorkerMetricsSummary     `json:"summary"`
	StatusBreakdown []WorkerStatusMetric     `json:"status_breakdown"`
	Series          []WorkerSeriesPoint      `json:"series"`
	Session         cfoauth.SessionSummary   `json:"session"`
	Capabilities    cfoauth.CapabilityMatrix `json:"capabilities"`
}

type WorkerMetricsSummary struct {
	Requests     int      `json:"requests"`
	Errors       int      `json:"errors"`
	Subrequests  int      `json:"subrequests"`
	CPUTimeP50Us *float64 `json:"cpu_time_p50_us,omitempty"`
	CPUTimeP99Us *float64 `json:"cpu_time_p99_us,omitempty"`
	CPUTimeUs    *float64 `json:"cpu_time_us,omitempty"`
}

type WorkerStatusMetric struct {
	Status   string `json:"status"`
	Requests int    `json:"requests"`
}

type WorkerSeriesPoint struct {
	Time     time.Time `json:"time"`
	Requests int       `json:"requests"`
	Errors   int       `json:"errors"`
}

type AccountUsageResponse struct {
	PeriodStart  time.Time                `json:"period_start"`
	TodayStart   time.Time                `json:"today_start"`
	Now          time.Time                `json:"now"`
	Billing      AccountBillingInfo       `json:"billing"`
	Workers      WorkersUsageMetrics      `json:"workers"`
	R2           R2UsageMetrics           `json:"r2"`
	D1           *D1UsageMetrics          `json:"d1,omitempty"`
	KV           *KVUsageMetrics          `json:"kv,omitempty"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type WorkersUsageMetrics struct {
	RequestsToday  int      `json:"requests_today"`
	RequestsPeriod int      `json:"requests_period"`
	ErrorsPeriod   int      `json:"errors_period"`
	ErrorsLastHour *int     `json:"errors_last_hour,omitempty"`
	Subrequests    int      `json:"subrequests"`
	CPUTimeP50Us   *float64 `json:"cpu_time_p50_us,omitempty"`
	CPUTimeP99Us   *float64 `json:"cpu_time_p99_us,omitempty"`
	CPUTimePeriod  *float64 `json:"cpu_time_period_us,omitempty"`
	CPUTimeToday   *float64 `json:"cpu_time_today_us,omitempty"`
}

type R2UsageMetrics struct {
	ClassAOperations int `json:"class_a_operations"`
	ClassBOperations int `json:"class_b_operations"`
	StorageBytes     int `json:"storage_bytes"`
	ObjectCount      int `json:"object_count"`
}

type R2AccountMetricsResponse struct {
	Standard         *R2ClassMetrics          `json:"standard,omitempty"`
	InfrequentAccess *R2ClassMetrics          `json:"infrequent_access,omitempty"`
	Session          cfoauth.SessionSummary   `json:"session"`
	Capabilities     cfoauth.CapabilityMatrix `json:"capabilities"`
}

type R2AccountMetrics struct {
	Standard         *R2ClassMetrics `json:"standard,omitempty"`
	InfrequentAccess *R2ClassMetrics `json:"infrequent_access,omitempty"`
}

type R2ClassMetrics struct {
	Published *R2MetricsSnapshot `json:"published,omitempty"`
	Uploaded  *R2MetricsSnapshot `json:"uploaded,omitempty"`
}

type R2MetricsSnapshot struct {
	Objects      int `json:"objects"`
	PayloadSize  int `json:"payload_size"`
	MetadataSize int `json:"metadata_size"`
	TotalBytes   int `json:"total_bytes"`
}

type D1UsageMetrics struct {
	RowsReadToday      int `json:"rows_read_today"`
	RowsWrittenToday   int `json:"rows_written_today"`
	RowsReadPeriod     int `json:"rows_read_period"`
	RowsWrittenPeriod  int `json:"rows_written_period"`
	ReadQueriesPeriod  int `json:"read_queries_period"`
	WriteQueriesPeriod int `json:"write_queries_period"`
}

type KVUsageMetrics struct {
	ReadsToday   int `json:"reads_today"`
	WritesToday  int `json:"writes_today"`
	ReadsPeriod  int `json:"reads_period"`
	WritesPeriod int `json:"writes_period"`
	StorageBytes int `json:"storage_bytes"`
	KeyCount     int `json:"key_count"`
}

type AccountBillingInfo struct {
	Available     bool                         `json:"available"`
	Reason        string                       `json:"reason,omitempty"`
	WorkersPaid   bool                         `json:"workers_paid"`
	R2Paid        bool                         `json:"r2_paid"`
	PeriodStart   *time.Time                   `json:"period_start,omitempty"`
	PeriodEnd     *time.Time                   `json:"period_end,omitempty"`
	Subscriptions []AccountSubscriptionSummary `json:"subscriptions,omitempty"`
}

type AccountSubscription struct {
	ID                 string                      `json:"id"`
	State              string                      `json:"state"`
	Frequency          string                      `json:"frequency"`
	CurrentPeriodStart string                      `json:"current_period_start"`
	CurrentPeriodEnd   string                      `json:"current_period_end"`
	RatePlan           AccountSubscriptionRatePlan `json:"rate_plan"`
}

type AccountSubscriptionRatePlan struct {
	ID         string `json:"id"`
	PublicName string `json:"public_name"`
}

type AccountSubscriptionSummary struct {
	ID                 string     `json:"id,omitempty"`
	State              string     `json:"state,omitempty"`
	Frequency          string     `json:"frequency,omitempty"`
	RatePlanID         string     `json:"rate_plan_id,omitempty"`
	RatePlanName       string     `json:"rate_plan_name,omitempty"`
	Active             bool       `json:"active"`
	CurrentPeriodStart *time.Time `json:"current_period_start,omitempty"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end,omitempty"`
}

type R2Bucket struct {
	Name         string     `json:"name"`
	CreationDate *time.Time `json:"creation_date,omitempty"`
	Location     string     `json:"location,omitempty"`
}

type R2BucketRequest struct {
	Name         string `json:"name"`
	LocationHint string `json:"location_hint,omitempty"`
}

type R2Object struct {
	Key          string         `json:"key"`
	ETag         string         `json:"etag,omitempty"`
	LastModified string         `json:"last_modified,omitempty"`
	Size         int            `json:"size,omitempty"`
	HTTPMetadata R2HTTPMetadata `json:"http_metadata,omitempty"`
	StorageClass string         `json:"storage_class,omitempty"`
}

type R2HTTPMetadata struct {
	ContentType string `json:"contentType,omitempty"`
}

type R2ObjectsResponse struct {
	Data         []R2Object               `json:"data"`
	Cursor       string                   `json:"cursor,omitempty"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type R2ObjectValue struct {
	Key           string           `json:"key"`
	Value         string           `json:"value,omitempty"`
	Encoding      string           `json:"encoding"`
	Bytes         int              `json:"bytes"`
	ContentType   string           `json:"content_type,omitempty"`
	Truncated     bool             `json:"truncated"`
	BinaryPreview *R2BinaryPreview `json:"binary_preview,omitempty"`
}

type R2BinaryPreview struct {
	Bytes     int    `json:"bytes"`
	Hexdump   string `json:"hexdump"`
	Truncated bool   `json:"truncated"`
}

type R2ObjectValueRequest struct {
	Value       string `json:"value"`
	ContentType string `json:"content_type,omitempty"`
}

type R2ObjectCopyRequest struct {
	SourceKey         string `json:"source_key"`
	DestinationBucket string `json:"destination_bucket,omitempty"`
	DestinationKey    string `json:"destination_key"`
	DeleteSource      bool   `json:"delete_source,omitempty"`
}

type R2ObjectDownload struct {
	Key           string
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ETag          string
	LastModified  string
}

type D1Database struct {
	UUID      string     `json:"uuid"`
	Name      string     `json:"name"`
	Version   string     `json:"version,omitempty"`
	NumTables int        `json:"num_tables"`
	FileSize  int64      `json:"file_size"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

type D1DatabaseDetailResponse struct {
	Database     D1Database               `json:"database"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type KVNamespace struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type KVKey struct {
	Name       string `json:"name"`
	Expiration int    `json:"expiration,omitempty"`
}

type KVValue struct {
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"`
	Encoding string `json:"encoding"`
	Bytes    int    `json:"bytes"`
}

type KVKeysResponse struct {
	Data         []KVKey                  `json:"data"`
	Cursor       string                   `json:"cursor,omitempty"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type KVValueRequest struct {
	Value string `json:"value"`
}

type D1QueryRequest struct {
	SQL    string   `json:"sql"`
	Params []string `json:"params,omitempty"`
}

type D1QueryResponse struct {
	Data         []D1Result               `json:"data"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type D1Column struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	NotNull    bool   `json:"not_null"`
	PrimaryKey bool   `json:"primary_key"`
}

type D1TableResponse struct {
	Table        string                   `json:"table"`
	Columns      []D1Column               `json:"columns"`
	Rows         []map[string]any         `json:"rows"`
	RowIDKey     string                   `json:"rowid_key"`
	Limit        int                      `json:"limit"`
	Offset       int                      `json:"offset"`
	HasMore      bool                     `json:"has_more"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type D1RowMutationRequest struct {
	RowID   string            `json:"rowid"`
	Changes map[string]string `json:"changes,omitempty"`
}

type D1MutationResponse struct {
	Success      bool                     `json:"success"`
	Data         []D1Result               `json:"data,omitempty"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type D1Result struct {
	Success *bool                         `json:"success,omitempty"`
	Results []map[string]any              `json:"results"`
	Meta    cloudflare.D1DatabaseMetadata `json:"meta"`
}

type Snippet struct {
	Name       string     `json:"name"`
	CreatedOn  *time.Time `json:"created_on,omitempty"`
	ModifiedOn *time.Time `json:"modified_on,omitempty"`
	RuleCount  int        `json:"rule_count"`
}

type SnippetRequest struct {
	Name     string `json:"name"`
	Code     string `json:"code"`
	MainFile string `json:"main_file,omitempty"`
}

type SnippetContent struct {
	Name         string                   `json:"name"`
	MainFile     string                   `json:"main_file"`
	Value        string                   `json:"value,omitempty"`
	Encoding     string                   `json:"encoding"`
	Bytes        int                      `json:"bytes"`
	Truncated    bool                     `json:"truncated"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type SnippetRule struct {
	ID          string `json:"id,omitempty"`
	SnippetName string `json:"snippet_name"`
	Expression  string `json:"expression"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type SnippetRuleRequest struct {
	SnippetName string `json:"snippet_name"`
	Expression  string `json:"expression"`
	Description string `json:"description,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

type SnippetRuleUpdateRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type WAFRuleset struct {
	ID          string     `json:"id,omitempty"`
	Name        string     `json:"name,omitempty"`
	Phase       string     `json:"phase,omitempty"`
	LastUpdated *time.Time `json:"last_updated,omitempty"`
	Rules       []WAFRule  `json:"rules"`
}

type WAFRule struct {
	ID               string                   `json:"id"`
	Ref              string                   `json:"ref,omitempty"`
	Version          string                   `json:"version,omitempty"`
	Action           string                   `json:"action"`
	ActionParameters *WAFRuleActionParameters `json:"action_parameters,omitempty"`
	Expression       string                   `json:"expression"`
	Description      string                   `json:"description,omitempty"`
	Enabled          *bool                    `json:"enabled,omitempty"`
	ScoreThreshold   int                      `json:"score_threshold,omitempty"`
	RateLimit        *WAFRuleRateLimit        `json:"ratelimit,omitempty"`
	Logging          *WAFRuleLogging          `json:"logging,omitempty"`
	CredentialCheck  *WAFRuleCredentialCheck  `json:"exposed_credential_check,omitempty"`
	LastUpdated      *time.Time               `json:"last_updated,omitempty"`
}

type WAFRuleRequest struct {
	Action           string                   `json:"action"`
	ActionParameters *WAFRuleActionParameters `json:"action_parameters,omitempty"`
	RateLimit        optionalWAFJSON          `json:"ratelimit,omitempty"`
	Expression       string                   `json:"expression"`
	Description      string                   `json:"description,omitempty"`
	Enabled          *bool                    `json:"enabled,omitempty"`
}

type WAFRuleActionParameters struct {
	ID        string               `json:"id,omitempty"`
	Ruleset   string               `json:"ruleset,omitempty"`
	Rulesets  []string             `json:"rulesets,omitempty"`
	Rules     map[string][]string  `json:"rules,omitempty"`
	Products  []string             `json:"products,omitempty"`
	Phases    []string             `json:"phases,omitempty"`
	Overrides *WAFManagedOverrides `json:"overrides,omitempty"`
	Version   *string              `json:"version,omitempty"`
	Raw       map[string]any       `json:"raw,omitempty"`
}

type WAFRuleUpdateRequest struct {
	Action                 *string                  `json:"action,omitempty"`
	ActionParameters       *WAFRuleActionParameters `json:"action_parameters,omitempty"`
	ActionParametersJSON   optionalWAFJSON          `json:"action_parameters_json,omitempty"`
	RateLimit              optionalWAFJSON          `json:"ratelimit,omitempty"`
	Logging                optionalWAFJSON          `json:"logging,omitempty"`
	ExposedCredentialCheck optionalWAFJSON          `json:"exposed_credential_check,omitempty"`
	Expression             *string                  `json:"expression,omitempty"`
	Description            *string                  `json:"description,omitempty"`
	Enabled                *bool                    `json:"enabled,omitempty"`
}

type WAFManagedOverrideRequest struct {
	ManagedRulesetID string               `json:"managed_ruleset_id"`
	Overrides        *WAFManagedOverrides `json:"overrides,omitempty"`
	Expression       string               `json:"expression"`
	Description      string               `json:"description,omitempty"`
	Enabled          *bool                `json:"enabled,omitempty"`
}

type WAFManagedOverrideUpdateRequest struct {
	ManagedRulesetID *string         `json:"managed_ruleset_id,omitempty"`
	Overrides        optionalWAFJSON `json:"overrides,omitempty"`
	Expression       *string         `json:"expression,omitempty"`
	Description      *string         `json:"description,omitempty"`
	Enabled          *bool           `json:"enabled,omitempty"`
}

type WAFManagedOverrides struct {
	Enabled          *bool                        `json:"enabled,omitempty"`
	Action           string                       `json:"action,omitempty"`
	SensitivityLevel string                       `json:"sensitivity_level,omitempty"`
	Categories       []WAFManagedCategoryOverride `json:"categories,omitempty"`
	Rules            []WAFManagedRuleOverride     `json:"rules,omitempty"`
}

type WAFManagedCategoryOverride struct {
	Category string `json:"category"`
	Action   string `json:"action,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

type WAFManagedRuleOverride struct {
	ID               string `json:"id"`
	Action           string `json:"action,omitempty"`
	Enabled          *bool  `json:"enabled,omitempty"`
	ScoreThreshold   int    `json:"score_threshold,omitempty"`
	SensitivityLevel string `json:"sensitivity_level,omitempty"`
}

type optionalWAFJSON struct {
	Set bool
	Raw json.RawMessage
}

func (v *optionalWAFJSON) UnmarshalJSON(raw []byte) error {
	v.Set = true
	v.Raw = append(v.Raw[:0], raw...)
	return nil
}

type WAFRuleRateLimit struct {
	Characteristics         []string `json:"characteristics,omitempty"`
	RequestsPerPeriod       int      `json:"requests_per_period,omitempty"`
	ScorePerPeriod          int      `json:"score_per_period,omitempty"`
	ScoreResponseHeaderName string   `json:"score_response_header_name,omitempty"`
	Period                  int      `json:"period,omitempty"`
	MitigationTimeout       int      `json:"mitigation_timeout,omitempty"`
	CountingExpression      string   `json:"counting_expression,omitempty"`
	RequestsToOrigin        bool     `json:"requests_to_origin,omitempty"`
}

type WAFRuleLogging struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type WAFRuleCredentialCheck struct {
	UsernameExpression string `json:"username_expression,omitempty"`
	PasswordExpression string `json:"password_expression,omitempty"`
}

type ZoneSetting struct {
	ID            string `json:"id"`
	Editable      bool   `json:"editable"`
	Value         any    `json:"value"`
	ModifiedOn    string `json:"modified_on,omitempty"`
	TimeRemaining int    `json:"time_remaining,omitempty"`
}

type AnalyticsPoint struct {
	Since          time.Time `json:"since"`
	Until          time.Time `json:"until"`
	Requests       int       `json:"requests"`
	CachedRequests int       `json:"cached_requests"`
	Uncached       int       `json:"uncached_requests"`
	Bytes          int       `json:"bytes"`
	CachedBytes    int       `json:"cached_bytes"`
	Threats        int       `json:"threats"`
	Pageviews      int       `json:"pageviews"`
	Uniques        int       `json:"uniques"`
}

type ZoneAnalyticsResponse struct {
	Range        string                   `json:"range"`
	Since        time.Time                `json:"since"`
	Until        time.Time                `json:"until"`
	Totals       AnalyticsPoint           `json:"totals"`
	Timeseries   []AnalyticsPoint         `json:"timeseries"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

type ListResponse[T any] struct {
	Data         []T                      `json:"data"`
	Session      cfoauth.SessionSummary   `json:"session"`
	Capabilities cfoauth.CapabilityMatrix `json:"capabilities"`
}

const workerMetricsSummaryQuery = `
query ($accountTag: string!, $scriptName: string!, $since: Time!, $until: Time!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      summary: workersInvocationsAdaptive(
        limit: 10000,
        filter: { scriptName: $scriptName, datetime_geq: $since, datetime_leq: $until }
      ) {
        sum { requests errors subrequests }
        quantiles { cpuTimeP50 cpuTimeP99 }
      }
      byStatus: workersInvocationsAdaptive(
        limit: 100,
        filter: { scriptName: $scriptName, datetime_geq: $since, datetime_leq: $until }
      ) {
        dimensions { status }
        sum { requests }
      }
    }
  }
}`

const workerMetricsCPUQuery = `
query ($accountTag: string!, $scriptName: string!, $since: Time!, $until: Time!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      summary: workersInvocationsAdaptive(
        limit: 10000,
        filter: { scriptName: $scriptName, datetime_geq: $since, datetime_leq: $until }
      ) {
        sum { cpuTimeUs }
      }
    }
  }
}`

const accountUsageQuery = `
query ($accountTag: string!, $periodStart: Time!, $todayStart: Time!, $now: Time!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      period: workersInvocationsAdaptive(
        limit: 10000,
        filter: { datetime_geq: $periodStart, datetime_leq: $now }
      ) {
        sum { requests errors subrequests }
        quantiles { cpuTimeP50 cpuTimeP99 }
      }
      today: workersInvocationsAdaptive(
        limit: 10000,
        filter: { datetime_geq: $todayStart, datetime_leq: $now }
      ) {
        sum { requests errors subrequests }
      }
      r2Ops: r2OperationsAdaptiveGroups(
        limit: 10000,
        filter: { datetime_geq: $periodStart, datetime_leq: $now }
      ) {
        dimensions { actionType }
        sum { requests }
      }
      r2Storage: r2StorageAdaptiveGroups(
        limit: 1000,
        filter: { datetime_geq: $todayStart, datetime_leq: $now }
      ) {
        dimensions { bucketName }
        max { payloadSize metadataSize objectCount }
      }
    }
  }
}`

const accountUsageCPUQuery = `
query ($accountTag: string!, $periodStart: Time!, $todayStart: Time!, $now: Time!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      period: workersInvocationsAdaptive(
        limit: 10000,
        filter: { datetime_geq: $periodStart, datetime_leq: $now }
      ) {
        sum { cpuTimeUs }
      }
      today: workersInvocationsAdaptive(
        limit: 10000,
        filter: { datetime_geq: $todayStart, datetime_leq: $now }
      ) {
        sum { cpuTimeUs }
      }
    }
  }
}`

const accountWorkersErrorsLastHourQuery = `
query ($accountTag: string!, $since: Time!, $until: Time!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      window: workersInvocationsAdaptive(
        limit: 10000,
        filter: { datetime_geq: $since, datetime_leq: $until }
      ) {
        sum { errors }
      }
    }
  }
}`

const d1UsageQuery = `
query ($accountTag: string!, $periodStart: Date!, $todayStart: Date!, $until: Date!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      period: d1AnalyticsAdaptiveGroups(
        limit: 10000,
        filter: { date_geq: $periodStart, date_leq: $until }
      ) {
        sum { rowsRead rowsWritten readQueries writeQueries }
      }
      today: d1AnalyticsAdaptiveGroups(
        limit: 10000,
        filter: { date_geq: $todayStart, date_leq: $until }
      ) {
        sum { rowsRead rowsWritten readQueries writeQueries }
      }
    }
  }
}`

const kvUsageOperationsQuery = `
query ($accountTag: string!, $periodStart: Date!, $todayStart: Date!, $until: Date!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      period: kvOperationsAdaptiveGroups(
        limit: 10000,
        filter: { date_geq: $periodStart, date_leq: $until }
      ) {
        dimensions { actionType }
        sum { requests }
      }
      today: kvOperationsAdaptiveGroups(
        limit: 10000,
        filter: { date_geq: $todayStart, date_leq: $until }
      ) {
        dimensions { actionType }
        sum { requests }
      }
    }
  }
}`

const kvUsageStorageQuery = `
query ($accountTag: string!, $periodStart: Date!, $todayStart: Date!, $until: Date!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      storage: kvStorageAdaptiveGroups(
        limit: 1000,
        filter: { date_geq: $todayStart, date_leq: $until }
      ) {
        dimensions { namespaceId }
        max { byteCount keyCount }
      }
    }
  }
}`

func (s *Service) CloudflareStatus(ctx context.Context) (StatusPageResponse, error) {
	var summary statusPageSummaryEnvelope
	if err := s.fetchStatusPage(ctx, "summary.json", &summary); err != nil {
		return StatusPageResponse{}, err
	}
	var history statusPageIncidentList
	if err := s.fetchStatusPage(ctx, "incidents.json", &history); err != nil {
		return StatusPageResponse{}, err
	}

	affectedProducts, productTotal, regions := splitStatusComponents(summary.Components)
	activeIDs := make(map[string]struct{}, len(summary.Incidents))
	for _, incident := range summary.Incidents {
		activeIDs[incident.ID] = struct{}{}
	}
	recent := make([]StatusPageIncident, 0, 10)
	for _, incident := range history.Incidents {
		if _, ok := activeIDs[incident.ID]; ok {
			continue
		}
		recent = append(recent, incident)
		if len(recent) >= 10 {
			break
		}
	}

	if strings.TrimSpace(summary.Page.URL) == "" {
		summary.Page.URL = "https://www.cloudflarestatus.com"
	}
	return StatusPageResponse{
		Page:             summary.Page,
		Overall:          summary.Status,
		ActiveIncidents:  compactStatusIncidents(summary.Incidents),
		Maintenances:     compactStatusIncidents(summary.ScheduledMaintenances),
		AffectedProducts: affectedProducts,
		ProductTotal:     productTotal,
		Regions:          regions,
		RecentIncidents:  compactStatusIncidents(recent),
		FetchedAt:        time.Now().UTC(),
	}, nil
}

func (s *Service) Overview(ctx context.Context, accountID string) (OverviewResponse, error) {
	token, session, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return OverviewResponse{}, err
	}
	accountID = strings.TrimSpace(accountID)
	caps := session.Capabilities
	resp := OverviewResponse{
		Metrics:      make([]OverviewMetric, 0, 11),
		Session:      session,
		Capabilities: caps,
		FetchedAt:    time.Now().UTC(),
	}

	var accounts []Account
	if canReadCapability(caps, "account") {
		accounts, err = s.overviewAccounts(ctx, token)
		if err != nil {
			resp.addMetricUnavailable("accounts", "account", err)
		} else {
			resp.addMetric("accounts", "account", len(accounts))
			if accountID == "" && len(accounts) > 0 {
				accountID = accounts[0].ID
			}
			for i := range accounts {
				if accounts[i].ID == accountID {
					account := accounts[i]
					resp.Account = &account
					break
				}
			}
		}
	} else {
		resp.addMetricMissingScope("accounts", "account")
	}
	if resp.Account == nil && accountID != "" {
		resp.Account = &Account{ID: accountID}
	}

	var zones []Zone
	if canReadCapability(caps, "zones") {
		zones, err = s.overviewZones(ctx, token, accountID)
		if err != nil {
			resp.addMetricUnavailable("zones", "zones", err)
			resp.addMetricUnavailable("active_zones", "zones", err)
		} else {
			active := 0
			for _, zone := range zones {
				if zone.Status == "active" {
					active++
				}
			}
			resp.addMetric("zones", "zones", len(zones))
			resp.addMetric("active_zones", "zones", active)
			if len(zones) > 0 {
				zone := zones[0]
				resp.Zone = &zone
				if accountID == "" && zone.AccountID != "" {
					accountID = zone.AccountID
					if resp.Account == nil {
						resp.Account = &Account{ID: accountID}
					}
				}
			}
		}
	} else {
		resp.addMetricMissingScope("zones", "zones")
		resp.addMetricMissingScope("active_zones", "zones")
	}

	if canReadCapability(caps, "dns") {
		count, limited, err := s.overviewDNSRecordCount(ctx, token, zones)
		if err != nil {
			resp.addMetricUnavailable("dns_records", "dns", err)
		} else {
			metric := OverviewMetric{ID: "dns_records", Feature: "dns", Value: count, Available: true}
			if limited {
				metric.Limited = true
				metric.Limit = overviewDNSZoneCountLimit
			}
			resp.Metrics = append(resp.Metrics, metric)
		}
	} else {
		resp.addMetricMissingScope("dns_records", "dns")
	}

	s.addAccountMetric(ctx, &resp, token, accountID, "tunnels", "tunnels", "/accounts/%s/cfd_tunnel", url.Values{"is_deleted": {"false"}, "per_page": {"100"}})
	s.addAccountMetric(ctx, &resp, token, accountID, "workers", "workers", "/accounts/%s/workers/scripts", url.Values{"per_page": {"100"}})
	s.addR2BucketMetric(ctx, &resp, token, accountID)
	s.addAccountMetric(ctx, &resp, token, accountID, "d1_databases", "d1", "/accounts/%s/d1/database", url.Values{"per_page": {"100"}})
	s.addAccountMetric(ctx, &resp, token, accountID, "kv_namespaces", "kv", "/accounts/%s/storage/kv/namespaces", url.Values{"per_page": {"100"}})
	s.addZoneMetric(ctx, &resp, token, resp.Zone, "snippets", "snippets", "/zones/%s/snippets", url.Values{"per_page": {"100"}})
	s.addWAFRuleMetric(ctx, &resp, token, resp.Zone)

	if status, err := s.CloudflareStatus(ctx); err == nil {
		resp.Status = &status.Overall
	}
	return resp, nil
}

func (s *Service) overviewAccounts(ctx context.Context, token string) ([]Account, error) {
	var accounts []Account
	_, err := s.cfAPI(ctx, token, http.MethodGet, "/accounts", url.Values{"per_page": {"100"}}, "", nil, &accounts)
	return accounts, err
}

func (s *Service) overviewZones(ctx context.Context, token, accountID string) ([]Zone, error) {
	query := url.Values{"per_page": {"100"}}
	if strings.TrimSpace(accountID) != "" {
		query.Set("account.id", strings.TrimSpace(accountID))
	}
	var zones []Zone
	_, err := s.cfAPI(ctx, token, http.MethodGet, "/zones", query, "", nil, &zones)
	return zones, err
}

func (s *Service) overviewDNSRecordCount(ctx context.Context, token string, zones []Zone) (count int, limited bool, err error) {
	if len(zones) == 0 {
		return 0, false, nil
	}
	limit := len(zones)
	if limit > overviewDNSZoneCountLimit {
		limit = overviewDNSZoneCountLimit
		limited = true
	}
	for _, zone := range zones[:limit] {
		var records []DNSRecord
		info, err := s.cfAPI(ctx, token, http.MethodGet, "/zones/"+url.PathEscape(zone.ID)+"/dns_records", url.Values{"page": {"1"}, "per_page": {"5"}}, "", nil, &records)
		if err != nil {
			return 0, limited, err
		}
		count += cfResultCount(info, len(records))
	}
	return count, limited, nil
}

func (s *Service) addAccountMetric(ctx context.Context, resp *OverviewResponse, token, accountID, id, feature, pathPattern string, query url.Values) {
	if !canReadCapability(resp.Capabilities, feature) {
		resp.addMetricMissingScope(id, feature)
		return
	}
	if strings.TrimSpace(accountID) == "" {
		resp.addMetricUnavailableMessage(id, feature, "missing_account")
		return
	}
	count, err := s.overviewListCount(ctx, token, fmt.Sprintf(pathPattern, url.PathEscape(accountID)), query)
	if err != nil {
		resp.addMetricUnavailable(id, feature, err)
		return
	}
	resp.addMetric(id, feature, count)
}

func (s *Service) addZoneMetric(ctx context.Context, resp *OverviewResponse, token string, zone *Zone, id, feature, pathPattern string, query url.Values) {
	if !canReadCapability(resp.Capabilities, feature) {
		resp.addMetricMissingScope(id, feature)
		return
	}
	if zone == nil || strings.TrimSpace(zone.ID) == "" {
		resp.addMetricUnavailableMessage(id, feature, "missing_zone")
		return
	}
	count, err := s.overviewListCount(ctx, token, fmt.Sprintf(pathPattern, url.PathEscape(zone.ID)), query)
	if err != nil {
		resp.addMetricUnavailable(id, feature, err)
		return
	}
	resp.addMetric(id, feature, count)
}

func (s *Service) addR2BucketMetric(ctx context.Context, resp *OverviewResponse, token, accountID string) {
	const id = "r2_buckets"
	const feature = "r2"
	if !canReadCapability(resp.Capabilities, feature) {
		resp.addMetricMissingScope(id, feature)
		return
	}
	if strings.TrimSpace(accountID) == "" {
		resp.addMetricUnavailableMessage(id, feature, "missing_account")
		return
	}
	var result struct {
		Buckets []json.RawMessage `json:"buckets"`
	}
	if _, err := s.cfAPI(ctx, token, http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/r2/buckets", url.Values{"per_page": {"100"}}, "", nil, &result); err != nil {
		resp.addMetricUnavailable(id, feature, err)
		return
	}
	resp.addMetric(id, feature, len(result.Buckets))
}

func (s *Service) addWAFRuleMetric(ctx context.Context, resp *OverviewResponse, token string, zone *Zone) {
	const id = "waf_rules"
	const feature = "waf"
	if !canReadCapability(resp.Capabilities, feature) {
		resp.addMetricMissingScope(id, feature)
		return
	}
	if zone == nil || strings.TrimSpace(zone.ID) == "" {
		resp.addMetricUnavailableMessage(id, feature, "missing_zone")
		return
	}
	var ruleset struct {
		Rules []json.RawMessage `json:"rules"`
	}
	_, err := s.cfAPI(ctx, token, http.MethodGet, "/zones/"+url.PathEscape(zone.ID)+"/rulesets/phases/http_request_firewall_custom/entrypoint", nil, "", nil, &ruleset)
	if err != nil {
		var statusErr *cfAPIStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			resp.addMetric(id, feature, 0)
			return
		}
		resp.addMetricUnavailable(id, feature, err)
		return
	}
	resp.addMetric(id, feature, len(ruleset.Rules))
}

func (s *Service) overviewListCount(ctx context.Context, token, path string, query url.Values) (int, error) {
	var items []json.RawMessage
	info, err := s.cfAPI(ctx, token, http.MethodGet, path, query, "", nil, &items)
	if err != nil {
		return 0, err
	}
	return cfResultCount(info, len(items)), nil
}

func (r *OverviewResponse) addMetric(id, feature string, value int) {
	r.Metrics = append(r.Metrics, OverviewMetric{ID: id, Feature: feature, Value: value, Available: true})
}

func (r *OverviewResponse) addMetricMissingScope(id, feature string) {
	r.addMetricUnavailableMessage(id, feature, "missing_scope")
}

func (r *OverviewResponse) addMetricUnavailable(id, feature string, err error) {
	r.addMetricUnavailableMessage(id, feature, overviewMetricError(err))
}

func (r *OverviewResponse) addMetricUnavailableMessage(id, feature, message string) {
	r.Metrics = append(r.Metrics, OverviewMetric{ID: id, Feature: feature, Available: false, Error: message})
}

func canReadCapability(caps cfoauth.CapabilityMatrix, feature string) bool {
	capability, ok := caps[feature]
	return ok && capability.Read
}

func overviewMetricError(err error) string {
	if err == nil {
		return ""
	}
	var statusErr *cfAPIStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "permission_denied"
		case http.StatusNotFound:
			return "not_found"
		default:
			return "request_failed"
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "request_failed"
	}
	var validationErr ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Message
	}
	return "unavailable"
}

func (s *Service) Accounts(ctx context.Context) (ListResponse[Account], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[Account]{}, err
	}
	var accounts []cloudflare.Account
	for page := 1; ; page++ {
		batch, info, err := client.Accounts(ctx, cloudflare.AccountsListParams{
			PaginationOptions: cloudflare.PaginationOptions{Page: page, PerPage: 100},
		})
		if err != nil {
			return ListResponse[Account]{}, err
		}
		accounts = append(accounts, batch...)
		if info.TotalPages <= 0 || page >= info.TotalPages {
			break
		}
	}
	data := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		data = append(data, Account{ID: account.ID, Name: account.Name, Type: account.Type})
	}
	return ListResponse[Account]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) AccountUsage(ctx context.Context, accountID string) (AccountUsageResponse, error) {
	token, session, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return AccountUsageResponse{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return AccountUsageResponse{}, validationError("account_id is required")
	}
	periodStart, todayStart, now := usageWindows(time.Now().UTC())
	billing := s.accountBillingInfo(ctx, token, accountID)
	if billing.PeriodStart != nil && billing.PeriodStart.Before(now) {
		periodStart = billing.PeriodStart.UTC()
	}
	variables := accountUsageGraphQLVariables{
		AccountTag:  accountID,
		PeriodStart: periodStart.Format(time.RFC3339),
		TodayStart:  todayStart.Format(time.RFC3339),
		Now:         now.Format(time.RFC3339),
	}

	var usageData accountUsageGraphQLData
	if err := s.graphQL(ctx, token, accountUsageQuery, variables, &usageData); err != nil {
		return AccountUsageResponse{}, err
	}
	workers, r2 := mapAccountUsage(usageData)
	if metrics, err := s.r2Metrics(ctx, token, accountID); err == nil {
		if storageBytes, objectCount, ok := r2StandardUsage(metrics); ok {
			r2.StorageBytes = storageBytes
			r2.ObjectCount = objectCount
		}
	}

	var cpuData accountUsageCPUGraphQLData
	if err := s.graphQL(ctx, token, accountUsageCPUQuery, variables, &cpuData); err == nil {
		periodCPU, todayCPU := mapAccountUsageCPU(cpuData)
		workers.CPUTimePeriod = periodCPU
		workers.CPUTimeToday = todayCPU
	}
	var workersErrorsData accountWorkersErrorsGraphQLData
	errorsVariables := accountWorkersErrorsGraphQLVariables{
		AccountTag: accountID,
		Since:      now.Add(-time.Hour).Format(time.RFC3339),
		Until:      now.Format(time.RFC3339),
	}
	if err := s.graphQL(ctx, token, accountWorkersErrorsLastHourQuery, errorsVariables, &workersErrorsData); err == nil {
		workers.ErrorsLastHour = mapAccountWorkersErrorsLastHour(workersErrorsData)
	}

	dateVariables := usageDateGraphQLVariables{
		AccountTag:  accountID,
		PeriodStart: periodStart.Format("2006-01-02"),
		TodayStart:  todayStart.Format("2006-01-02"),
		Until:       now.Format("2006-01-02"),
	}
	var d1Data d1UsageGraphQLData
	d1, d1OK := D1UsageMetrics{}, false
	if err := s.graphQL(ctx, token, d1UsageQuery, dateVariables, &d1Data); err == nil {
		d1 = mapD1Usage(d1Data)
		d1OK = true
	}

	var kvData kvUsageGraphQLData
	kv, kvOK := KVUsageMetrics{}, false
	if err := s.graphQL(ctx, token, kvUsageOperationsQuery, dateVariables, &kvData); err == nil {
		kv = mapKVUsage(kvData)
		kvOK = true
	}
	var kvStorageData kvStorageGraphQLData
	if err := s.graphQL(ctx, token, kvUsageStorageQuery, dateVariables, &kvStorageData); err == nil {
		storageBytes, keyCount := mapKVStorage(kvStorageData)
		kv.StorageBytes = storageBytes
		kv.KeyCount = keyCount
		kvOK = true
	}

	resp := AccountUsageResponse{
		PeriodStart:  periodStart,
		TodayStart:   todayStart,
		Now:          now,
		Billing:      billing,
		Workers:      workers,
		R2:           r2,
		Session:      session,
		Capabilities: session.Capabilities,
	}
	if d1OK {
		resp.D1 = &d1
	}
	if kvOK {
		resp.KV = &kv
	}
	return resp, nil
}

func (s *Service) accountBillingInfo(ctx context.Context, accessToken, accountID string) AccountBillingInfo {
	subscriptions, err := s.accountSubscriptions(ctx, accessToken, accountID)
	if err != nil {
		return AccountBillingInfo{Available: false, Reason: accountBillingUnavailableReason(err)}
	}
	return deriveAccountBillingInfo(subscriptions)
}

func (s *Service) accountSubscriptions(ctx context.Context, accessToken, accountID string) ([]AccountSubscription, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, validationError("account_id is required")
	}
	var subscriptions []AccountSubscription
	_, err := s.cfAPI(ctx, accessToken, http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/subscriptions", nil, "", nil, &subscriptions)
	if err != nil {
		return nil, err
	}
	return subscriptions, nil
}

func (s *Service) Zones(ctx context.Context, accountID string) (ListResponse[Zone], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[Zone]{}, err
	}
	opts := []cloudflare.ReqOption{}
	if strings.TrimSpace(accountID) != "" {
		opts = append(opts, cloudflare.WithZoneFilters("", strings.TrimSpace(accountID), ""))
	}
	zones, err := client.ListZonesContext(ctx, opts...)
	if err != nil {
		return ListResponse[Zone]{}, err
	}
	data := make([]Zone, 0, len(zones.Result))
	for _, zone := range zones.Result {
		data = append(data, mapZone(zone))
	}
	return ListResponse[Zone]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) Zone(ctx context.Context, zoneID string) (ZoneDetailResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ZoneDetailResponse{}, err
	}
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return ZoneDetailResponse{}, validationError("zone_id is required")
	}
	zone, err := client.ZoneDetails(ctx, zoneID)
	if err != nil {
		return ZoneDetailResponse{}, err
	}
	return ZoneDetailResponse{
		Zone:         mapZone(zone),
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) DNSRecords(ctx context.Context, zoneID string) (ListResponse[DNSRecord], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[DNSRecord]{}, err
	}
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return ListResponse[DNSRecord]{}, validationError("zone_id is required")
	}
	records, _, err := client.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{})
	if err != nil {
		return ListResponse[DNSRecord]{}, err
	}
	data := make([]DNSRecord, 0, len(records))
	for _, record := range records {
		data = append(data, mapDNSRecord(record))
	}
	return ListResponse[DNSRecord]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) DNSRecordCount(ctx context.Context, zoneID string) (DNSRecordCountResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return DNSRecordCountResponse{}, err
	}
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return DNSRecordCountResponse{}, validationError("zone_id is required")
	}
	records, info, err := client.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{
		ResultInfo: cloudflare.ResultInfo{Page: 1, PerPage: 5},
	})
	if err != nil {
		return DNSRecordCountResponse{}, err
	}
	return DNSRecordCountResponse{
		Count:        dnsRecordCountFromResult(records, info),
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) CreateDNSRecord(ctx context.Context, zoneID string, req DNSRecordRequest) (DNSRecord, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return DNSRecord{}, err
	}
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return DNSRecord{}, validationError("zone_id is required")
	}
	params, err := createDNSParams(req)
	if err != nil {
		return DNSRecord{}, err
	}
	record, err := client.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), params)
	if err != nil {
		return DNSRecord{}, err
	}
	return mapDNSRecord(record), nil
}

func (s *Service) UpdateDNSRecord(ctx context.Context, zoneID, recordID string, req DNSRecordRequest) (DNSRecord, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return DNSRecord{}, err
	}
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return DNSRecord{}, validationError("zone_id is required")
	}
	params, err := updateDNSParams(recordID, req)
	if err != nil {
		return DNSRecord{}, err
	}
	record, err := client.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), params)
	if err != nil {
		return DNSRecord{}, err
	}
	return mapDNSRecord(record), nil
}

func (s *Service) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return err
	}
	zoneID = strings.TrimSpace(zoneID)
	recordID = strings.TrimSpace(recordID)
	if zoneID == "" || recordID == "" {
		return validationError("zone_id and record id are required")
	}
	return client.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), recordID)
}

func (s *Service) Tunnels(ctx context.Context, accountID string) (ListResponse[Tunnel], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[Tunnel]{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ListResponse[Tunnel]{}, validationError("account_id is required")
	}
	tunnels, _, err := client.ListTunnels(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.TunnelListParams{})
	if err != nil {
		return ListResponse[Tunnel]{}, err
	}
	data := make([]Tunnel, 0, len(tunnels))
	for _, tunnel := range tunnels {
		data = append(data, mapTunnel(tunnel))
	}
	return ListResponse[Tunnel]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) CreateTunnel(ctx context.Context, accountID string, req TunnelCreateRequest) (TunnelCreateResult, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return TunnelCreateResult{}, err
	}
	if !session.Capabilities["tunnels"].Write {
		return TunnelCreateResult{}, validationError("cloudflare tunnel write scope is required")
	}
	accountID = strings.TrimSpace(accountID)
	name, err := normalizeTunnelName(req.Name)
	if err != nil {
		return TunnelCreateResult{}, err
	}
	if accountID == "" {
		return TunnelCreateResult{}, validationError("account_id is required")
	}
	secret, err := generateTunnelSecret()
	if err != nil {
		return TunnelCreateResult{}, err
	}
	tunnel, err := client.CreateTunnel(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.TunnelCreateParams{
		Name:      name,
		Secret:    secret,
		ConfigSrc: "cloudflare",
	})
	if err != nil {
		return TunnelCreateResult{}, err
	}
	token, err := client.GetTunnelToken(ctx, cloudflare.AccountIdentifier(accountID), tunnel.ID)
	if err != nil {
		return TunnelCreateResult{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return TunnelCreateResult{}, fmt.Errorf("cloudflare returned empty tunnel token")
	}
	return TunnelCreateResult{
		Tunnel:       mapTunnel(tunnel),
		Token:        token,
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) Workers(ctx context.Context, accountID string) (ListResponse[WorkerScript], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[WorkerScript]{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ListResponse[WorkerScript]{}, validationError("account_id is required")
	}
	resp, _, err := client.ListWorkers(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.ListWorkersParams{})
	if err != nil {
		return ListResponse[WorkerScript]{}, err
	}
	data := make([]WorkerScript, 0, len(resp.WorkerList))
	for _, script := range resp.WorkerList {
		data = append(data, mapWorkerMetadata(script))
	}
	return ListResponse[WorkerScript]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) Worker(ctx context.Context, accountID, scriptName string) (WorkerDetailResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return WorkerDetailResponse{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return WorkerDetailResponse{}, validationError("account_id is required")
	}
	scriptName, err = normalizeWorkerScriptName(scriptName)
	if err != nil {
		return WorkerDetailResponse{}, err
	}
	settings, err := client.GetWorkersScriptSettings(ctx, cloudflare.AccountIdentifier(accountID), scriptName)
	if err != nil {
		return WorkerDetailResponse{}, err
	}
	content, err := client.GetWorkersScriptContent(ctx, cloudflare.AccountIdentifier(accountID), scriptName)
	if err != nil {
		return WorkerDetailResponse{}, err
	}
	worker := mapWorkerMetadata(settings.WorkerMetaData)
	if worker.ID == "" {
		worker.ID = scriptName
	}
	return WorkerDetailResponse{
		Worker:       worker,
		Content:      mapWorkerContent(content),
		Settings:     mapWorkerSettings(settings),
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) StartWorkerTail(ctx context.Context, accountID, scriptName string) (WorkerTailSession, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WorkerTailSession{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return WorkerTailSession{}, validationError("account_id is required")
	}
	scriptName, err = normalizeWorkerScriptName(scriptName)
	if err != nil {
		return WorkerTailSession{}, err
	}
	tail, err := client.StartWorkersTail(ctx, cloudflare.AccountIdentifier(accountID), scriptName)
	if err != nil {
		return WorkerTailSession{}, err
	}
	return WorkerTailSession{
		ID:        tail.ID,
		URL:       tail.URL,
		ExpiresAt: tail.ExpiresAt,
	}, nil
}

func (s *Service) DeleteWorkerTail(ctx context.Context, accountID, scriptName, tailID string) error {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return validationError("account_id is required")
	}
	scriptName, err = normalizeWorkerScriptName(scriptName)
	if err != nil {
		return err
	}
	tailID = strings.TrimSpace(tailID)
	if tailID == "" {
		return validationError("worker tail id is required")
	}
	return client.DeleteWorkersTail(ctx, cloudflare.AccountIdentifier(accountID), scriptName, tailID)
}

func (s *Service) WorkerMetrics(ctx context.Context, accountID, scriptName, requestedRange string) (WorkerMetricsResponse, error) {
	token, session, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return WorkerMetricsResponse{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return WorkerMetricsResponse{}, validationError("account_id is required")
	}
	scriptName, err = normalizeWorkerScriptName(scriptName)
	if err != nil {
		return WorkerMetricsResponse{}, err
	}
	metricsRange, since, until, err := normalizeAnalyticsRange(requestedRange, time.Now().UTC())
	if err != nil {
		return WorkerMetricsResponse{}, err
	}

	var summaryData workerMetricsGraphQLData
	variables := workerMetricsGraphQLVariables{
		AccountTag: accountID,
		ScriptName: scriptName,
		Since:      since.Format(time.RFC3339),
		Until:      until.Format(time.RFC3339),
	}
	if err := s.graphQL(ctx, token, workerMetricsSummaryQuery, variables, &summaryData); err != nil {
		return WorkerMetricsResponse{}, err
	}
	summary, statuses := mapWorkerMetricsSummary(summaryData)
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Requests > statuses[j].Requests
	})

	var cpuData workerCPUTimeGraphQLData
	if err := s.graphQL(ctx, token, workerMetricsCPUQuery, variables, &cpuData); err == nil {
		if cpuTotal := mapWorkerCPUTotal(cpuData); cpuTotal != nil {
			summary.CPUTimeUs = cpuTotal
		}
	}

	var series []WorkerSeriesPoint
	var seriesData workerSeriesGraphQLData
	seriesQuery := workerMetricsSeriesQuery(metricsRange != "24h")
	if err := s.graphQL(ctx, token, seriesQuery, variables, &seriesData); err == nil {
		series = mapWorkerSeries(seriesData, metricsRange != "24h")
	}

	return WorkerMetricsResponse{
		Range:           metricsRange,
		Since:           since,
		Until:           until,
		Summary:         summary,
		StatusBreakdown: statuses,
		Series:          series,
		Session:         session,
		Capabilities:    session.Capabilities,
	}, nil
}

func (s *Service) R2Buckets(ctx context.Context, accountID string) (ListResponse[R2Bucket], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[R2Bucket]{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ListResponse[R2Bucket]{}, validationError("account_id is required")
	}
	buckets, err := client.ListR2Buckets(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.ListR2BucketsParams{PerPage: 1000})
	if err != nil {
		return ListResponse[R2Bucket]{}, err
	}
	data := make([]R2Bucket, 0, len(buckets))
	for _, bucket := range buckets {
		data = append(data, mapR2Bucket(bucket))
	}
	return ListResponse[R2Bucket]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) CreateR2Bucket(ctx context.Context, accountID string, req R2BucketRequest) (R2Bucket, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return R2Bucket{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return R2Bucket{}, validationError("account_id is required")
	}
	req, err = normalizeR2BucketRequest(req)
	if err != nil {
		return R2Bucket{}, err
	}
	bucket, err := client.CreateR2Bucket(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.CreateR2BucketParameters{
		Name:         req.Name,
		LocationHint: req.LocationHint,
	})
	if err != nil {
		return R2Bucket{}, err
	}
	return mapR2Bucket(bucket), nil
}

func (s *Service) DeleteR2Bucket(ctx context.Context, accountID, bucketName string) error {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return validationError("account_id is required")
	}
	bucketName, err = normalizeR2BucketName(bucketName)
	if err != nil {
		return err
	}
	return client.DeleteR2Bucket(ctx, cloudflare.AccountIdentifier(accountID), bucketName)
}

func (s *Service) R2Metrics(ctx context.Context, accountID string) (R2AccountMetricsResponse, error) {
	token, session, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return R2AccountMetricsResponse{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return R2AccountMetricsResponse{}, validationError("account_id is required")
	}

	metrics, err := s.r2Metrics(ctx, token, accountID)
	if err != nil {
		return R2AccountMetricsResponse{}, err
	}
	return R2AccountMetricsResponse{
		Standard:         metrics.Standard,
		InfrequentAccess: metrics.InfrequentAccess,
		Session:          session,
		Capabilities:     session.Capabilities,
	}, nil
}

func (s *Service) r2Metrics(ctx context.Context, accessToken, accountID string) (R2AccountMetrics, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return R2AccountMetrics{}, validationError("account_id is required")
	}
	var metrics R2AccountMetrics
	if _, err := s.cfAPI(ctx, accessToken, http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/r2/metrics", nil, "", nil, &metrics); err != nil {
		return R2AccountMetrics{}, err
	}
	return metrics, nil
}

func (s *Service) R2Objects(ctx context.Context, accountID, bucketName, cursor string, limit int) (R2ObjectsResponse, error) {
	token, session, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return R2ObjectsResponse{}, err
	}
	accountID, bucketName, err = normalizeR2ObjectContainer(accountID, bucketName)
	if err != nil {
		return R2ObjectsResponse{}, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := url.Values{}
	query.Set("per_page", strconv.Itoa(limit))
	if strings.TrimSpace(cursor) != "" {
		query.Set("cursor", strings.TrimSpace(cursor))
	}
	var objects []R2Object
	info, err := s.cfAPI(ctx, token, http.MethodGet, r2ObjectsPath(accountID, bucketName), query, "", nil, &objects)
	if err != nil {
		return R2ObjectsResponse{}, err
	}
	resp := R2ObjectsResponse{
		Data:         objects,
		Session:      session,
		Capabilities: session.Capabilities,
	}
	if info != nil && info.IsTruncated {
		resp.Cursor = info.Cursor
	}
	return resp, nil
}

func (s *Service) R2ObjectValue(ctx context.Context, accountID, bucketName, key string) (R2ObjectValue, error) {
	token, _, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return R2ObjectValue{}, err
	}
	accountID, bucketName, key, err = normalizeR2ObjectTarget(accountID, bucketName, key)
	if err != nil {
		return R2ObjectValue{}, err
	}
	resp, err := s.cfAPIRaw(ctx, token, http.MethodGet, r2ObjectPath(accountID, bucketName, key), nil, "", nil)
	if err != nil {
		return R2ObjectValue{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxR2ObjectTextValueBytes+1))
	if err != nil {
		return R2ObjectValue{}, err
	}
	size := len(raw)
	if resp.ContentLength > int64(size) {
		maxInt := int64(^uint(0) >> 1)
		if resp.ContentLength <= maxInt {
			size = int(resp.ContentLength)
		}
	}
	value := R2ObjectValue{
		Key:         key,
		Bytes:       size,
		ContentType: resp.Header.Get("Content-Type"),
		Truncated:   len(raw) > maxR2ObjectTextValueBytes,
	}
	if value.Truncated {
		raw = raw[:maxR2ObjectTextValueBytes]
	}
	if utf8.Valid(raw) {
		value.Encoding = "text"
		value.Value = string(raw)
	} else {
		value.Encoding = "binary"
		value.BinaryPreview = r2BinaryPreview(raw, value.Truncated)
	}
	return value, nil
}

func (s *Service) WriteR2ObjectValue(ctx context.Context, accountID, bucketName, key string, req R2ObjectValueRequest) (R2ObjectValue, error) {
	token, _, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return R2ObjectValue{}, err
	}
	accountID, bucketName, key, err = normalizeR2ObjectTarget(accountID, bucketName, key)
	if err != nil {
		return R2ObjectValue{}, err
	}
	data := []byte(req.Value)
	if len(data) > maxR2ObjectWriteBytes {
		return R2ObjectValue{}, validationError("r2 object value is too large")
	}
	contentType := strings.TrimSpace(req.ContentType)
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	if _, err := s.cfAPI(ctx, token, http.MethodPut, r2ObjectPath(accountID, bucketName, key), nil, contentType, bytes.NewReader(data), nil); err != nil {
		return R2ObjectValue{}, err
	}
	return R2ObjectValue{
		Key:         key,
		Value:       req.Value,
		Encoding:    "text",
		Bytes:       len(data),
		ContentType: contentType,
	}, nil
}

func (s *Service) WriteR2ObjectStream(ctx context.Context, accountID, bucketName, key, contentType string, contentLength int64, body io.Reader) (R2ObjectValue, error) {
	token, _, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return R2ObjectValue{}, err
	}
	accountID, bucketName, key, err = normalizeR2ObjectTarget(accountID, bucketName, key)
	if err != nil {
		return R2ObjectValue{}, err
	}
	if body == nil {
		return R2ObjectValue{}, validationError("r2 object upload body is required")
	}
	if contentLength < 0 {
		return R2ObjectValue{}, validationError("r2 object upload content length is required")
	}
	if contentLength > maxR2ObjectUploadBytes {
		return R2ObjectValue{}, validationError("r2 object upload is too large")
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if _, err := s.cfAPI(ctx, token, http.MethodPut, r2ObjectPath(accountID, bucketName, key), nil, contentType, body, nil); err != nil {
		return R2ObjectValue{}, err
	}
	return R2ObjectValue{
		Key:         key,
		Encoding:    "binary",
		Bytes:       int(contentLength),
		ContentType: contentType,
	}, nil
}

func (s *Service) CopyR2Object(ctx context.Context, accountID, bucketName string, req R2ObjectCopyRequest) (R2ObjectValue, error) {
	token, _, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return R2ObjectValue{}, err
	}
	accountID, sourceBucketName, sourceKey, err := normalizeR2ObjectTarget(accountID, bucketName, req.SourceKey)
	if err != nil {
		return R2ObjectValue{}, err
	}
	destinationBucketName := strings.TrimSpace(req.DestinationBucket)
	if destinationBucketName == "" {
		destinationBucketName = sourceBucketName
	}
	_, destinationBucketName, destinationKey, err := normalizeR2ObjectTarget(accountID, destinationBucketName, req.DestinationKey)
	if err != nil {
		return R2ObjectValue{}, err
	}
	if sourceBucketName == destinationBucketName && sourceKey == destinationKey {
		return R2ObjectValue{}, validationError("r2 object destination key must differ from source key")
	}

	source, err := s.cfAPIRaw(ctx, token, http.MethodGet, r2ObjectPath(accountID, sourceBucketName, sourceKey), nil, "", nil)
	if err != nil {
		return R2ObjectValue{}, err
	}
	defer source.Body.Close()

	contentType := strings.TrimSpace(source.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	putResp, err := s.cfAPIRaw(ctx, token, http.MethodPut, r2ObjectPath(accountID, destinationBucketName, destinationKey), nil, contentType, source.Body)
	if err != nil {
		return R2ObjectValue{}, err
	}
	_ = putResp.Body.Close()
	if req.DeleteSource {
		if _, err := s.cfAPI(ctx, token, http.MethodDelete, r2ObjectPath(accountID, sourceBucketName, sourceKey), nil, "", nil, nil); err != nil {
			return R2ObjectValue{}, err
		}
	}
	return R2ObjectValue{
		Key:         destinationKey,
		Encoding:    "binary",
		Bytes:       intFromContentLength(source.ContentLength),
		ContentType: contentType,
	}, nil
}

func (s *Service) R2ObjectDownload(ctx context.Context, accountID, bucketName, key string) (R2ObjectDownload, error) {
	token, _, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return R2ObjectDownload{}, err
	}
	accountID, bucketName, key, err = normalizeR2ObjectTarget(accountID, bucketName, key)
	if err != nil {
		return R2ObjectDownload{}, err
	}
	resp, err := s.cfAPIRaw(ctx, token, http.MethodGet, r2ObjectPath(accountID, bucketName, key), nil, "", nil)
	if err != nil {
		return R2ObjectDownload{}, err
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return R2ObjectDownload{
		Key:           key,
		Body:          resp.Body,
		ContentType:   contentType,
		ContentLength: resp.ContentLength,
		ETag:          resp.Header.Get("ETag"),
		LastModified:  resp.Header.Get("Last-Modified"),
	}, nil
}

func (s *Service) DeleteR2Object(ctx context.Context, accountID, bucketName, key string) error {
	token, _, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return err
	}
	accountID, bucketName, key, err = normalizeR2ObjectTarget(accountID, bucketName, key)
	if err != nil {
		return err
	}
	_, err = s.cfAPI(ctx, token, http.MethodDelete, r2ObjectPath(accountID, bucketName, key), nil, "", nil, nil)
	return err
}

func (s *Service) D1Databases(ctx context.Context, accountID string) (ListResponse[D1Database], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[D1Database]{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ListResponse[D1Database]{}, validationError("account_id is required")
	}
	databases, _, err := client.ListD1Databases(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.ListD1DatabasesParams{})
	if err != nil {
		return ListResponse[D1Database]{}, err
	}
	data := make([]D1Database, 0, len(databases))
	for _, database := range databases {
		data = append(data, mapD1Database(database))
	}
	return ListResponse[D1Database]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) D1Database(ctx context.Context, accountID, databaseID string) (D1DatabaseDetailResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return D1DatabaseDetailResponse{}, err
	}
	accountID, databaseID, err = normalizeD1Target(accountID, databaseID)
	if err != nil {
		return D1DatabaseDetailResponse{}, err
	}
	database, err := client.GetD1Database(ctx, cloudflare.AccountIdentifier(accountID), databaseID)
	if err != nil {
		return D1DatabaseDetailResponse{}, err
	}
	return D1DatabaseDetailResponse{
		Database:     mapD1Database(database),
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) KVNamespaces(ctx context.Context, accountID string) (ListResponse[KVNamespace], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[KVNamespace]{}, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return ListResponse[KVNamespace]{}, validationError("account_id is required")
	}
	namespaces, _, err := client.ListWorkersKVNamespaces(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.ListWorkersKVNamespacesParams{})
	if err != nil {
		return ListResponse[KVNamespace]{}, err
	}
	data := make([]KVNamespace, 0, len(namespaces))
	for _, namespace := range namespaces {
		data = append(data, KVNamespace{ID: namespace.ID, Title: namespace.Title})
	}
	return ListResponse[KVNamespace]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) KVKeys(ctx context.Context, accountID, namespaceID, prefix, cursor string, limit int) (KVKeysResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return KVKeysResponse{}, err
	}
	accountID = strings.TrimSpace(accountID)
	namespaceID = strings.TrimSpace(namespaceID)
	if accountID == "" || namespaceID == "" {
		return KVKeysResponse{}, validationError("account_id and namespace_id are required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	resp, err := client.ListWorkersKVKeys(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.ListWorkersKVsParams{
		NamespaceID: namespaceID,
		Limit:       limit,
		Prefix:      strings.TrimSpace(prefix),
		Cursor:      strings.TrimSpace(cursor),
	})
	if err != nil {
		return KVKeysResponse{}, err
	}
	data := make([]KVKey, 0, len(resp.Result))
	for _, key := range resp.Result {
		data = append(data, KVKey{Name: key.Name, Expiration: key.Expiration})
	}
	return KVKeysResponse{
		Data:         data,
		Cursor:       resp.ResultInfo.Cursor,
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) KVValue(ctx context.Context, accountID, namespaceID, key string) (KVValue, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return KVValue{}, err
	}
	accountID, namespaceID, key, err = normalizeKVValueTarget(accountID, namespaceID, key)
	if err != nil {
		return KVValue{}, err
	}
	data, err := client.GetWorkersKV(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.GetWorkersKVParams{
		NamespaceID: namespaceID,
		Key:         key,
	})
	if err != nil {
		return KVValue{}, err
	}
	value := KVValue{Key: key, Bytes: len(data)}
	switch {
	case len(data) > maxKVTextValueBytes:
		value.Encoding = "too_large"
	case utf8.Valid(data):
		value.Encoding = "text"
		value.Value = string(data)
	default:
		value.Encoding = "binary"
	}
	return value, nil
}

func (s *Service) WriteKVValue(ctx context.Context, accountID, namespaceID, key string, req KVValueRequest) (KVValue, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return KVValue{}, err
	}
	accountID, namespaceID, key, err = normalizeKVValueTarget(accountID, namespaceID, key)
	if err != nil {
		return KVValue{}, err
	}
	if len(req.Value) > maxKVTextValueBytes {
		return KVValue{}, validationError("kv value is too large for the cfui text editor")
	}
	if _, err := client.WriteWorkersKVEntry(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.WriteWorkersKVEntryParams{
		NamespaceID: namespaceID,
		Key:         key,
		Value:       []byte(req.Value),
	}); err != nil {
		return KVValue{}, err
	}
	return KVValue{Key: key, Value: req.Value, Encoding: "text", Bytes: len(req.Value)}, nil
}

func (s *Service) DeleteKVValue(ctx context.Context, accountID, namespaceID, key string) error {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return err
	}
	accountID, namespaceID, key, err = normalizeKVValueTarget(accountID, namespaceID, key)
	if err != nil {
		return err
	}
	_, err = client.DeleteWorkersKVEntry(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.DeleteWorkersKVEntryParams{
		NamespaceID: namespaceID,
		Key:         key,
	})
	return err
}

func (s *Service) QueryD1(ctx context.Context, accountID, databaseID string, req D1QueryRequest) (D1QueryResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return D1QueryResponse{}, err
	}
	accountID = strings.TrimSpace(accountID)
	databaseID = strings.TrimSpace(databaseID)
	if accountID == "" || databaseID == "" {
		return D1QueryResponse{}, validationError("account_id and database_id are required")
	}
	req, err = normalizeD1QueryRequest(req)
	if err != nil {
		return D1QueryResponse{}, err
	}
	results, err := client.QueryD1Database(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.QueryD1DatabaseParams{
		DatabaseID: databaseID,
		SQL:        req.SQL,
		Parameters: req.Params,
	})
	if err != nil {
		return D1QueryResponse{}, err
	}
	data := make([]D1Result, 0, len(results))
	for _, result := range results {
		data = append(data, D1Result{Success: result.Success, Results: result.Results, Meta: result.Meta})
	}
	return D1QueryResponse{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) D1Tables(ctx context.Context, accountID, databaseID string) (ListResponse[string], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[string]{}, err
	}
	accountID, databaseID, err = normalizeD1Target(accountID, databaseID)
	if err != nil {
		return ListResponse[string]{}, err
	}
	results, err := queryD1(ctx, client, accountID, databaseID, D1QueryRequest{
		SQL: "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' ORDER BY name",
	})
	if err != nil {
		return ListResponse[string]{}, err
	}
	tables := make([]string, 0)
	if len(results) > 0 {
		for _, row := range results[0].Results {
			if name := stringFromAny(row["name"]); name != "" {
				tables = append(tables, name)
			}
		}
	}
	return ListResponse[string]{Data: tables, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) D1Table(ctx context.Context, accountID, databaseID, tableName, limitText, offsetText string) (D1TableResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return D1TableResponse{}, err
	}
	accountID, databaseID, tableName, err = normalizeD1TableTarget(accountID, databaseID, tableName)
	if err != nil {
		return D1TableResponse{}, err
	}
	limit, offset, err := normalizeD1TablePage(limitText, offsetText)
	if err != nil {
		return D1TableResponse{}, err
	}
	columns, err := d1TableColumns(ctx, client, accountID, databaseID, tableName)
	if err != nil {
		return D1TableResponse{}, err
	}
	if len(columns) == 0 {
		return D1TableResponse{}, validationError("table was not found or has no columns")
	}
	rowIDKey := d1RowIDKey(columns)
	sql := fmt.Sprintf(
		"SELECT rowid AS %s, * FROM %s LIMIT %d OFFSET %d",
		quoteSQLIdentifier(rowIDKey),
		quoteSQLIdentifier(tableName),
		limit+1,
		offset,
	)
	results, err := queryD1(ctx, client, accountID, databaseID, D1QueryRequest{SQL: sql})
	if err != nil {
		return D1TableResponse{}, err
	}
	rows := []map[string]any{}
	if len(results) > 0 {
		rows = append(rows, results[0].Results...)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return D1TableResponse{
		Table:        tableName,
		Columns:      columns,
		Rows:         rows,
		RowIDKey:     rowIDKey,
		Limit:        limit,
		Offset:       offset,
		HasMore:      hasMore,
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) UpdateD1Row(ctx context.Context, accountID, databaseID, tableName string, req D1RowMutationRequest) (D1MutationResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return D1MutationResponse{}, err
	}
	accountID, databaseID, tableName, err = normalizeD1TableTarget(accountID, databaseID, tableName)
	if err != nil {
		return D1MutationResponse{}, err
	}
	columns, err := d1TableColumns(ctx, client, accountID, databaseID, tableName)
	if err != nil {
		return D1MutationResponse{}, err
	}
	sql, params, err := d1UpdateRowStatement(tableName, columns, req)
	if err != nil {
		return D1MutationResponse{}, err
	}
	results, err := queryD1(ctx, client, accountID, databaseID, D1QueryRequest{SQL: sql, Params: params})
	if err != nil {
		return D1MutationResponse{}, err
	}
	return D1MutationResponse{
		Success:      true,
		Data:         results,
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) DeleteD1Row(ctx context.Context, accountID, databaseID, tableName string, req D1RowMutationRequest) (D1MutationResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return D1MutationResponse{}, err
	}
	accountID, databaseID, tableName, err = normalizeD1TableTarget(accountID, databaseID, tableName)
	if err != nil {
		return D1MutationResponse{}, err
	}
	rowID := strings.TrimSpace(req.RowID)
	if rowID == "" {
		return D1MutationResponse{}, validationError("rowid is required")
	}
	results, err := queryD1(ctx, client, accountID, databaseID, D1QueryRequest{
		SQL:    fmt.Sprintf("DELETE FROM %s WHERE rowid = ?", quoteSQLIdentifier(tableName)),
		Params: []string{rowID},
	})
	if err != nil {
		return D1MutationResponse{}, err
	}
	return D1MutationResponse{
		Success:      true,
		Data:         results,
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) Snippets(ctx context.Context, zoneID string) (ListResponse[Snippet], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[Snippet]{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return ListResponse[Snippet]{}, validationError("zone_id is required")
	}
	snippets, err := client.ListZoneSnippets(ctx, cloudflare.ZoneIdentifier(zoneID))
	if err != nil {
		return ListResponse[Snippet]{}, err
	}
	rules, err := client.ListZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID))
	if err != nil {
		return ListResponse[Snippet]{}, err
	}
	ruleCounts := make(map[string]int, len(rules))
	for _, rule := range rules {
		ruleCounts[rule.SnippetName]++
	}
	data := make([]Snippet, 0, len(snippets))
	for _, snippet := range snippets {
		data = append(data, Snippet{
			Name:       snippet.SnippetName,
			CreatedOn:  snippet.CreatedOn,
			ModifiedOn: snippet.ModifiedOn,
			RuleCount:  ruleCounts[snippet.SnippetName],
		})
	}
	return ListResponse[Snippet]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) CreateOrUpdateSnippet(ctx context.Context, zoneID string, req SnippetRequest) (Snippet, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return Snippet{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return Snippet{}, err
	}
	req, err = normalizeSnippetRequest(req)
	if err != nil {
		return Snippet{}, err
	}
	snippet, err := client.UpdateZoneSnippet(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.SnippetRequest{
		SnippetName: req.Name,
		MainFile:    req.MainFile,
		Files: []cloudflare.SnippetFile{{
			FileName: req.MainFile,
			Content:  req.Code,
		}},
	})
	if err != nil {
		return Snippet{}, err
	}
	if snippet == nil {
		return Snippet{}, validationError("cloudflare returned no snippet")
	}
	return Snippet{
		Name:       snippet.SnippetName,
		CreatedOn:  snippet.CreatedOn,
		ModifiedOn: snippet.ModifiedOn,
	}, nil
}

func (s *Service) SnippetContent(ctx context.Context, zoneID, name string) (SnippetContent, error) {
	token, session, err := s.auth.CurrentAccessToken(ctx)
	if err != nil {
		return SnippetContent{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return SnippetContent{}, err
	}
	name, err = normalizeSnippetName(name)
	if err != nil {
		return SnippetContent{}, err
	}
	path := fmt.Sprintf("/zones/%s/snippets/%s/content", url.PathEscape(zoneID), url.PathEscape(name))
	resp, err := s.cfAPIRaw(ctx, token, http.MethodGet, path, nil, "", nil)
	if err != nil {
		return SnippetContent{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxSnippetContentRespBytes+1))
	if len(raw) > maxSnippetContentRespBytes {
		return SnippetContent{}, validationError("snippet content response is too large")
	}
	content := mapSnippetContent(name, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"), raw)
	content.Session = session
	content.Capabilities = session.Capabilities
	return content, nil
}

func (s *Service) WriteSnippetContent(ctx context.Context, zoneID, name string, req SnippetRequest) (SnippetContent, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return SnippetContent{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return SnippetContent{}, err
	}
	name, err = normalizeSnippetName(name)
	if err != nil {
		return SnippetContent{}, err
	}
	req.Name = name
	req, err = normalizeSnippetRequest(req)
	if err != nil {
		return SnippetContent{}, err
	}
	snippet, err := client.UpdateZoneSnippet(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.SnippetRequest{
		SnippetName: req.Name,
		MainFile:    req.MainFile,
		Files: []cloudflare.SnippetFile{{
			FileName: req.MainFile,
			Content:  req.Code,
		}},
	})
	if err != nil {
		return SnippetContent{}, err
	}
	if snippet == nil {
		return SnippetContent{}, validationError("cloudflare returned no snippet")
	}
	return SnippetContent{
		Name:         req.Name,
		MainFile:     req.MainFile,
		Value:        req.Code,
		Encoding:     "utf-8",
		Bytes:        len([]byte(req.Code)),
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) DeleteSnippet(ctx context.Context, zoneID, name string) error {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return err
	}
	name, err = normalizeSnippetName(name)
	if err != nil {
		return err
	}
	if err := client.DeleteZoneSnippet(ctx, cloudflare.ZoneIdentifier(zoneID), name); err != nil {
		return err
	}
	rules, err := client.ListZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID))
	if err != nil {
		return err
	}
	filtered := make([]cloudflare.SnippetRule, 0, len(rules))
	changed := false
	for _, rule := range rules {
		if rule.SnippetName == name {
			changed = true
			continue
		}
		filtered = append(filtered, rule)
	}
	if changed {
		_, err = client.UpdateZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID), filtered)
	}
	return err
}

func (s *Service) SnippetRules(ctx context.Context, zoneID, snippetName string) (ListResponse[SnippetRule], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	snippetName = strings.TrimSpace(snippetName)
	if snippetName != "" {
		if _, err := normalizeSnippetName(snippetName); err != nil {
			return ListResponse[SnippetRule]{}, err
		}
	}
	rules, err := client.ListZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID))
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	data := make([]SnippetRule, 0, len(rules))
	for _, rule := range rules {
		if snippetName != "" && rule.SnippetName != snippetName {
			continue
		}
		data = append(data, mapSnippetRule(rule))
	}
	return ListResponse[SnippetRule]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) CreateSnippetRule(ctx context.Context, zoneID string, req SnippetRuleRequest) (ListResponse[SnippetRule], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	rule, err := normalizeSnippetRuleRequest(req)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	rules, err := client.ListZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID))
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	rules = append(rules, rule)
	updated, err := client.UpdateZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID), rules)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	return ListResponse[SnippetRule]{Data: mapSnippetRules(updated), Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) UpdateSnippetRule(ctx context.Context, zoneID, ruleID string, req SnippetRuleUpdateRequest) (ListResponse[SnippetRule], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	ruleID, err = normalizeSnippetRuleID(ruleID)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	if req.Enabled == nil {
		return ListResponse[SnippetRule]{}, validationError("enabled is required")
	}
	rules, err := client.ListZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID))
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	found := false
	for i := range rules {
		if rules[i].ID == ruleID {
			found = true
			rules[i].Enabled = req.Enabled
			break
		}
	}
	if !found {
		return ListResponse[SnippetRule]{}, validationError("snippet rule was not found")
	}
	updated, err := client.UpdateZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID), rules)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	return ListResponse[SnippetRule]{Data: mapSnippetRules(updated), Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) DeleteSnippetRule(ctx context.Context, zoneID, ruleID string) (ListResponse[SnippetRule], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	ruleID, err = normalizeSnippetRuleID(ruleID)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	rules, err := client.ListZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID))
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	filtered := make([]cloudflare.SnippetRule, 0, len(rules))
	found := false
	for _, rule := range rules {
		if rule.ID == ruleID {
			found = true
			continue
		}
		filtered = append(filtered, rule)
	}
	if !found {
		return ListResponse[SnippetRule]{}, validationError("snippet rule was not found")
	}
	updated, err := client.UpdateZoneSnippetsRules(ctx, cloudflare.ZoneIdentifier(zoneID), filtered)
	if err != nil {
		return ListResponse[SnippetRule]{}, err
	}
	return ListResponse[SnippetRule]{Data: mapSnippetRules(updated), Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) WAFRules(ctx context.Context, zoneID string) (WAFRuleset, error) {
	return s.wafRules(ctx, zoneID, cloudflare.RulesetPhaseHTTPRequestFirewallCustom, false)
}

func (s *Service) WAFManagedExceptions(ctx context.Context, zoneID string) (WAFRuleset, error) {
	return s.wafRules(ctx, zoneID, cloudflare.RulesetPhaseHTTPRequestFirewallManaged, true)
}

func (s *Service) WAFManagedOverrides(ctx context.Context, zoneID string) (WAFRuleset, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WAFRuleset{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleset, found, err := getWAFRuleset(ctx, client, zoneID, cloudflare.RulesetPhaseHTTPRequestFirewallManaged)
	if err != nil {
		return WAFRuleset{}, err
	}
	if !found {
		return WAFRuleset{Phase: string(cloudflare.RulesetPhaseHTTPRequestFirewallManaged), Rules: []WAFRule{}}, nil
	}
	return mapWAFRulesetByAction(ruleset, "execute"), nil
}

func (s *Service) wafRules(ctx context.Context, zoneID string, phase cloudflare.RulesetPhase, skipOnly bool) (WAFRuleset, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WAFRuleset{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleset, found, err := getWAFRuleset(ctx, client, zoneID, phase)
	if err != nil {
		return WAFRuleset{}, err
	}
	if !found {
		return WAFRuleset{Phase: string(phase), Rules: []WAFRule{}}, nil
	}
	return mapWAFRulesetFiltered(ruleset, skipOnly), nil
}

func (s *Service) CreateWAFRule(ctx context.Context, zoneID string, req WAFRuleRequest) (WAFRuleset, error) {
	return s.createWAFRule(ctx, zoneID, req, cloudflare.RulesetPhaseHTTPRequestFirewallCustom, "cfui custom WAF rules", false, false)
}

func (s *Service) CreateWAFManagedException(ctx context.Context, zoneID string, req WAFRuleRequest) (WAFRuleset, error) {
	return s.createWAFRule(ctx, zoneID, req, cloudflare.RulesetPhaseHTTPRequestFirewallManaged, "cfui WAF managed exceptions", true, true)
}

func (s *Service) CreateWAFManagedOverride(ctx context.Context, zoneID string, req WAFManagedOverrideRequest) (WAFRuleset, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WAFRuleset{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return WAFRuleset{}, err
	}
	rule, err := normalizeWAFManagedOverrideRequest(req)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleset, found, err := getWAFRuleset(ctx, client, zoneID, cloudflare.RulesetPhaseHTTPRequestFirewallManaged)
	if err != nil {
		return WAFRuleset{}, err
	}
	rules := []cloudflare.RulesetRule{rule}
	description := "cfui WAF managed overrides"
	if found {
		rules = append(append([]cloudflare.RulesetRule{}, ruleset.Rules...), rule)
		description = ruleset.Description
	}
	updated, err := client.UpdateEntrypointRuleset(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateEntrypointRulesetParams{
		Phase:       string(cloudflare.RulesetPhaseHTTPRequestFirewallManaged),
		Description: description,
		Rules:       rules,
	})
	if err != nil {
		return WAFRuleset{}, err
	}
	return mapWAFRulesetByAction(updated, "execute"), nil
}

func (s *Service) createWAFRule(ctx context.Context, zoneID string, req WAFRuleRequest, phase cloudflare.RulesetPhase, defaultDescription string, allowManagedSkip bool, skipOnly bool) (WAFRuleset, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WAFRuleset{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return WAFRuleset{}, err
	}
	rule, err := normalizeWAFRuleRequestWithOptions(req, allowManagedSkip, skipOnly)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleset, found, err := getWAFRuleset(ctx, client, zoneID, phase)
	if err != nil {
		return WAFRuleset{}, err
	}
	rules := []cloudflare.RulesetRule{rule}
	description := defaultDescription
	if found {
		rules = append(append([]cloudflare.RulesetRule{}, ruleset.Rules...), rule)
		description = ruleset.Description
	}
	updated, err := client.UpdateEntrypointRuleset(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateEntrypointRulesetParams{
		Phase:       string(phase),
		Description: description,
		Rules:       rules,
	})
	if err != nil {
		return WAFRuleset{}, err
	}
	return mapWAFRulesetFiltered(updated, skipOnly), nil
}

func (s *Service) UpdateWAFRule(ctx context.Context, zoneID, ruleID string, req WAFRuleUpdateRequest) (WAFRuleset, error) {
	return s.updateWAFRule(ctx, zoneID, ruleID, req, cloudflare.RulesetPhaseHTTPRequestFirewallCustom, false, false)
}

func (s *Service) UpdateWAFManagedException(ctx context.Context, zoneID, ruleID string, req WAFRuleUpdateRequest) (WAFRuleset, error) {
	return s.updateWAFRule(ctx, zoneID, ruleID, req, cloudflare.RulesetPhaseHTTPRequestFirewallManaged, true, true)
}

func (s *Service) UpdateWAFManagedOverride(ctx context.Context, zoneID, ruleID string, req WAFManagedOverrideUpdateRequest) (WAFRuleset, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WAFRuleset{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleID, err = normalizeWAFRuleID(ruleID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleset, found, err := getWAFRuleset(ctx, client, zoneID, cloudflare.RulesetPhaseHTTPRequestFirewallManaged)
	if err != nil {
		return WAFRuleset{}, err
	}
	if !found {
		return WAFRuleset{}, validationError("waf ruleset was not found")
	}
	rules := append([]cloudflare.RulesetRule{}, ruleset.Rules...)
	ruleFound := false
	for i := range rules {
		if rules[i].ID == ruleID {
			if rules[i].Action != "execute" {
				return WAFRuleset{}, validationError("waf managed override was not found")
			}
			updatedRule, err := applyWAFManagedOverrideUpdate(rules[i], req)
			if err != nil {
				return WAFRuleset{}, err
			}
			ruleFound = true
			rules[i] = updatedRule
			break
		}
	}
	if !ruleFound {
		return WAFRuleset{}, validationError("waf managed override was not found")
	}
	updated, err := client.UpdateEntrypointRuleset(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateEntrypointRulesetParams{
		Phase:       string(cloudflare.RulesetPhaseHTTPRequestFirewallManaged),
		Description: ruleset.Description,
		Rules:       rules,
	})
	if err != nil {
		return WAFRuleset{}, err
	}
	return mapWAFRulesetByAction(updated, "execute"), nil
}

func (s *Service) updateWAFRule(ctx context.Context, zoneID, ruleID string, req WAFRuleUpdateRequest, phase cloudflare.RulesetPhase, allowManagedSkip bool, skipOnly bool) (WAFRuleset, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WAFRuleset{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleID, err = normalizeWAFRuleID(ruleID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleset, found, err := getWAFRuleset(ctx, client, zoneID, phase)
	if err != nil {
		return WAFRuleset{}, err
	}
	if !found {
		return WAFRuleset{}, validationError("waf ruleset was not found")
	}
	rules := append([]cloudflare.RulesetRule{}, ruleset.Rules...)
	ruleFound := false
	for i := range rules {
		if rules[i].ID == ruleID {
			if skipOnly && rules[i].Action != "skip" {
				return WAFRuleset{}, validationError("waf managed exception was not found")
			}
			updatedRule, err := applyWAFRuleUpdateWithOptions(rules[i], req, allowManagedSkip, skipOnly)
			if err != nil {
				return WAFRuleset{}, err
			}
			ruleFound = true
			rules[i] = updatedRule
			break
		}
	}
	if !ruleFound {
		return WAFRuleset{}, validationError("waf rule was not found")
	}
	updated, err := client.UpdateEntrypointRuleset(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateEntrypointRulesetParams{
		Phase:       string(phase),
		Description: ruleset.Description,
		Rules:       rules,
	})
	if err != nil {
		return WAFRuleset{}, err
	}
	return mapWAFRulesetFiltered(updated, skipOnly), nil
}

func (s *Service) DeleteWAFRule(ctx context.Context, zoneID, ruleID string) (WAFRuleset, error) {
	return s.deleteWAFRule(ctx, zoneID, ruleID, cloudflare.RulesetPhaseHTTPRequestFirewallCustom, false)
}

func (s *Service) DeleteWAFManagedException(ctx context.Context, zoneID, ruleID string) (WAFRuleset, error) {
	return s.deleteWAFRule(ctx, zoneID, ruleID, cloudflare.RulesetPhaseHTTPRequestFirewallManaged, true)
}

func (s *Service) DeleteWAFManagedOverride(ctx context.Context, zoneID, ruleID string) (WAFRuleset, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WAFRuleset{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleID, err = normalizeWAFRuleID(ruleID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleset, found, err := getWAFRuleset(ctx, client, zoneID, cloudflare.RulesetPhaseHTTPRequestFirewallManaged)
	if err != nil {
		return WAFRuleset{}, err
	}
	if !found {
		return WAFRuleset{}, validationError("waf ruleset was not found")
	}
	rules := make([]cloudflare.RulesetRule, 0, len(ruleset.Rules))
	ruleFound := false
	for _, rule := range ruleset.Rules {
		if rule.ID == ruleID {
			if rule.Action != "execute" {
				return WAFRuleset{}, validationError("waf managed override was not found")
			}
			ruleFound = true
			continue
		}
		rules = append(rules, rule)
	}
	if !ruleFound {
		return WAFRuleset{}, validationError("waf managed override was not found")
	}
	updated, err := client.UpdateEntrypointRuleset(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateEntrypointRulesetParams{
		Phase:       string(cloudflare.RulesetPhaseHTTPRequestFirewallManaged),
		Description: ruleset.Description,
		Rules:       rules,
	})
	if err != nil {
		return WAFRuleset{}, err
	}
	return mapWAFRulesetByAction(updated, "execute"), nil
}

func (s *Service) deleteWAFRule(ctx context.Context, zoneID, ruleID string, phase cloudflare.RulesetPhase, skipOnly bool) (WAFRuleset, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return WAFRuleset{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleID, err = normalizeWAFRuleID(ruleID)
	if err != nil {
		return WAFRuleset{}, err
	}
	ruleset, found, err := getWAFRuleset(ctx, client, zoneID, phase)
	if err != nil {
		return WAFRuleset{}, err
	}
	if !found {
		return WAFRuleset{}, validationError("waf ruleset was not found")
	}
	rules := make([]cloudflare.RulesetRule, 0, len(ruleset.Rules))
	ruleFound := false
	for _, rule := range ruleset.Rules {
		if rule.ID == ruleID {
			if skipOnly && rule.Action != "skip" {
				return WAFRuleset{}, validationError("waf managed exception was not found")
			}
			ruleFound = true
			continue
		}
		rules = append(rules, rule)
	}
	if !ruleFound {
		return WAFRuleset{}, validationError("waf rule was not found")
	}
	updated, err := client.UpdateEntrypointRuleset(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateEntrypointRulesetParams{
		Phase:       string(phase),
		Description: ruleset.Description,
		Rules:       rules,
	})
	if err != nil {
		return WAFRuleset{}, err
	}
	return mapWAFRulesetFiltered(updated, skipOnly), nil
}

func (s *Service) ZoneAnalytics(ctx context.Context, zoneID, requestedRange string) (ZoneAnalyticsResponse, error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ZoneAnalyticsResponse{}, err
	}
	zoneID, err = normalizeZoneID(zoneID)
	if err != nil {
		return ZoneAnalyticsResponse{}, err
	}
	analyticsRange, since, until, err := normalizeAnalyticsRange(requestedRange, time.Now().UTC())
	if err != nil {
		return ZoneAnalyticsResponse{}, err
	}
	continuous := true
	data, err := client.ZoneAnalyticsDashboard(ctx, zoneID, cloudflare.ZoneAnalyticsOptions{
		Since:      &since,
		Until:      &until,
		Continuous: &continuous,
	})
	if err != nil {
		return ZoneAnalyticsResponse{}, err
	}
	return ZoneAnalyticsResponse{
		Range:        analyticsRange,
		Since:        since,
		Until:        until,
		Totals:       mapAnalyticsPoint(data.Totals),
		Timeseries:   mapAnalyticsPoints(data.Timeseries),
		Session:      session,
		Capabilities: session.Capabilities,
	}, nil
}

func (s *Service) ZoneSettings(ctx context.Context, zoneID string) (ListResponse[ZoneSetting], error) {
	client, session, err := s.currentClient(ctx)
	if err != nil {
		return ListResponse[ZoneSetting]{}, err
	}
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return ListResponse[ZoneSetting]{}, validationError("zone_id is required")
	}
	settings, err := client.ZoneSettings(ctx, zoneID)
	if err != nil {
		return ListResponse[ZoneSetting]{}, err
	}
	data := make([]ZoneSetting, 0, len(settings.Result))
	for _, setting := range settings.Result {
		if !isDisplayedZoneSetting(setting.ID) {
			continue
		}
		data = append(data, ZoneSetting{
			ID:            setting.ID,
			Editable:      setting.Editable,
			Value:         setting.Value,
			ModifiedOn:    setting.ModifiedOn,
			TimeRemaining: setting.TimeRemaining,
		})
	}
	return ListResponse[ZoneSetting]{Data: data, Session: session, Capabilities: session.Capabilities}, nil
}

func (s *Service) UpdateZoneSetting(ctx context.Context, zoneID, settingID string, req ZoneSettingUpdateRequest) (ZoneSetting, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return ZoneSetting{}, err
	}
	zoneID = strings.TrimSpace(zoneID)
	settingID = strings.TrimSpace(settingID)
	if zoneID == "" || settingID == "" {
		return ZoneSetting{}, validationError("zone_id and setting id are required")
	}
	value, err := normalizeZoneSettingValue(settingID, req.Value)
	if err != nil {
		return ZoneSetting{}, err
	}
	setting, err := client.UpdateZoneSetting(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.UpdateZoneSettingParams{
		Name:  settingID,
		Value: value,
	})
	if err != nil {
		return ZoneSetting{}, err
	}
	return ZoneSetting{
		ID:            setting.ID,
		Editable:      setting.Editable,
		Value:         setting.Value,
		ModifiedOn:    setting.ModifiedOn,
		TimeRemaining: setting.TimeRemaining,
	}, nil
}

func (s *Service) PurgeZoneCache(ctx context.Context, zoneID string) (CachePurgeResult, error) {
	client, _, err := s.currentClient(ctx)
	if err != nil {
		return CachePurgeResult{}, err
	}
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return CachePurgeResult{}, validationError("zone_id is required")
	}
	resp, err := client.PurgeEverything(ctx, zoneID)
	if err != nil {
		return CachePurgeResult{}, err
	}
	return CachePurgeResult{ID: resp.Result.ID, Success: resp.Success}, nil
}

func createDNSParams(req DNSRecordRequest) (cloudflare.CreateDNSRecordParams, error) {
	req = normalizeDNSRequest(req)
	if req.Type == "" || req.Name == "" || req.Content == "" {
		return cloudflare.CreateDNSRecordParams{}, validationError("type, name, and content are required")
	}
	return cloudflare.CreateDNSRecordParams{
		Type:    req.Type,
		Name:    req.Name,
		Content: req.Content,
		TTL:     normalizeTTL(req.TTL),
		Proxied: req.Proxied,
		Comment: req.Comment,
	}, nil
}

func updateDNSParams(recordID string, req DNSRecordRequest) (cloudflare.UpdateDNSRecordParams, error) {
	req = normalizeDNSRequest(req)
	recordID = strings.TrimSpace(recordID)
	if recordID == "" || req.Type == "" || req.Name == "" || req.Content == "" {
		return cloudflare.UpdateDNSRecordParams{}, validationError("record id, type, name, and content are required")
	}
	comment := req.Comment
	return cloudflare.UpdateDNSRecordParams{
		ID:      recordID,
		Type:    req.Type,
		Name:    req.Name,
		Content: req.Content,
		TTL:     normalizeTTL(req.TTL),
		Proxied: req.Proxied,
		Comment: &comment,
	}, nil
}

func normalizeDNSRequest(req DNSRecordRequest) DNSRecordRequest {
	req.Type = strings.ToUpper(strings.TrimSpace(req.Type))
	req.Name = strings.TrimSpace(req.Name)
	req.Content = strings.TrimSpace(req.Content)
	req.Comment = strings.TrimSpace(req.Comment)
	return req
}

func normalizeTTL(ttl int) int {
	if ttl <= 0 {
		return 1
	}
	return ttl
}

func normalizeR2ObjectContainer(accountID, bucketName string) (string, string, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", "", validationError("account_id is required")
	}
	bucketName, err := normalizeR2BucketName(bucketName)
	if err != nil {
		return "", "", err
	}
	return accountID, bucketName, nil
}

func normalizeR2ObjectTarget(accountID, bucketName, key string) (string, string, string, error) {
	accountID, bucketName, err := normalizeR2ObjectContainer(accountID, bucketName)
	if err != nil {
		return "", "", "", err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", "", validationError("r2 object key is required")
	}
	if len([]byte(key)) > maxR2ObjectKeyBytes {
		return "", "", "", validationError("r2 object key is too long")
	}
	return accountID, bucketName, key, nil
}

func r2BinaryPreview(raw []byte, sourceTruncated bool) *R2BinaryPreview {
	limit := len(raw)
	if limit > maxR2ObjectBinaryPreviewBytes {
		limit = maxR2ObjectBinaryPreviewBytes
	}
	sample := raw[:limit]
	return &R2BinaryPreview{
		Bytes:     len(sample),
		Hexdump:   hexdump(sample),
		Truncated: sourceTruncated || len(raw) > len(sample),
	}
}

func hexdump(data []byte) string {
	var b strings.Builder
	for offset := 0; offset < len(data); offset += 16 {
		line := data[offset:min(offset+16, len(data))]
		if offset > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%08x  ", offset)
		for i := 0; i < 16; i++ {
			if i == 8 {
				b.WriteByte(' ')
			}
			if i < len(line) {
				fmt.Fprintf(&b, "%02x ", line[i])
			} else {
				b.WriteString("   ")
			}
		}
		b.WriteString(" |")
		for _, c := range line {
			if c >= 32 && c <= 126 {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteByte('|')
	}
	return b.String()
}

func r2ObjectsPath(accountID, bucketName string) string {
	return fmt.Sprintf("/accounts/%s/r2/buckets/%s/objects", url.PathEscape(accountID), url.PathEscape(bucketName))
}

func r2ObjectPath(accountID, bucketName, key string) string {
	return r2ObjectsPath(accountID, bucketName) + "/" + url.PathEscape(key)
}

func intFromContentLength(contentLength int64) int {
	if contentLength <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if contentLength > maxInt {
		return 0
	}
	return int(contentLength)
}

func normalizeKVValueTarget(accountID, namespaceID, key string) (string, string, string, error) {
	accountID = strings.TrimSpace(accountID)
	namespaceID = strings.TrimSpace(namespaceID)
	key = strings.TrimSpace(key)
	if accountID == "" || namespaceID == "" || key == "" {
		return "", "", "", validationError("account_id, namespace_id, and key are required")
	}
	return accountID, namespaceID, key, nil
}

func normalizeZoneID(zoneID string) (string, error) {
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return "", validationError("zone_id is required")
	}
	return zoneID, nil
}

func normalizeSnippetRequest(req SnippetRequest) (SnippetRequest, error) {
	name, err := normalizeSnippetName(req.Name)
	if err != nil {
		return SnippetRequest{}, err
	}
	req.Name = name
	if strings.TrimSpace(req.Code) == "" {
		return SnippetRequest{}, validationError("snippet code is required")
	}
	if len(req.Code) > maxSnippetCodeBytes {
		return SnippetRequest{}, validationError("snippet code is too large")
	}
	req.MainFile = strings.TrimSpace(req.MainFile)
	if req.MainFile == "" {
		req.MainFile = "snippet.js"
	}
	if len(req.MainFile) > maxSnippetFileLen {
		return SnippetRequest{}, validationError("snippet main file name is too long")
	}
	for _, r := range req.MainFile {
		if !(r == '.' || r == '-' || r == '_' || isASCIILetter(r) || isASCIIDigit(r)) {
			return SnippetRequest{}, validationError("snippet main file name contains unsupported characters")
		}
	}
	return req, nil
}

func normalizeSnippetName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", validationError("snippet name is required")
	}
	if len(name) > maxSnippetNameLen {
		return "", validationError("snippet name is too long")
	}
	for _, r := range name {
		if !(r == '_' || isASCIILetter(r) || isASCIIDigit(r)) {
			return "", validationError("snippet name may only contain letters, numbers, and underscores")
		}
	}
	return name, nil
}

func normalizeSnippetRuleRequest(req SnippetRuleRequest) (cloudflare.SnippetRule, error) {
	name, err := normalizeSnippetName(req.SnippetName)
	if err != nil {
		return cloudflare.SnippetRule{}, err
	}
	expression := strings.TrimSpace(req.Expression)
	if expression == "" {
		return cloudflare.SnippetRule{}, validationError("snippet rule expression is required")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return cloudflare.SnippetRule{
		SnippetName: name,
		Expression:  expression,
		Description: strings.TrimSpace(req.Description),
		Enabled:     &enabled,
	}, nil
}

func normalizeSnippetRuleID(ruleID string) (string, error) {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return "", validationError("snippet rule id is required")
	}
	return ruleID, nil
}

func normalizeWAFRuleRequest(req WAFRuleRequest) (cloudflare.RulesetRule, error) {
	return normalizeWAFRuleRequestWithOptions(req, false, false)
}

func normalizeWAFRuleRequestWithOptions(req WAFRuleRequest, allowManagedSkip bool, skipOnly bool) (cloudflare.RulesetRule, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if skipOnly && action == "" {
		action = "skip"
	}
	if skipOnly && action != "skip" {
		return cloudflare.RulesetRule{}, validationError("waf managed exception action must be skip")
	}
	if !isSupportedWAFAction(action) {
		return cloudflare.RulesetRule{}, validationError("unsupported waf action")
	}
	expression := strings.TrimSpace(req.Expression)
	if expression == "" {
		return cloudflare.RulesetRule{}, validationError("waf expression is required")
	}
	if len(expression) > maxWAFExpressionLen {
		return cloudflare.RulesetRule{}, validationError("waf expression is too long")
	}
	description := strings.TrimSpace(req.Description)
	if len(description) > maxWAFDescriptionLen {
		return cloudflare.RulesetRule{}, validationError("waf description is too long")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	actionParameters, err := normalizeWAFActionParameters(action, req.ActionParameters, allowManagedSkip, skipOnly)
	if err != nil {
		return cloudflare.RulesetRule{}, err
	}
	rule := cloudflare.RulesetRule{
		Action:           action,
		ActionParameters: actionParameters,
		Expression:       expression,
		Description:      description,
		Enabled:          &enabled,
	}
	if req.RateLimit.Set {
		rateLimit, clear, err := decodeWAFAdvancedObject[cloudflare.RulesetRuleRateLimit]("ratelimit", req.RateLimit)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		if !clear {
			rule.RateLimit = rateLimit
		}
	}
	return rule, nil
}

func applyWAFRuleUpdate(existing cloudflare.RulesetRule, req WAFRuleUpdateRequest) (cloudflare.RulesetRule, error) {
	return applyWAFRuleUpdateWithOptions(existing, req, false, false)
}

func normalizeWAFManagedOverrideRequest(req WAFManagedOverrideRequest) (cloudflare.RulesetRule, error) {
	managedRulesetID, err := normalizeWAFManagedRulesetID(req.ManagedRulesetID)
	if err != nil {
		return cloudflare.RulesetRule{}, err
	}
	expression, err := normalizeWAFExpression(req.Expression)
	if err != nil {
		return cloudflare.RulesetRule{}, err
	}
	description, err := normalizeWAFDescription(req.Description)
	if err != nil {
		return cloudflare.RulesetRule{}, err
	}
	overrides, err := normalizeWAFManagedOverrides(req.Overrides)
	if err != nil {
		return cloudflare.RulesetRule{}, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return cloudflare.RulesetRule{
		Action:      "execute",
		Expression:  expression,
		Description: description,
		Enabled:     &enabled,
		ActionParameters: &cloudflare.RulesetRuleActionParameters{
			ID:        managedRulesetID,
			Overrides: overrides,
		},
	}, nil
}

func applyWAFManagedOverrideUpdate(existing cloudflare.RulesetRule, req WAFManagedOverrideUpdateRequest) (cloudflare.RulesetRule, error) {
	updated := existing
	updated.ActionParameters = cloneWAFActionParameters(existing.ActionParameters)
	if updated.Action != "execute" {
		return cloudflare.RulesetRule{}, validationError("waf managed override action must be execute")
	}
	hasChange := false
	if req.Enabled != nil {
		hasChange = true
		updated.Enabled = req.Enabled
	}
	if req.Expression != nil {
		hasChange = true
		expression, err := normalizeWAFExpression(*req.Expression)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		updated.Expression = expression
	}
	if req.Description != nil {
		hasChange = true
		description, err := normalizeWAFDescription(*req.Description)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		updated.Description = description
	}
	if req.ManagedRulesetID != nil {
		hasChange = true
		managedRulesetID, err := normalizeWAFManagedRulesetID(*req.ManagedRulesetID)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		if updated.ActionParameters == nil {
			updated.ActionParameters = &cloudflare.RulesetRuleActionParameters{}
		}
		updated.ActionParameters.ID = managedRulesetID
	}
	if req.Overrides.Set {
		hasChange = true
		if updated.ActionParameters == nil {
			updated.ActionParameters = &cloudflare.RulesetRuleActionParameters{}
		}
		overrides, clear, err := decodeWAFAdvancedObject[WAFManagedOverrides]("overrides", req.Overrides)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		if clear {
			updated.ActionParameters.Overrides = nil
		} else {
			normalized, err := normalizeWAFManagedOverrides(overrides)
			if err != nil {
				return cloudflare.RulesetRule{}, err
			}
			updated.ActionParameters.Overrides = normalized
		}
	}
	if !hasChange {
		return cloudflare.RulesetRule{}, validationError("waf managed override update is empty")
	}
	if updated.ActionParameters == nil || strings.TrimSpace(updated.ActionParameters.ID) == "" {
		return cloudflare.RulesetRule{}, validationError("managed ruleset id is required")
	}
	return updated, nil
}

func cloneWAFActionParameters(params *cloudflare.RulesetRuleActionParameters) *cloudflare.RulesetRuleActionParameters {
	if params == nil {
		return nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		cloned := *params
		return &cloned
	}
	var out cloudflare.RulesetRuleActionParameters
	if err := json.Unmarshal(raw, &out); err != nil {
		cloned := *params
		return &cloned
	}
	return &out
}

func applyWAFRuleUpdateWithOptions(existing cloudflare.RulesetRule, req WAFRuleUpdateRequest, allowManagedSkip bool, skipOnly bool) (cloudflare.RulesetRule, error) {
	updated := existing
	hasChange := false
	actionTouched := req.Action != nil
	actionParamsTouched := req.ActionParameters != nil
	actionParamsAdvancedTouched := req.ActionParametersJSON.Set

	if req.Enabled != nil {
		hasChange = true
		updated.Enabled = req.Enabled
	}
	if req.Expression != nil {
		hasChange = true
		expression := strings.TrimSpace(*req.Expression)
		if expression == "" {
			return cloudflare.RulesetRule{}, validationError("waf expression is required")
		}
		if len(expression) > maxWAFExpressionLen {
			return cloudflare.RulesetRule{}, validationError("waf expression is too long")
		}
		updated.Expression = expression
	}
	if req.Description != nil {
		hasChange = true
		description := strings.TrimSpace(*req.Description)
		if len(description) > maxWAFDescriptionLen {
			return cloudflare.RulesetRule{}, validationError("waf description is too long")
		}
		updated.Description = description
	}
	if actionTouched {
		hasChange = true
		action := strings.ToLower(strings.TrimSpace(*req.Action))
		if skipOnly && action != "skip" {
			return cloudflare.RulesetRule{}, validationError("waf managed exception action must be skip")
		}
		if !isSupportedWAFAction(action) {
			return cloudflare.RulesetRule{}, validationError("unsupported waf action")
		}
		updated.Action = action
	}
	if actionParamsTouched {
		hasChange = true
	}
	if actionParamsAdvancedTouched || req.RateLimit.Set || req.Logging.Set || req.ExposedCredentialCheck.Set {
		hasChange = true
	}
	if !hasChange {
		return cloudflare.RulesetRule{}, validationError("waf rule update is empty")
	}

	if actionTouched || actionParamsTouched {
		if !isSupportedWAFAction(updated.Action) {
			return cloudflare.RulesetRule{}, validationError("unsupported waf action")
		}
		if updated.Action == "skip" {
			params := req.ActionParameters
			if params == nil {
				if existing.Action == "skip" && existing.ActionParameters != nil {
					updated.ActionParameters = existing.ActionParameters
				} else {
					return cloudflare.RulesetRule{}, validationError("skip action requires action parameters")
				}
			} else {
				actionParameters, err := normalizeWAFActionParameters(updated.Action, params, allowManagedSkip, skipOnly)
				if err != nil {
					return cloudflare.RulesetRule{}, err
				}
				updated.ActionParameters = actionParameters
			}
		} else {
			updated.ActionParameters = nil
		}
	}
	if actionParamsAdvancedTouched {
		actionParameters, clear, err := decodeWAFAdvancedObject[cloudflare.RulesetRuleActionParameters]("action_parameters_json", req.ActionParametersJSON)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		if clear {
			updated.ActionParameters = nil
		} else {
			if err := validateWAFActionParametersForMode(updated.Action, actionParameters, allowManagedSkip, skipOnly); err != nil {
				return cloudflare.RulesetRule{}, err
			}
			updated.ActionParameters = actionParameters
		}
	}
	if req.RateLimit.Set {
		rateLimit, clear, err := decodeWAFAdvancedObject[cloudflare.RulesetRuleRateLimit]("ratelimit", req.RateLimit)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		if clear {
			updated.RateLimit = nil
		} else {
			updated.RateLimit = rateLimit
		}
	}
	if req.Logging.Set {
		logging, clear, err := decodeWAFAdvancedObject[cloudflare.RulesetRuleLogging]("logging", req.Logging)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		if clear {
			updated.Logging = nil
		} else {
			updated.Logging = logging
		}
	}
	if req.ExposedCredentialCheck.Set {
		credentialCheck, clear, err := decodeWAFAdvancedObject[cloudflare.RulesetRuleExposedCredentialCheck]("exposed_credential_check", req.ExposedCredentialCheck)
		if err != nil {
			return cloudflare.RulesetRule{}, err
		}
		if clear {
			updated.ExposedCredentialCheck = nil
		} else {
			updated.ExposedCredentialCheck = credentialCheck
		}
	}
	if updated.Action == "skip" && updated.ActionParameters == nil {
		return cloudflare.RulesetRule{}, validationError("skip action requires action parameters")
	}

	return updated, nil
}

func normalizeWAFExpression(expression string) (string, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return "", validationError("waf expression is required")
	}
	if len(expression) > maxWAFExpressionLen {
		return "", validationError("waf expression is too long")
	}
	return expression, nil
}

func normalizeWAFDescription(description string) (string, error) {
	description = strings.TrimSpace(description)
	if len(description) > maxWAFDescriptionLen {
		return "", validationError("waf description is too long")
	}
	return description, nil
}

func decodeWAFAdvancedObject[T any](field string, value optionalWAFJSON) (*T, bool, error) {
	raw := bytes.TrimSpace(value.Raw)
	if len(raw) == 0 {
		return nil, false, validationError("waf %s json is required", field)
	}
	if len(raw) > maxWAFAdvancedJSONBytes {
		return nil, false, validationError("waf %s json is too large", field)
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, true, nil
	}
	if raw[0] != '{' {
		return nil, false, validationError("waf %s json must be an object or null", field)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, validationError("waf %s json is invalid: %v", field, err)
	}
	return &out, false, nil
}

func normalizeWAFRuleID(ruleID string) (string, error) {
	ruleID = strings.TrimSpace(ruleID)
	if ruleID == "" {
		return "", validationError("waf rule id is required")
	}
	return ruleID, nil
}

func normalizeWAFManagedRulesetID(rulesetID string) (string, error) {
	rulesetID = strings.TrimSpace(rulesetID)
	if rulesetID == "" {
		return "", validationError("managed ruleset id is required")
	}
	if len(rulesetID) > 256 {
		return "", validationError("managed ruleset id is too long")
	}
	return rulesetID, nil
}

func isSupportedWAFAction(action string) bool {
	switch action {
	case "block", "challenge", "managed_challenge", "js_challenge", "log", "skip":
		return true
	default:
		return false
	}
}

func normalizeWAFManagedOverrides(params *WAFManagedOverrides) (*cloudflare.RulesetRuleActionParametersOverrides, error) {
	if params == nil {
		return nil, nil
	}
	action, err := normalizeWAFManagedOverrideAction(params.Action)
	if err != nil {
		return nil, err
	}
	sensitivity, err := normalizeWAFManagedSensitivity(params.SensitivityLevel)
	if err != nil {
		return nil, err
	}
	categories, err := normalizeWAFManagedOverrideCategories(params.Categories)
	if err != nil {
		return nil, err
	}
	rules, err := normalizeWAFManagedOverrideRules(params.Rules)
	if err != nil {
		return nil, err
	}
	if params.Enabled == nil && action == "" && sensitivity == "" && len(categories) == 0 && len(rules) == 0 {
		return nil, nil
	}
	return &cloudflare.RulesetRuleActionParametersOverrides{
		Enabled:          params.Enabled,
		Action:           action,
		SensitivityLevel: sensitivity,
		Categories:       categories,
		Rules:            rules,
	}, nil
}

func normalizeWAFManagedOverrideCategories(values []WAFManagedCategoryOverride) ([]cloudflare.RulesetRuleActionParametersCategories, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]cloudflare.RulesetRuleActionParametersCategories, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		category := strings.TrimSpace(value.Category)
		if category == "" {
			return nil, validationError("managed ruleset category is required")
		}
		if len(category) > 128 {
			return nil, validationError("managed ruleset category is too long")
		}
		if _, ok := seen[category]; ok {
			return nil, validationError("managed ruleset category is duplicated")
		}
		seen[category] = struct{}{}
		action, err := normalizeWAFManagedOverrideAction(value.Action)
		if err != nil {
			return nil, err
		}
		out = append(out, cloudflare.RulesetRuleActionParametersCategories{
			Category: category,
			Action:   action,
			Enabled:  value.Enabled,
		})
	}
	return out, nil
}

func normalizeWAFManagedOverrideRules(values []WAFManagedRuleOverride) ([]cloudflare.RulesetRuleActionParametersRules, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]cloudflare.RulesetRuleActionParametersRules, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		ruleID := strings.TrimSpace(value.ID)
		if ruleID == "" {
			return nil, validationError("managed rule id is required")
		}
		if len(ruleID) > 256 {
			return nil, validationError("managed rule id is too long")
		}
		if _, ok := seen[ruleID]; ok {
			return nil, validationError("managed rule id is duplicated")
		}
		seen[ruleID] = struct{}{}
		action, err := normalizeWAFManagedOverrideAction(value.Action)
		if err != nil {
			return nil, err
		}
		sensitivity, err := normalizeWAFManagedSensitivity(value.SensitivityLevel)
		if err != nil {
			return nil, err
		}
		if value.ScoreThreshold < 0 || value.ScoreThreshold > 100 {
			return nil, validationError("managed rule score threshold must be between 0 and 100")
		}
		out = append(out, cloudflare.RulesetRuleActionParametersRules{
			ID:               ruleID,
			Action:           action,
			Enabled:          value.Enabled,
			ScoreThreshold:   value.ScoreThreshold,
			SensitivityLevel: sensitivity,
		})
	}
	return out, nil
}

func normalizeWAFManagedOverrideAction(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "", "block", "challenge", "managed_challenge", "js_challenge", "log":
		return action, nil
	default:
		return "", validationError("unsupported managed ruleset override action")
	}
}

func normalizeWAFManagedSensitivity(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "default", "high", "medium", "low", "eoff":
		return value, nil
	default:
		return "", validationError("unsupported managed ruleset sensitivity level")
	}
}

func normalizeWAFActionParameters(action string, params *WAFRuleActionParameters, allowManagedSkip bool, skipOnly bool) (*cloudflare.RulesetRuleActionParameters, error) {
	if action != "skip" {
		return nil, nil
	}
	if params == nil {
		return nil, validationError("skip action requires action parameters")
	}
	ruleset := strings.TrimSpace(params.Ruleset)
	if ruleset != "" && ruleset != "current" {
		return nil, validationError("unsupported skip ruleset")
	}
	products, err := normalizeWAFSkipValues(params.Products, supportedWAFSkipProducts(), "skip product")
	if err != nil {
		return nil, err
	}
	phases, err := normalizeWAFSkipValues(params.Phases, supportedWAFSkipPhases(), "skip phase")
	if err != nil {
		return nil, err
	}
	if !allowManagedSkip && (len(params.Rulesets) > 0 || len(params.Rules) > 0) {
		return nil, validationError("unsupported managed skip target")
	}
	if skipOnly && (len(products) > 0 || len(phases) > 0) {
		return nil, validationError("waf managed exceptions can only skip managed rulesets or managed rules")
	}
	rulesets, err := normalizeWAFIDList(params.Rulesets, "skip ruleset")
	if err != nil {
		return nil, err
	}
	rules, err := normalizeWAFRuleTargets(params.Rules)
	if err != nil {
		return nil, err
	}
	if ruleset == "" && len(products) == 0 && len(phases) == 0 && len(rulesets) == 0 && len(rules) == 0 {
		return nil, validationError("skip action requires at least one skip option")
	}
	return &cloudflare.RulesetRuleActionParameters{
		Ruleset:  ruleset,
		Rulesets: rulesets,
		Rules:    rules,
		Products: products,
		Phases:   phases,
	}, nil
}

func validateWAFActionParametersForMode(action string, params *cloudflare.RulesetRuleActionParameters, allowManagedSkip bool, skipOnly bool) error {
	if action != "skip" || params == nil {
		return nil
	}
	if !allowManagedSkip && (len(params.Rulesets) > 0 || len(params.Rules) > 0) {
		return validationError("unsupported managed skip target")
	}
	if skipOnly {
		if len(params.Products) > 0 || len(params.Phases) > 0 {
			return validationError("waf managed exceptions can only skip managed rulesets or managed rules")
		}
		raw := structJSONMap(params)
		for key := range raw {
			switch key {
			case "ruleset", "rulesets", "rules":
			default:
				return validationError("unsupported waf managed exception action parameter")
			}
		}
	}
	return nil
}

func normalizeWAFIDList(values []string, label string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, validationError("%s is required", label)
	}
	return out, nil
}

func normalizeWAFRuleTargets(values map[string][]string) (map[string][]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(values))
	for rawRulesetID, rawRuleIDs := range values {
		rulesetID := strings.TrimSpace(rawRulesetID)
		if rulesetID == "" {
			return nil, validationError("skip rule ruleset id is required")
		}
		ruleIDs, err := normalizeWAFIDList(rawRuleIDs, "skip rule id")
		if err != nil {
			return nil, err
		}
		out[rulesetID] = ruleIDs
	}
	return out, nil
}

func normalizeWAFSkipValues(values []string, supported map[string]struct{}, label string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := supported[value]; !ok {
			return nil, validationError("unsupported %s", label)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func supportedWAFSkipProducts() map[string]struct{} {
	return map[string]struct{}{
		"zoneLockdown":  {},
		"uaBlock":       {},
		"bic":           {},
		"hot":           {},
		"securityLevel": {},
		"rateLimit":     {},
		"waf":           {},
	}
}

func supportedWAFSkipPhases() map[string]struct{} {
	return map[string]struct{}{
		"http_ratelimit":                {},
		"http_request_sbfm":             {},
		"http_request_firewall_managed": {},
	}
}

func normalizeWorkerScriptName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", validationError("worker script name is required")
	}
	if len(name) > maxWorkerScriptNameLen {
		return "", validationError("worker script name is too long")
	}
	if strings.ContainsAny(name, `/\`) {
		return "", validationError("worker script name cannot contain slashes")
	}
	return name, nil
}

func normalizeAnalyticsRange(value string, now time.Time) (string, time.Time, time.Time, error) {
	now = now.UTC()
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "24h":
		return "24h", now.Add(-24 * time.Hour), now, nil
	case "7d":
		return "7d", now.Add(-7 * 24 * time.Hour), now, nil
	case "30d":
		return "30d", now.Add(-30 * 24 * time.Hour), now, nil
	default:
		return "", time.Time{}, time.Time{}, validationError("analytics range must be 24h, 7d, or 30d")
	}
}

func normalizeD1QueryRequest(req D1QueryRequest) (D1QueryRequest, error) {
	req.SQL = strings.TrimSpace(req.SQL)
	if req.SQL == "" {
		return D1QueryRequest{}, validationError("sql is required")
	}
	if len(req.SQL) > maxD1SQLBytes {
		return D1QueryRequest{}, validationError("sql is too large")
	}
	if len(req.Params) > maxD1Parameters {
		return D1QueryRequest{}, validationError("too many sql parameters")
	}
	for _, param := range req.Params {
		if len(param) > maxD1CellValueBytes {
			return D1QueryRequest{}, validationError("sql parameter is too large")
		}
	}
	return req, nil
}

func normalizeD1Target(accountID, databaseID string) (string, string, error) {
	accountID = strings.TrimSpace(accountID)
	databaseID = strings.TrimSpace(databaseID)
	if accountID == "" || databaseID == "" {
		return "", "", validationError("account_id and database_id are required")
	}
	return accountID, databaseID, nil
}

func normalizeD1TableTarget(accountID, databaseID, tableName string) (string, string, string, error) {
	var err error
	accountID, databaseID, err = normalizeD1Target(accountID, databaseID)
	if err != nil {
		return "", "", "", err
	}
	tableName = strings.TrimSpace(tableName)
	if tableName == "" {
		return "", "", "", validationError("table is required")
	}
	if len(tableName) > maxD1IdentifierLen {
		return "", "", "", validationError("table name is too long")
	}
	return accountID, databaseID, tableName, nil
}

func normalizeD1TablePage(limitText, offsetText string) (int, int, error) {
	limit := defaultD1TableLimit
	offset := 0
	var err error
	if strings.TrimSpace(limitText) != "" {
		limit, err = strconv.Atoi(strings.TrimSpace(limitText))
		if err != nil {
			return 0, 0, validationError("limit must be a number")
		}
	}
	if strings.TrimSpace(offsetText) != "" {
		offset, err = strconv.Atoi(strings.TrimSpace(offsetText))
		if err != nil {
			return 0, 0, validationError("offset must be a number")
		}
	}
	if limit < 1 || limit > maxD1TableLimit {
		return 0, 0, validationError("limit must be between 1 and %d", maxD1TableLimit)
	}
	if offset < 0 {
		return 0, 0, validationError("offset must be zero or greater")
	}
	return limit, offset, nil
}

func queryD1(ctx context.Context, client *cloudflare.API, accountID, databaseID string, req D1QueryRequest) ([]D1Result, error) {
	req, err := normalizeD1QueryRequest(req)
	if err != nil {
		return nil, err
	}
	results, err := client.QueryD1Database(ctx, cloudflare.AccountIdentifier(accountID), cloudflare.QueryD1DatabaseParams{
		DatabaseID: databaseID,
		SQL:        req.SQL,
		Parameters: req.Params,
	})
	if err != nil {
		return nil, err
	}
	data := make([]D1Result, 0, len(results))
	for _, result := range results {
		data = append(data, D1Result{Success: result.Success, Results: result.Results, Meta: result.Meta})
	}
	return data, nil
}

func d1TableColumns(ctx context.Context, client *cloudflare.API, accountID, databaseID, tableName string) ([]D1Column, error) {
	results, err := queryD1(ctx, client, accountID, databaseID, D1QueryRequest{
		SQL: fmt.Sprintf("PRAGMA table_info(%s)", quoteSQLIdentifier(tableName)),
	})
	if err != nil {
		return nil, err
	}
	columns := []D1Column{}
	if len(results) == 0 {
		return columns, nil
	}
	for _, row := range results[0].Results {
		name := stringFromAny(row["name"])
		if name == "" {
			continue
		}
		columns = append(columns, D1Column{
			Name:       name,
			Type:       stringFromAny(row["type"]),
			NotNull:    intFromAny(row["notnull"]) != 0,
			PrimaryKey: intFromAny(row["pk"]) != 0,
		})
	}
	return columns, nil
}

func d1UpdateRowStatement(tableName string, columns []D1Column, req D1RowMutationRequest) (string, []string, error) {
	rowID := strings.TrimSpace(req.RowID)
	if rowID == "" {
		return "", nil, validationError("rowid is required")
	}
	if len(req.Changes) == 0 {
		return "", nil, validationError("at least one changed column is required")
	}
	if len(req.Changes) > maxD1Parameters-1 {
		return "", nil, validationError("too many changed columns")
	}
	allowed := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		allowed[column.Name] = struct{}{}
	}
	keys := make([]string, 0, len(req.Changes))
	changes := make(map[string]string, len(req.Changes))
	for column, value := range req.Changes {
		column = strings.TrimSpace(column)
		if column == "" {
			return "", nil, validationError("changed column name is required")
		}
		if len(column) > maxD1IdentifierLen {
			return "", nil, validationError("column name is too long")
		}
		if _, ok := allowed[column]; !ok {
			return "", nil, validationError("column %q is not part of table %q", column, tableName)
		}
		if len(value) > maxD1CellValueBytes {
			return "", nil, validationError("column %q value is too large", column)
		}
		if _, exists := changes[column]; exists {
			return "", nil, validationError("column %q is duplicated", column)
		}
		changes[column] = value
		keys = append(keys, column)
	}
	sort.Strings(keys)
	assignments := make([]string, 0, len(keys))
	params := make([]string, 0, len(keys)+1)
	for _, column := range keys {
		assignments = append(assignments, fmt.Sprintf("%s = ?", quoteSQLIdentifier(column)))
		params = append(params, changes[column])
	}
	params = append(params, rowID)
	return fmt.Sprintf("UPDATE %s SET %s WHERE rowid = ?", quoteSQLIdentifier(tableName), strings.Join(assignments, ", ")), params, nil
}

func quoteSQLIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func d1RowIDKey(columns []D1Column) string {
	used := map[string]struct{}{}
	for _, column := range columns {
		used[column.Name] = struct{}{}
	}
	key := d1RowIDDefaultKey
	for {
		if _, ok := used[key]; !ok {
			return key
		}
		key += "_"
	}
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func mapSnippetRule(rule cloudflare.SnippetRule) SnippetRule {
	enabled := true
	if rule.Enabled != nil {
		enabled = *rule.Enabled
	}
	return SnippetRule{
		ID:          rule.ID,
		SnippetName: rule.SnippetName,
		Expression:  rule.Expression,
		Description: rule.Description,
		Enabled:     enabled,
	}
}

func mapSnippetRules(rules []cloudflare.SnippetRule) []SnippetRule {
	out := make([]SnippetRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, mapSnippetRule(rule))
	}
	return out
}

func mapSnippetContent(name, contentType, contentDisposition string, raw []byte) SnippetContent {
	if content, ok := mapMultipartSnippetContent(name, contentType, raw); ok {
		return content
	}
	mainFile := snippetFilenameFromDisposition(contentDisposition)
	if mainFile == "" {
		mainFile = "snippet.js"
	}
	return snippetContentFromBytes(name, mainFile, raw)
}

func mapMultipartSnippetContent(name, contentType string, raw []byte) (SnippetContent, bool) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") || strings.TrimSpace(params["boundary"]) == "" {
		return SnippetContent{}, false
	}
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	mainFile := ""
	files := make(map[string][]byte)
	firstFile := ""
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return SnippetContent{}, false
		}
		data, _ := io.ReadAll(io.LimitReader(part, maxSnippetCodeBytes+1))
		switch part.FormName() {
		case "metadata":
			var metadata struct {
				MainModule string `json:"main_module"`
			}
			if err := json.Unmarshal(data, &metadata); err == nil {
				mainFile = strings.TrimSpace(metadata.MainModule)
			}
		default:
			filename := strings.TrimSpace(part.FileName())
			if filename != "" {
				if firstFile == "" {
					firstFile = filename
				}
				files[filename] = data
			}
		}
	}
	if mainFile == "" {
		mainFile = firstFile
	}
	if mainFile == "" {
		mainFile = "snippet.js"
	}
	data, ok := files[mainFile]
	if !ok && firstFile != "" {
		data = files[firstFile]
		mainFile = firstFile
		ok = true
	}
	if !ok {
		return SnippetContent{Name: name, MainFile: mainFile, Encoding: "utf-8"}, true
	}
	return snippetContentFromBytes(name, mainFile, data), true
}

func snippetFilenameFromDisposition(disposition string) string {
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params["filename"])
}

func snippetContentFromBytes(name, mainFile string, raw []byte) SnippetContent {
	bytesLen := len(raw)
	truncated := bytesLen > maxSnippetCodeBytes
	if truncated {
		raw = raw[:maxSnippetCodeBytes]
	}
	if !utf8.Valid(raw) {
		return SnippetContent{
			Name:      name,
			MainFile:  mainFile,
			Encoding:  "binary",
			Bytes:     bytesLen,
			Truncated: truncated,
		}
	}
	return SnippetContent{
		Name:      name,
		MainFile:  mainFile,
		Value:     string(raw),
		Encoding:  "utf-8",
		Bytes:     bytesLen,
		Truncated: truncated,
	}
}

func getWAFRuleset(ctx context.Context, client *cloudflare.API, zoneID string, phase cloudflare.RulesetPhase) (cloudflare.Ruleset, bool, error) {
	ruleset, err := client.GetEntrypointRuleset(ctx, cloudflare.ZoneIdentifier(zoneID), string(phase))
	if err != nil {
		var notFound cloudflare.NotFoundError
		if errors.As(err, &notFound) || strings.Contains(strings.ToLower(err.Error()), "could not find entrypoint") {
			return cloudflare.Ruleset{}, false, nil
		}
		return cloudflare.Ruleset{}, false, err
	}
	return ruleset, true, nil
}

func mapWAFRuleset(ruleset cloudflare.Ruleset) WAFRuleset {
	return mapWAFRulesetFiltered(ruleset, false)
}

func mapWAFRulesetFiltered(ruleset cloudflare.Ruleset, skipOnly bool) WAFRuleset {
	if !skipOnly {
		return mapWAFRulesetByAction(ruleset, "")
	}
	return mapWAFRulesetByAction(ruleset, "skip")
}

func mapWAFRulesetByAction(ruleset cloudflare.Ruleset, action string) WAFRuleset {
	rules := make([]WAFRule, 0, len(ruleset.Rules))
	for _, rule := range ruleset.Rules {
		if action != "" && rule.Action != action {
			continue
		}
		rules = append(rules, WAFRule{
			ID:               rule.ID,
			Ref:              rule.Ref,
			Version:          stringValue(rule.Version),
			Action:           rule.Action,
			ActionParameters: mapWAFActionParameters(rule.ActionParameters),
			Expression:       rule.Expression,
			Description:      rule.Description,
			Enabled:          rule.Enabled,
			ScoreThreshold:   rule.ScoreThreshold,
			RateLimit:        mapWAFRateLimit(rule.RateLimit),
			Logging:          mapWAFLogging(rule.Logging),
			CredentialCheck:  mapWAFCredentialCheck(rule.ExposedCredentialCheck),
			LastUpdated:      rule.LastUpdated,
		})
	}
	return WAFRuleset{
		ID:          ruleset.ID,
		Name:        ruleset.Name,
		Phase:       ruleset.Phase,
		LastUpdated: ruleset.LastUpdated,
		Rules:       rules,
	}
}

func mapWAFActionParameters(params *cloudflare.RulesetRuleActionParameters) *WAFRuleActionParameters {
	if params == nil {
		return nil
	}
	raw := structJSONMap(params)
	out := &WAFRuleActionParameters{
		ID:        strings.TrimSpace(params.ID),
		Ruleset:   strings.TrimSpace(params.Ruleset),
		Rulesets:  append([]string{}, params.Rulesets...),
		Rules:     copyStringSliceMap(params.Rules),
		Products:  append([]string{}, params.Products...),
		Phases:    append([]string{}, params.Phases...),
		Overrides: mapWAFManagedOverrides(params.Overrides),
		Version:   copyStringPtr(params.Version),
		Raw:       raw,
	}
	if out.ID == "" && out.Ruleset == "" && len(out.Rulesets) == 0 && len(out.Rules) == 0 && len(out.Products) == 0 && len(out.Phases) == 0 && out.Overrides == nil && out.Version == nil && len(out.Raw) == 0 {
		return nil
	}
	return out
}

func mapWAFManagedOverrides(overrides *cloudflare.RulesetRuleActionParametersOverrides) *WAFManagedOverrides {
	if overrides == nil {
		return nil
	}
	out := &WAFManagedOverrides{
		Enabled:          copyBoolPtr(overrides.Enabled),
		Action:           strings.TrimSpace(overrides.Action),
		SensitivityLevel: strings.TrimSpace(overrides.SensitivityLevel),
		Categories:       make([]WAFManagedCategoryOverride, 0, len(overrides.Categories)),
		Rules:            make([]WAFManagedRuleOverride, 0, len(overrides.Rules)),
	}
	for _, category := range overrides.Categories {
		out.Categories = append(out.Categories, WAFManagedCategoryOverride{
			Category: strings.TrimSpace(category.Category),
			Action:   strings.TrimSpace(category.Action),
			Enabled:  copyBoolPtr(category.Enabled),
		})
	}
	for _, rule := range overrides.Rules {
		out.Rules = append(out.Rules, WAFManagedRuleOverride{
			ID:               strings.TrimSpace(rule.ID),
			Action:           strings.TrimSpace(rule.Action),
			Enabled:          copyBoolPtr(rule.Enabled),
			ScoreThreshold:   rule.ScoreThreshold,
			SensitivityLevel: strings.TrimSpace(rule.SensitivityLevel),
		})
	}
	if out.Enabled == nil && out.Action == "" && out.SensitivityLevel == "" && len(out.Categories) == 0 && len(out.Rules) == 0 {
		return nil
	}
	return out
}

func mapWAFRateLimit(rateLimit *cloudflare.RulesetRuleRateLimit) *WAFRuleRateLimit {
	if rateLimit == nil {
		return nil
	}
	out := &WAFRuleRateLimit{
		Characteristics:         append([]string{}, rateLimit.Characteristics...),
		RequestsPerPeriod:       rateLimit.RequestsPerPeriod,
		ScorePerPeriod:          rateLimit.ScorePerPeriod,
		ScoreResponseHeaderName: rateLimit.ScoreResponseHeaderName,
		Period:                  rateLimit.Period,
		MitigationTimeout:       rateLimit.MitigationTimeout,
		CountingExpression:      rateLimit.CountingExpression,
		RequestsToOrigin:        rateLimit.RequestsToOrigin,
	}
	if len(out.Characteristics) == 0 && out.RequestsPerPeriod == 0 && out.ScorePerPeriod == 0 && out.ScoreResponseHeaderName == "" && out.Period == 0 && out.MitigationTimeout == 0 && out.CountingExpression == "" && !out.RequestsToOrigin {
		return nil
	}
	return out
}

func mapWAFLogging(logging *cloudflare.RulesetRuleLogging) *WAFRuleLogging {
	if logging == nil {
		return nil
	}
	return &WAFRuleLogging{Enabled: copyBoolPtr(logging.Enabled)}
}

func mapWAFCredentialCheck(check *cloudflare.RulesetRuleExposedCredentialCheck) *WAFRuleCredentialCheck {
	if check == nil {
		return nil
	}
	out := &WAFRuleCredentialCheck{
		UsernameExpression: check.UsernameExpression,
		PasswordExpression: check.PasswordExpression,
	}
	if out.UsernameExpression == "" && out.PasswordExpression == "" {
		return nil
	}
	return out
}

func structJSONMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func copyStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func copyBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func copyStringSliceMap(value map[string][]string) map[string][]string {
	if len(value) == 0 {
		return nil
	}
	out := make(map[string][]string, len(value))
	for key, values := range value {
		out[key] = append([]string{}, values...)
	}
	return out
}

func mapWorkerMetadata(script cloudflare.WorkerMetaData) WorkerScript {
	return WorkerScript{
		ID:               script.ID,
		Size:             script.Size,
		CreatedOn:        zeroTimePtr(script.CreatedOn),
		ModifiedOn:       zeroTimePtr(script.ModifiedOn),
		Logpush:          script.Logpush,
		LastDeployedFrom: stringValue(script.LastDeployedFrom),
		DeploymentID:     stringValue(script.DeploymentId),
	}
}

func mapWorkerSettings(settings cloudflare.WorkerScriptSettingsResponse) WorkerScriptSettings {
	out := WorkerScriptSettings{
		ETag:          settings.ETAG,
		Logpush:       settings.Logpush,
		TailConsumers: mapWorkerTailConsumers(settings.TailConsumers),
	}
	if settings.Placement != nil {
		out.PlacementMode = string(settings.Placement.Mode)
		out.PlacementStatus = string(settings.Placement.Status)
	} else if settings.PlacementMode != nil {
		out.PlacementMode = string(*settings.PlacementMode)
	}
	return out
}

func mapWorkerTailConsumers(consumers *[]cloudflare.WorkersTailConsumer) []WorkerTailConsumer {
	if consumers == nil {
		return nil
	}
	out := make([]WorkerTailConsumer, 0, len(*consumers))
	for _, consumer := range *consumers {
		out = append(out, WorkerTailConsumer{
			Service:     consumer.Service,
			Environment: stringValue(consumer.Environment),
			Namespace:   stringValue(consumer.Namespace),
		})
	}
	return out
}

func mapWorkerContent(value string) WorkerScriptContent {
	bytesLen := len(value)
	if !utf8.ValidString(value) {
		return WorkerScriptContent{Encoding: "binary", Bytes: bytesLen}
	}
	truncated := bytesLen > maxWorkerScriptContentBytes
	if truncated {
		value = value[:maxWorkerScriptContentBytes]
		for !utf8.ValidString(value) && len(value) > 0 {
			value = value[:len(value)-1]
		}
	}
	return WorkerScriptContent{
		Value:     value,
		Encoding:  "utf-8",
		Bytes:     bytesLen,
		Truncated: truncated,
	}
}

func mapAnalyticsPoints(items []cloudflare.ZoneAnalytics) []AnalyticsPoint {
	out := make([]AnalyticsPoint, 0, len(items))
	for _, item := range items {
		out = append(out, mapAnalyticsPoint(item))
	}
	return out
}

func mapAnalyticsPoint(item cloudflare.ZoneAnalytics) AnalyticsPoint {
	return AnalyticsPoint{
		Since:          item.Since,
		Until:          item.Until,
		Requests:       item.Requests.All,
		CachedRequests: item.Requests.Cached,
		Uncached:       item.Requests.Uncached,
		Bytes:          item.Bandwidth.All,
		CachedBytes:    item.Bandwidth.Cached,
		Threats:        item.Threats.All,
		Pageviews:      item.Pageviews.All,
		Uniques:        item.Uniques.All,
	}
}

type accountUsageGraphQLVariables struct {
	AccountTag  string `json:"accountTag"`
	PeriodStart string `json:"periodStart"`
	TodayStart  string `json:"todayStart"`
	Now         string `json:"now"`
}

type usageDateGraphQLVariables struct {
	AccountTag  string `json:"accountTag"`
	PeriodStart string `json:"periodStart"`
	TodayStart  string `json:"todayStart"`
	Until       string `json:"until"`
}

type accountWorkersErrorsGraphQLVariables struct {
	AccountTag string `json:"accountTag"`
	Since      string `json:"since"`
	Until      string `json:"until"`
}

type accountUsageGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Period    []workerMetricsGroup `json:"period"`
			Today     []workerMetricsGroup `json:"today"`
			R2Ops     []r2OpsGroup         `json:"r2Ops"`
			R2Storage []r2StorageGroup     `json:"r2Storage"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type accountWorkersErrorsGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Window []workerMetricsGroup `json:"window"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type accountUsageCPUGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Period []workerMetricsGroup `json:"period"`
			Today  []workerMetricsGroup `json:"today"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type d1UsageGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Period []d1UsageGroup `json:"period"`
			Today  []d1UsageGroup `json:"today"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type kvUsageGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Period []kvOpsGroup `json:"period"`
			Today  []kvOpsGroup `json:"today"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type kvStorageGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Storage []kvStorageGroup `json:"storage"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type r2OpsGroup struct {
	Dimensions struct {
		ActionType string `json:"actionType"`
	} `json:"dimensions"`
	Sum *struct {
		Requests int `json:"requests"`
	} `json:"sum"`
}

type r2StorageGroup struct {
	Dimensions struct {
		BucketName string `json:"bucketName"`
	} `json:"dimensions"`
	Max *struct {
		PayloadSize  int `json:"payloadSize"`
		MetadataSize int `json:"metadataSize"`
		ObjectCount  int `json:"objectCount"`
	} `json:"max"`
}

type d1UsageGroup struct {
	Sum *struct {
		RowsRead     int `json:"rowsRead"`
		RowsWritten  int `json:"rowsWritten"`
		ReadQueries  int `json:"readQueries"`
		WriteQueries int `json:"writeQueries"`
	} `json:"sum"`
}

type kvOpsGroup struct {
	Dimensions struct {
		ActionType string `json:"actionType"`
	} `json:"dimensions"`
	Sum *struct {
		Requests int `json:"requests"`
	} `json:"sum"`
}

type kvStorageGroup struct {
	Dimensions struct {
		NamespaceID string `json:"namespaceId"`
	} `json:"dimensions"`
	Max *struct {
		ByteCount int `json:"byteCount"`
		KeyCount  int `json:"keyCount"`
	} `json:"max"`
}

var r2ClassAOperations = map[string]struct{}{
	"ListBuckets":                     {},
	"PutBucket":                       {},
	"ListObjects":                     {},
	"PutObject":                       {},
	"CopyObject":                      {},
	"CompleteMultipartUpload":         {},
	"CreateMultipartUpload":           {},
	"UploadPart":                      {},
	"UploadPartCopy":                  {},
	"ListMultipartUploads":            {},
	"ListParts":                       {},
	"PutBucketEncryption":             {},
	"PutBucketCors":                   {},
	"PutBucketLifecycleConfiguration": {},
	"LifecycleStorageTierTransition":  {},
}

var r2ClassBOperations = map[string]struct{}{
	"HeadBucket":                      {},
	"HeadObject":                      {},
	"GetObject":                       {},
	"UsageSummary":                    {},
	"GetBucketEncryption":             {},
	"GetBucketLocation":               {},
	"GetBucketCors":                   {},
	"GetBucketLifecycleConfiguration": {},
}

func usageWindows(now time.Time) (periodStart, todayStart, normalizedNow time.Time) {
	normalizedNow = now.UTC()
	periodStart = time.Date(normalizedNow.Year(), normalizedNow.Month(), 1, 0, 0, 0, 0, time.UTC)
	todayStart = time.Date(normalizedNow.Year(), normalizedNow.Month(), normalizedNow.Day(), 0, 0, 0, 0, time.UTC)
	return periodStart, todayStart, normalizedNow
}

func deriveAccountBillingInfo(subscriptions []AccountSubscription) AccountBillingInfo {
	info := AccountBillingInfo{
		Available:     true,
		Subscriptions: make([]AccountSubscriptionSummary, 0, len(subscriptions)),
	}
	active := make([]AccountSubscription, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		summary := summarizeAccountSubscription(subscription)
		info.Subscriptions = append(info.Subscriptions, summary)
		if summary.Active {
			active = append(active, subscription)
		}
	}

	var workersSub, r2Sub *AccountSubscription
	for i := range active {
		subscription := &active[i]
		if workersSub == nil && subscriptionMatchesRatePlan(*subscription, "workers") {
			workersSub = subscription
			info.WorkersPaid = true
		}
		if r2Sub == nil && subscriptionMatchesRatePlan(*subscription, "r2") {
			r2Sub = subscription
			info.R2Paid = true
		}
	}

	var periodSource *AccountSubscription
	switch {
	case workersSub != nil:
		periodSource = workersSub
	case r2Sub != nil:
		periodSource = r2Sub
	case len(active) > 0:
		periodSource = &active[0]
	}
	if periodSource != nil {
		info.PeriodStart = parseOptionalTime(periodSource.CurrentPeriodStart)
		info.PeriodEnd = parseOptionalTime(periodSource.CurrentPeriodEnd)
	}
	return info
}

func summarizeAccountSubscription(subscription AccountSubscription) AccountSubscriptionSummary {
	return AccountSubscriptionSummary{
		ID:                 strings.TrimSpace(subscription.ID),
		State:              strings.TrimSpace(subscription.State),
		Frequency:          strings.TrimSpace(subscription.Frequency),
		RatePlanID:         strings.TrimSpace(subscription.RatePlan.ID),
		RatePlanName:       strings.TrimSpace(subscription.RatePlan.PublicName),
		Active:             accountSubscriptionActive(subscription),
		CurrentPeriodStart: parseOptionalTime(subscription.CurrentPeriodStart),
		CurrentPeriodEnd:   parseOptionalTime(subscription.CurrentPeriodEnd),
	}
}

func accountSubscriptionActive(subscription AccountSubscription) bool {
	switch strings.ToLower(strings.TrimSpace(subscription.State)) {
	case "cancelled", "canceled", "expired", "failed":
		return false
	default:
		return true
	}
}

func subscriptionMatchesRatePlan(subscription AccountSubscription, keyword string) bool {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return false
	}
	id := strings.ToLower(subscription.RatePlan.ID)
	name := strings.ToLower(subscription.RatePlan.PublicName)
	return strings.Contains(id, keyword) || strings.Contains(name, keyword)
}

func parseOptionalTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func mapAccountUsage(data accountUsageGraphQLData) (WorkersUsageMetrics, R2UsageMetrics) {
	if len(data.Viewer.Accounts) == 0 {
		return WorkersUsageMetrics{}, R2UsageMetrics{}
	}
	account := data.Viewer.Accounts[0]
	workers := WorkersUsageMetrics{}
	for _, group := range account.Period {
		if group.Sum == nil {
			continue
		}
		workers.RequestsPeriod += group.Sum.Requests
		workers.ErrorsPeriod += group.Sum.Errors
		workers.Subrequests += group.Sum.Subrequests
	}
	for _, group := range account.Today {
		if group.Sum == nil {
			continue
		}
		workers.RequestsToday += group.Sum.Requests
	}
	if len(account.Period) > 0 && account.Period[0].Quantiles != nil {
		workers.CPUTimeP50Us = account.Period[0].Quantiles.CPUTimeP50
		workers.CPUTimeP99Us = account.Period[0].Quantiles.CPUTimeP99
	}

	r2 := R2UsageMetrics{}
	for _, group := range account.R2Ops {
		if group.Sum == nil {
			continue
		}
		action := strings.TrimSpace(group.Dimensions.ActionType)
		if _, ok := r2ClassAOperations[action]; ok {
			r2.ClassAOperations += group.Sum.Requests
		} else if _, ok := r2ClassBOperations[action]; ok {
			r2.ClassBOperations += group.Sum.Requests
		}
	}
	for _, group := range account.R2Storage {
		if group.Max == nil {
			continue
		}
		r2.StorageBytes += group.Max.PayloadSize + group.Max.MetadataSize
		r2.ObjectCount += group.Max.ObjectCount
	}
	return workers, r2
}

func mapAccountUsageCPU(data accountUsageCPUGraphQLData) (*float64, *float64) {
	if len(data.Viewer.Accounts) == 0 {
		return nil, nil
	}
	return sumCPUTime(data.Viewer.Accounts[0].Period), sumCPUTime(data.Viewer.Accounts[0].Today)
}

func mapAccountWorkersErrorsLastHour(data accountWorkersErrorsGraphQLData) *int {
	if len(data.Viewer.Accounts) == 0 {
		return nil
	}
	var total int
	for _, group := range data.Viewer.Accounts[0].Window {
		if group.Sum == nil {
			continue
		}
		total += group.Sum.Errors
	}
	return &total
}

func sumCPUTime(groups []workerMetricsGroup) *float64 {
	var total float64
	var found bool
	for _, group := range groups {
		if group.Sum == nil || group.Sum.CPUTimeUs == nil {
			continue
		}
		total += *group.Sum.CPUTimeUs
		found = true
	}
	if !found {
		return nil
	}
	return &total
}

func mapD1Usage(data d1UsageGraphQLData) D1UsageMetrics {
	if len(data.Viewer.Accounts) == 0 {
		return D1UsageMetrics{}
	}
	account := data.Viewer.Accounts[0]
	var out D1UsageMetrics
	for _, group := range account.Period {
		if group.Sum == nil {
			continue
		}
		out.RowsReadPeriod += group.Sum.RowsRead
		out.RowsWrittenPeriod += group.Sum.RowsWritten
		out.ReadQueriesPeriod += group.Sum.ReadQueries
		out.WriteQueriesPeriod += group.Sum.WriteQueries
	}
	for _, group := range account.Today {
		if group.Sum == nil {
			continue
		}
		out.RowsReadToday += group.Sum.RowsRead
		out.RowsWrittenToday += group.Sum.RowsWritten
	}
	return out
}

func mapKVUsage(data kvUsageGraphQLData) KVUsageMetrics {
	if len(data.Viewer.Accounts) == 0 {
		return KVUsageMetrics{}
	}
	account := data.Viewer.Accounts[0]
	var out KVUsageMetrics
	add := func(groups []kvOpsGroup, today bool) {
		for _, group := range groups {
			if group.Sum == nil {
				continue
			}
			switch group.Dimensions.ActionType {
			case "read":
				if today {
					out.ReadsToday += group.Sum.Requests
				} else {
					out.ReadsPeriod += group.Sum.Requests
				}
			case "write":
				if today {
					out.WritesToday += group.Sum.Requests
				} else {
					out.WritesPeriod += group.Sum.Requests
				}
			}
		}
	}
	add(account.Period, false)
	add(account.Today, true)
	return out
}

func mapKVStorage(data kvStorageGraphQLData) (storageBytes, keyCount int) {
	if len(data.Viewer.Accounts) == 0 {
		return 0, 0
	}
	for _, group := range data.Viewer.Accounts[0].Storage {
		if group.Max == nil {
			continue
		}
		storageBytes += group.Max.ByteCount
		keyCount += group.Max.KeyCount
	}
	return storageBytes, keyCount
}

func r2StandardUsage(metrics R2AccountMetrics) (storageBytes, objectCount int, ok bool) {
	if metrics.Standard == nil {
		return 0, 0, false
	}
	for _, snapshot := range []*R2MetricsSnapshot{metrics.Standard.Published, metrics.Standard.Uploaded} {
		if snapshot == nil {
			continue
		}
		storageBytes += snapshot.TotalBytes
		objectCount += snapshot.Objects
	}
	return storageBytes, objectCount, true
}

func (m *R2AccountMetrics) UnmarshalJSON(data []byte) error {
	var raw struct {
		Standard              *R2ClassMetrics `json:"standard"`
		InfrequentAccess      *R2ClassMetrics `json:"infrequentAccess"`
		InfrequentAccessSnake *R2ClassMetrics `json:"infrequent_access"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Standard = raw.Standard
	m.InfrequentAccess = raw.InfrequentAccess
	if m.InfrequentAccess == nil {
		m.InfrequentAccess = raw.InfrequentAccessSnake
	}
	return nil
}

func (m *R2ClassMetrics) UnmarshalJSON(data []byte) error {
	var raw struct {
		Published   *R2MetricsSnapshot `json:"published"`
		Uploaded    *R2MetricsSnapshot `json:"uploaded"`
		Unpublished *R2MetricsSnapshot `json:"unpublished"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Published = raw.Published
	m.Uploaded = raw.Uploaded
	if m.Uploaded == nil {
		m.Uploaded = raw.Unpublished
	}
	return nil
}

func (m *R2MetricsSnapshot) UnmarshalJSON(data []byte) error {
	var raw struct {
		Objects           *int `json:"objects"`
		ObjectCount       *int `json:"objectCount"`
		PayloadSize       *int `json:"payloadSize"`
		PayloadSizeSnake  *int `json:"payload_size"`
		MetadataSize      *int `json:"metadataSize"`
		MetadataSizeSnake *int `json:"metadata_size"`
		TotalBytes        *int `json:"totalBytes"`
		TotalBytesSnake   *int `json:"total_bytes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Objects = firstInt(raw.Objects, raw.ObjectCount)
	m.PayloadSize = firstInt(raw.PayloadSize, raw.PayloadSizeSnake)
	m.MetadataSize = firstInt(raw.MetadataSize, raw.MetadataSizeSnake)
	total := firstInt(raw.TotalBytes, raw.TotalBytesSnake)
	if raw.TotalBytes == nil && raw.TotalBytesSnake == nil {
		total = m.PayloadSize + m.MetadataSize
	}
	m.TotalBytes = total
	return nil
}

func firstInt(values ...*int) int {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}

type workerMetricsGraphQLVariables struct {
	AccountTag string `json:"accountTag"`
	ScriptName string `json:"scriptName"`
	Since      string `json:"since"`
	Until      string `json:"until"`
}

type workerMetricsGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Summary  []workerMetricsGroup `json:"summary"`
			ByStatus []workerStatusGroup  `json:"byStatus"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type workerCPUTimeGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Summary []workerMetricsGroup `json:"summary"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type workerSeriesGraphQLData struct {
	Viewer struct {
		Accounts []struct {
			Series []workerSeriesGroup `json:"series"`
		} `json:"accounts"`
	} `json:"viewer"`
}

type workerMetricsGroup struct {
	Sum       *workerMetricsSum       `json:"sum"`
	Quantiles *workerMetricsQuantiles `json:"quantiles"`
}

type workerMetricsSum struct {
	Requests    int      `json:"requests"`
	Errors      int      `json:"errors"`
	Subrequests int      `json:"subrequests"`
	CPUTimeUs   *float64 `json:"cpuTimeUs"`
}

type workerMetricsQuantiles struct {
	CPUTimeP50 *float64 `json:"cpuTimeP50"`
	CPUTimeP99 *float64 `json:"cpuTimeP99"`
}

type workerStatusGroup struct {
	Dimensions struct {
		Status string `json:"status"`
	} `json:"dimensions"`
	Sum *struct {
		Requests int `json:"requests"`
	} `json:"sum"`
}

type workerSeriesGroup struct {
	Dimensions struct {
		Date         string `json:"date"`
		DatetimeHour string `json:"datetimeHour"`
	} `json:"dimensions"`
	Sum *struct {
		Requests int `json:"requests"`
		Errors   int `json:"errors"`
	} `json:"sum"`
}

func workerMetricsSeriesQuery(daily bool) string {
	dimension := "datetimeHour"
	if daily {
		dimension = "date"
	}
	return fmt.Sprintf(`
query ($accountTag: string!, $scriptName: string!, $since: Time!, $until: Time!) {
  viewer {
    accounts(filter: { accountTag: $accountTag }) {
      series: workersInvocationsAdaptive(
        limit: 1000,
        orderBy: [%[1]s_ASC],
        filter: { scriptName: $scriptName, datetime_geq: $since, datetime_leq: $until }
      ) {
        dimensions { %[1]s }
        sum { requests errors }
      }
    }
  }
}`, dimension)
}

func mapWorkerMetricsSummary(data workerMetricsGraphQLData) (WorkerMetricsSummary, []WorkerStatusMetric) {
	if len(data.Viewer.Accounts) == 0 {
		return WorkerMetricsSummary{}, nil
	}
	account := data.Viewer.Accounts[0]
	var summary WorkerMetricsSummary
	for _, group := range account.Summary {
		if group.Sum == nil {
			continue
		}
		summary.Requests += group.Sum.Requests
		summary.Errors += group.Sum.Errors
		summary.Subrequests += group.Sum.Subrequests
	}
	if len(account.Summary) > 0 && account.Summary[0].Quantiles != nil {
		summary.CPUTimeP50Us = account.Summary[0].Quantiles.CPUTimeP50
		summary.CPUTimeP99Us = account.Summary[0].Quantiles.CPUTimeP99
	}

	statuses := make([]WorkerStatusMetric, 0, len(account.ByStatus))
	for _, group := range account.ByStatus {
		status := strings.TrimSpace(group.Dimensions.Status)
		if status == "" || group.Sum == nil {
			continue
		}
		statuses = append(statuses, WorkerStatusMetric{Status: status, Requests: group.Sum.Requests})
	}
	return summary, statuses
}

func mapWorkerCPUTotal(data workerCPUTimeGraphQLData) *float64 {
	if len(data.Viewer.Accounts) == 0 {
		return nil
	}
	var total float64
	var found bool
	for _, group := range data.Viewer.Accounts[0].Summary {
		if group.Sum == nil || group.Sum.CPUTimeUs == nil {
			continue
		}
		total += *group.Sum.CPUTimeUs
		found = true
	}
	if !found {
		return nil
	}
	return &total
}

func mapWorkerSeries(data workerSeriesGraphQLData, daily bool) []WorkerSeriesPoint {
	if len(data.Viewer.Accounts) == 0 {
		return nil
	}
	points := make([]WorkerSeriesPoint, 0, len(data.Viewer.Accounts[0].Series))
	for _, group := range data.Viewer.Accounts[0].Series {
		var ts time.Time
		var err error
		if daily {
			ts, err = time.Parse("2006-01-02", group.Dimensions.Date)
		} else {
			ts, err = time.Parse(time.RFC3339, group.Dimensions.DatetimeHour)
		}
		if err != nil {
			continue
		}
		point := WorkerSeriesPoint{Time: ts}
		if group.Sum != nil {
			point.Requests = group.Sum.Requests
			point.Errors = group.Sum.Errors
		}
		points = append(points, point)
	}
	return points
}

type statusPageSummaryEnvelope struct {
	Page                  StatusPageInfo        `json:"page"`
	Status                StatusPageOverall     `json:"status"`
	Components            []StatusPageComponent `json:"components"`
	Incidents             []StatusPageIncident  `json:"incidents"`
	ScheduledMaintenances []StatusPageIncident  `json:"scheduled_maintenances"`
}

type statusPageIncidentList struct {
	Incidents []StatusPageIncident `json:"incidents"`
}

func compactStatusIncidents(items []StatusPageIncident) []StatusPageIncident {
	out := make([]StatusPageIncident, 0, len(items))
	for _, item := range items {
		item.IncidentUpdates = nil
		out = append(out, item)
	}
	return out
}

func (s *Service) fetchStatusPage(ctx context.Context, name string, target any) error {
	endpoint := strings.TrimRight(strings.TrimSpace(s.statusEndpoint), "/")
	if endpoint == "" {
		endpoint = defaultStatusPageEndpoint
	}
	reqURL, err := url.Parse(endpoint + "/" + name)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return err
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxStatusPageResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxStatusPageResponseBytes {
		return validationError("cloudflare status response is too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("cloudflare status endpoint returned %d", resp.StatusCode)
	}
	return json.Unmarshal(raw, target)
}

func splitStatusComponents(components []StatusPageComponent) ([]StatusPageComponent, int, []StatusPageRegion) {
	const serviceGroupName = "Cloudflare Sites and Services"
	serviceGroupID := ""
	for _, component := range components {
		if component.Group && component.Name == serviceGroupName {
			serviceGroupID = component.ID
			break
		}
	}
	if serviceGroupID == "" {
		leaves := make([]StatusPageComponent, 0, len(components))
		affected := []StatusPageComponent{}
		for _, component := range components {
			if component.Group {
				continue
			}
			leaves = append(leaves, component)
			if component.Status != "operational" {
				affected = append(affected, component)
			}
		}
		return affected, len(leaves), nil
	}

	products := []StatusPageComponent{}
	affected := []StatusPageComponent{}
	for _, component := range components {
		if component.GroupID != serviceGroupID {
			continue
		}
		products = append(products, component)
		if component.Status != "operational" {
			affected = append(affected, component)
		}
	}

	regions := []StatusPageRegion{}
	for _, group := range components {
		if !group.Group || group.ID == serviceGroupID {
			continue
		}
		total := 0
		impacted := 0
		for _, component := range components {
			if component.GroupID != group.ID {
				continue
			}
			total++
			if component.Status != "operational" {
				impacted++
			}
		}
		if total > 0 {
			regions = append(regions, StatusPageRegion{
				ID:       group.ID,
				Name:     group.Name,
				Total:    total,
				Impacted: impacted,
			})
		}
	}
	return affected, len(products), regions
}

func (s *Service) graphQL(ctx context.Context, accessToken, query string, variables any, target any) error {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return cfoauth.ErrNotLoggedIn
	}
	body, err := json.Marshal(struct {
		Query     string `json:"query"`
		Variables any    `json:"variables"`
	}{
		Query:     query,
		Variables: variables,
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimSpace(s.graphQLEndpoint)
	if endpoint == "" {
		endpoint = defaultGraphQLEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("cloudflare graphql endpoint returned %d", resp.StatusCode)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		message := strings.TrimSpace(envelope.Errors[0].Message)
		if len(message) > 512 {
			message = message[:512]
		}
		if message == "" {
			message = "query failed"
		}
		return fmt.Errorf("cloudflare graphql error: %s", message)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("cloudflare graphql response returned no data")
	}
	return json.Unmarshal(envelope.Data, target)
}

type cfResultInfo struct {
	Count       int    `json:"count"`
	TotalCount  int    `json:"total_count"`
	TotalPages  int    `json:"total_pages"`
	Cursor      string `json:"cursor"`
	IsTruncated bool   `json:"is_truncated"`
}

func cfResultCount(info *cfResultInfo, fallback int) int {
	if info == nil {
		return fallback
	}
	if info.TotalCount > 0 {
		return info.TotalCount
	}
	if info.Count > 0 {
		return info.Count
	}
	return fallback
}

type cfAPIMessage struct {
	Message string `json:"message"`
}

type cfAPIStatusError struct {
	StatusCode int
	Message    string
}

func (e *cfAPIStatusError) Error() string {
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("cloudflare api returned %d", e.StatusCode)
	}
	return fmt.Sprintf("cloudflare api returned %d: %s", e.StatusCode, e.Message)
}

func (s *Service) cfAPI(ctx context.Context, accessToken, method, path string, query url.Values, contentType string, body io.Reader, target any) (*cfResultInfo, error) {
	resp, err := s.cfAPIRaw(ctx, accessToken, method, path, query, contentType, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if strings.TrimSpace(string(raw)) == "" {
		return nil, nil
	}
	var envelope struct {
		Success    bool            `json:"success"`
		Result     json.RawMessage `json:"result"`
		ResultInfo *cfResultInfo   `json:"result_info"`
		Errors     []cfAPIMessage  `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if !envelope.Success {
		return nil, fmt.Errorf("cloudflare api error: %s", firstCFMessage(envelope.Errors))
	}
	if target != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		if err := json.Unmarshal(envelope.Result, target); err != nil {
			return nil, err
		}
	}
	return envelope.ResultInfo, nil
}

func (s *Service) cfAPIRaw(ctx context.Context, accessToken, method, path string, query url.Values, contentType string, body io.Reader) (*http.Response, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return nil, cfoauth.ErrNotLoggedIn
	}
	endpoint := strings.TrimRight(strings.TrimSpace(s.restEndpoint), "/")
	if endpoint == "" {
		endpoint = defaultRESTEndpoint
	}
	reqURL, err := url.Parse(endpoint + path)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		reqURL.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil && strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", strings.TrimSpace(contentType))
	}
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return nil, cfAPIError(raw, resp.StatusCode)
	}
	return resp, nil
}

func cfAPIError(raw []byte, statusCode int) error {
	var envelope struct {
		Errors []cfAPIMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Errors) > 0 {
		return &cfAPIStatusError{StatusCode: statusCode, Message: firstCFMessage(envelope.Errors)}
	}
	return &cfAPIStatusError{StatusCode: statusCode}
}

func accountBillingUnavailableReason(err error) string {
	var statusErr *cfAPIStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "permission_denied"
		case http.StatusNotFound:
			return "not_found"
		default:
			return "request_failed"
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "request_failed"
	}
	return "unavailable"
}

func firstCFMessage(items []cfAPIMessage) string {
	for _, item := range items {
		message := strings.TrimSpace(item.Message)
		if message != "" {
			if len(message) > 512 {
				return message[:512]
			}
			return message
		}
	}
	return "request failed"
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func normalizeR2BucketRequest(req R2BucketRequest) (R2BucketRequest, error) {
	name, err := normalizeR2BucketName(req.Name)
	if err != nil {
		return R2BucketRequest{}, err
	}
	req.Name = name
	req.LocationHint = strings.TrimSpace(req.LocationHint)
	if len(req.LocationHint) > 64 {
		return R2BucketRequest{}, validationError("location_hint is too long")
	}
	for _, r := range req.LocationHint {
		if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return R2BucketRequest{}, validationError("location_hint contains unsupported characters")
		}
	}
	return req, nil
}

func normalizeR2BucketName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) < 3 || len(name) > 63 {
		return "", validationError("bucket name must be 3-63 characters")
	}
	for i, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !valid {
			return "", validationError("bucket name can only contain lowercase letters, numbers, and hyphens")
		}
		if (i == 0 || i == len(name)-1) && r == '-' {
			return "", validationError("bucket name must start and end with a letter or number")
		}
	}
	return name, nil
}

func normalizeZoneSettingValue(settingID string, value any) (any, error) {
	stringValue, ok := value.(string)
	switch settingID {
	case "development_mode", "always_use_https", "automatic_https_rewrites", "brotli", "rocket_loader":
		if !ok {
			return nil, validationError("%s value must be a string", settingID)
		}
		return normalizeEnumZoneSetting(settingID, stringValue, "on", "off")
	case "security_level":
		if !ok {
			return nil, validationError("%s value must be a string", settingID)
		}
		return normalizeEnumZoneSetting(settingID, stringValue, "essentially_off", "low", "medium", "high", "under_attack")
	case "cache_level":
		if !ok {
			return nil, validationError("%s value must be a string", settingID)
		}
		return normalizeEnumZoneSetting(settingID, stringValue, "aggressive", "basic", "simplified")
	case "ssl":
		if !ok {
			return nil, validationError("%s value must be a string", settingID)
		}
		return normalizeEnumZoneSetting(settingID, stringValue, "off", "flexible", "full", "strict", "origin_pull")
	case "browser_cache_ttl":
		return normalizeBrowserCacheTTL(value)
	default:
		return nil, validationError("%s cannot be changed from cfui OAuth mode", settingID)
	}
}

func normalizeEnumZoneSetting(settingID, value string, allowed ...string) (string, error) {
	value = strings.TrimSpace(value)
	for _, allowedValue := range allowed {
		if value == allowedValue {
			return value, nil
		}
	}
	return "", validationError("%s value is invalid", settingID)
}

func normalizeBrowserCacheTTL(value any) (int, error) {
	var ttl int64
	switch v := value.(type) {
	case int:
		ttl = int64(v)
	case int64:
		ttl = v
	case float64:
		ttl = int64(v)
		if v != float64(ttl) {
			return 0, validationError("browser_cache_ttl must be a whole number of seconds")
		}
	case json.Number:
		parsed, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return 0, validationError("browser_cache_ttl must be a whole number of seconds")
		}
		ttl = parsed
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, validationError("browser_cache_ttl must be a whole number of seconds")
		}
		ttl = parsed
	default:
		return 0, validationError("browser_cache_ttl must be a number of seconds")
	}
	if ttl < 0 || ttl > maxBrowserCacheTTLSeconds {
		return 0, validationError("browser_cache_ttl must be between 0 and %d seconds", maxBrowserCacheTTLSeconds)
	}
	return int(ttl), nil
}

func validationError(format string, args ...any) error {
	return ValidationError{Message: fmt.Sprintf(format, args...)}
}

func normalizeTunnelName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", validationError("tunnel name is required")
	}
	if !utf8.ValidString(name) {
		return "", validationError("tunnel name must be valid UTF-8")
	}
	if utf8.RuneCountInString(name) > maxTunnelNameLen {
		return "", validationError("tunnel name is too long")
	}
	return name, nil
}

func generateTunnelSecret() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate tunnel secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(secret), nil
}

func dnsRecordCountFromResult(records []cloudflare.DNSRecord, info *cloudflare.ResultInfo) int {
	if info == nil {
		return len(records)
	}
	if info.Total > 0 {
		return info.Total
	}
	if len(records) == 0 {
		return 0
	}
	if info.Count > 0 {
		return info.Count
	}
	return len(records)
}

func mapZone(zone cloudflare.Zone) Zone {
	return Zone{
		ID:                  zone.ID,
		Name:                zone.Name,
		Status:              zone.Status,
		Type:                zone.Type,
		Paused:              zone.Paused,
		AccountID:           zone.Account.ID,
		Account:             Account{ID: zone.Account.ID, Name: zone.Account.Name, Type: zone.Account.Type},
		Plan:                mapZonePlan(zone.Plan),
		NameServers:         zone.NameServers,
		OriginalNameServers: zone.OriginalNS,
		CreatedOn:           zeroTimePtr(zone.CreatedOn),
		ModifiedOn:          zeroTimePtr(zone.ModifiedOn),
	}
}

func mapZonePlan(plan cloudflare.ZonePlan) ZonePlan {
	return ZonePlan{
		ID:        plan.ID,
		Name:      plan.Name,
		LegacyID:  plan.LegacyID,
		Frequency: plan.Frequency,
		Currency:  plan.Currency,
		Price:     plan.Price,
	}
}

func mapTunnel(tunnel cloudflare.Tunnel) Tunnel {
	connections := make([]TunnelConnection, 0, len(tunnel.Connections))
	for _, connection := range tunnel.Connections {
		connections = append(connections, TunnelConnection{
			ID:                 connection.ID,
			ColoName:           connection.ColoName,
			ClientID:           connection.ClientID,
			ClientVersion:      connection.ClientVersion,
			OpenedAt:           connection.OpenedAt,
			OriginIP:           connection.OriginIP,
			IsPendingReconnect: connection.IsPendingReconnect,
		})
	}
	return Tunnel{
		ID:                    tunnel.ID,
		Name:                  tunnel.Name,
		Status:                tunnel.Status,
		Type:                  tunnel.TunnelType,
		RemoteConfig:          tunnel.RemoteConfig,
		CreatedAt:             tunnel.CreatedAt,
		DeletedAt:             tunnel.DeletedAt,
		ConnectionsActiveAt:   tunnel.ConnsActiveAt,
		ConnectionsInactiveAt: tunnel.ConnInactiveAt,
		ConnectionCount:       len(connections),
		Connections:           connections,
	}
}

func mapR2Bucket(bucket cloudflare.R2Bucket) R2Bucket {
	return R2Bucket{
		Name:         bucket.Name,
		CreationDate: bucket.CreationDate,
		Location:     bucket.Location,
	}
}

func mapD1Database(database cloudflare.D1Database) D1Database {
	return D1Database{
		UUID:      database.UUID,
		Name:      database.Name,
		Version:   database.Version,
		NumTables: database.NumTables,
		FileSize:  database.FileSize,
		CreatedAt: database.CreatedAt,
	}
}

func mapDNSRecord(record cloudflare.DNSRecord) DNSRecord {
	return DNSRecord{
		ID:         record.ID,
		Type:       record.Type,
		Name:       record.Name,
		Content:    record.Content,
		TTL:        record.TTL,
		Proxied:    record.Proxied,
		Proxiable:  record.Proxiable,
		Comment:    record.Comment,
		CreatedOn:  zeroTimePtr(record.CreatedOn),
		ModifiedOn: zeroTimePtr(record.ModifiedOn),
	}
}

func zeroTimePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func isDisplayedZoneSetting(id string) bool {
	switch id {
	case "ssl", "security_level", "development_mode", "cache_level", "browser_cache_ttl", "always_use_https", "automatic_https_rewrites", "brotli", "rocket_loader":
		return true
	default:
		return false
	}
}
