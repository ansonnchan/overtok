package leaderboard

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"time"

	serveraccount "github.com/ansonnchan/tokenholic/server/account"
	serverauth "github.com/ansonnchan/tokenholic/server/auth"
	servergroup "github.com/ansonnchan/tokenholic/server/group"
)

// Period is a timezone-relative leaderboard window.
type Period string

const (
	PeriodToday      Period = "today"
	PeriodWeek       Period = "week"
	PeriodLast30Days Period = "last_30_days"
	PeriodAllTime    Period = "all_time"
)

// Metric is the primary value used to rank group members.
type Metric string

const (
	MetricTotalTokens Metric = "total_tokens"
	MetricOutput      Metric = "output_tokens"
	MetricAPIEstimate Metric = "api_estimate"
)

// Window is the UTC query range for one period. End is exclusive and Start is
// nil only for all-time queries.
type Window struct {
	Start *time.Time `json:"start"`
	End   time.Time  `json:"end"`
}

// SourceMix is one source's total-token contribution and its coverage.
type SourceMix struct {
	Source      string              `json:"source"`
	TotalTokens serveraccount.Value `json:"total_tokens"`
}

// ModelUsage describes the most-used model without hiding resolution quality.
type ModelUsage struct {
	ModelID     string              `json:"model_id"`
	Resolution  string              `json:"resolution"`
	TotalTokens serveraccount.Value `json:"total_tokens"`
}

// Limitations makes incomplete model and price resolution visible per member.
type Limitations struct {
	UnknownModelBuckets           int64 `json:"unknown_model_buckets"`
	InferredModelBuckets          int64 `json:"inferred_model_buckets"`
	APIEstimateUnavailableBuckets int64 `json:"api_estimate_unavailable_buckets"`
}

// Row is one active member's leaderboard result.
type Row struct {
	Rank          *int                `json:"rank"`
	UserID        string              `json:"user_id"`
	DisplayName   string              `json:"display_name"`
	CurrentUser   bool                `json:"current_user"`
	Value         serveraccount.Value `json:"value"`
	LastSyncedAt  *time.Time          `json:"last_synced_at"`
	SourceMix     []SourceMix         `json:"source_mix"`
	MostUsedModel *ModelUsage         `json:"most_used_model"`
	Limitations   Limitations         `json:"limitations"`
}

// Board is a membership-authorized leaderboard snapshot.
type Board struct {
	GroupID     string    `json:"group_id"`
	GroupName   string    `json:"group_name"`
	Timezone    string    `json:"timezone"`
	Period      Period    `json:"period"`
	Metric      Metric    `json:"metric"`
	Window      Window    `json:"window"`
	GeneratedAt time.Time `json:"generated_at"`
	Rows        []Row     `json:"rows"`
}

// Query is validated repository input.
type Query struct {
	GroupID     string
	Period      Period
	Metric      Metric
	GeneratedAt time.Time
}

// Authenticator resolves a tokenholic device credential.
type Authenticator interface {
	Authenticate(context.Context, string) (serverauth.Principal, error)
}

// Repository reads one authorized group ranking snapshot.
type Repository interface {
	Leaderboard(context.Context, serverauth.Principal, Query) (Board, error)
}

// Service owns leaderboard input validation, authentication, and tie ranking.
type Service struct {
	auth       Authenticator
	repository Repository
	now        func() time.Time
}

// NewService constructs the group leaderboard boundary.
func NewService(auth Authenticator, repository Repository) (*Service, error) {
	if auth == nil || repository == nil {
		return nil, errors.New("initialize leaderboards: authenticator and repository are required")
	}
	return &Service{auth: auth, repository: repository, now: time.Now}, nil
}

// Get authenticates one device and returns a ranked group snapshot.
func (s *Service) Get(ctx context.Context, credential, groupID string, period Period, metric Metric) (Board, error) {
	principal, err := s.auth.Authenticate(ctx, credential)
	if err != nil {
		return Board{}, err
	}
	if !validUUID(groupID) {
		return Board{}, servergroup.ErrNotFound
	}
	if !period.Valid() || !metric.Valid() {
		return Board{}, servergroup.ErrInvalidInput
	}
	board, err := s.repository.Leaderboard(ctx, principal, Query{
		GroupID: groupID, Period: period, Metric: metric, GeneratedAt: s.now().UTC(),
	})
	if err != nil {
		return Board{}, err
	}
	for index := range board.Rows {
		board.Rows[index].CurrentUser = board.Rows[index].UserID == principal.UserID
	}
	rankRows(board.Rows)
	return board, nil
}

// Valid reports whether a period is supported by the MVP query contract.
func (p Period) Valid() bool {
	switch p {
	case PeriodToday, PeriodWeek, PeriodLast30Days, PeriodAllTime:
		return true
	default:
		return false
	}
}

// Valid reports whether a metric is supported by the MVP query contract.
func (m Metric) Valid() bool {
	switch m {
	case MetricTotalTokens, MetricOutput, MetricAPIEstimate:
		return true
	default:
		return false
	}
}

// WindowFor computes calendar boundaries in an IANA timezone. Today starts at
// local midnight, weeks start Monday, and 30 days means 30 local dates.
func WindowFor(period Period, generatedAt time.Time, location *time.Location) (Window, error) {
	if !period.Valid() || location == nil {
		return Window{}, servergroup.ErrInvalidInput
	}
	local := generatedAt.In(location)
	end := generatedAt.UTC()
	if period == PeriodAllTime {
		return Window{End: end}, nil
	}
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	switch period {
	case PeriodWeek:
		daysSinceMonday := (int(start.Weekday()) + 6) % 7
		start = start.AddDate(0, 0, -daysSinceMonday)
	case PeriodLast30Days:
		start = start.AddDate(0, 0, -29)
	}
	startUTC := start.UTC()
	return Window{Start: &startUTC, End: end}, nil
}

func rankRows(rows []Row) {
	sort.SliceStable(rows, func(left, right int) bool {
		leftValue, leftKnown := decimal(rows[left].Value)
		rightValue, rightKnown := decimal(rows[right].Value)
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown {
			if comparison := leftValue.Cmp(rightValue); comparison != 0 {
				return comparison > 0
			}
		}
		if rows[left].DisplayName != rows[right].DisplayName {
			return rows[left].DisplayName < rows[right].DisplayName
		}
		return rows[left].UserID < rows[right].UserID
	})
	var previous *big.Int
	for index := range rows {
		value, known := decimal(rows[index].Value)
		if !known {
			rows[index].Rank = nil
			continue
		}
		rank := index + 1
		if previous != nil && value.Cmp(previous) == 0 {
			rank = *rows[index-1].Rank
		}
		rows[index].Rank = &rank
		previous = value
	}
}

func decimal(value serveraccount.Value) (*big.Int, bool) {
	// Partial values remain visible as lower-bound coverage but are not
	// comparable enough to assign a rank.
	if value.Value == nil || value.State != serveraccount.AvailabilityComplete {
		return nil, false
	}
	parsed, ok := new(big.Int).SetString(*value.Value, 10)
	return parsed, ok
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}
