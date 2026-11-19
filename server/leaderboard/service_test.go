package leaderboard

import (
	"context"
	"errors"
	"testing"
	"time"

	serveraccount "github.com/ansonnchan/tokenholic/server/account"
	serverauth "github.com/ansonnchan/tokenholic/server/auth"
	servergroup "github.com/ansonnchan/tokenholic/server/group"
)

func TestWindowForCalendarPeriodsAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/Vancouver")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 12, 30, 0, 0, location)
	tests := []struct {
		period Period
		start  time.Time
	}{
		{period: PeriodToday, start: time.Date(2026, 3, 8, 0, 0, 0, 0, location)},
		{period: PeriodWeek, start: time.Date(2026, 3, 2, 0, 0, 0, 0, location)},
		{period: PeriodLast30Days, start: time.Date(2026, 2, 7, 0, 0, 0, 0, location)},
	}
	for _, test := range tests {
		t.Run(string(test.period), func(t *testing.T) {
			window, err := WindowFor(test.period, now, location)
			if err != nil {
				t.Fatal(err)
			}
			if window.Start == nil || !window.Start.Equal(test.start.UTC()) || !window.End.Equal(now.UTC()) {
				t.Fatalf("WindowFor() = %#v, want %s through %s", window, test.start.UTC(), now.UTC())
			}
		})
	}
	allTime, err := WindowFor(PeriodAllTime, now, location)
	if err != nil || allTime.Start != nil || !allTime.End.Equal(now.UTC()) {
		t.Fatalf("all-time window = %#v, %v", allTime, err)
	}
}

func TestRankRowsUsesCompetitionTiesAndLeavesUnavailableUnranked(t *testing.T) {
	ten, seven, five := "10", "7", "5"
	rows := []Row{
		{UserID: "c", DisplayName: "Charlie", Value: serveraccount.Value{State: serveraccount.AvailabilityUnavailable}},
		{UserID: "b", DisplayName: "Beta", Value: serveraccount.Value{Value: &ten, State: serveraccount.AvailabilityComplete}},
		{UserID: "a", DisplayName: "Alpha", Value: serveraccount.Value{Value: &ten, State: serveraccount.AvailabilityComplete}},
		{UserID: "d", DisplayName: "Delta", Value: serveraccount.Value{Value: &seven, State: serveraccount.AvailabilityComplete}},
		{UserID: "e", DisplayName: "Echo", Value: serveraccount.Value{Value: &five, State: serveraccount.AvailabilityPartial}},
	}
	rankRows(rows)
	if rows[0].DisplayName != "Alpha" || rows[1].DisplayName != "Beta" || rows[2].DisplayName != "Delta" || rows[3].DisplayName != "Charlie" || rows[4].DisplayName != "Echo" {
		t.Fatalf("ranked order = %#v", rows)
	}
	if rows[0].Rank == nil || *rows[0].Rank != 1 || rows[1].Rank == nil || *rows[1].Rank != 1 || rows[2].Rank == nil || *rows[2].Rank != 3 || rows[3].Rank != nil || rows[4].Rank != nil {
		t.Fatalf("ranks = %#v", rows)
	}
}

func TestServiceAuthenticatesValidatesAndMarksCurrentUser(t *testing.T) {
	repository := &fakeRepository{board: Board{Rows: []Row{{UserID: testUserID, DisplayName: "current"}}}}
	service, err := NewService(fakeAuthenticator{}, repository)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }
	board, err := service.Get(t.Context(), "credential", testGroupID, PeriodToday, MetricTotalTokens)
	if err != nil {
		t.Fatal(err)
	}
	if repository.query.GroupID != testGroupID || repository.query.GeneratedAt.IsZero() {
		t.Fatalf("query = %#v", repository.query)
	}
	if !board.Rows[0].CurrentUser {
		t.Fatal("current user was not marked")
	}
	if _, err := service.Get(t.Context(), "credential", "bad", PeriodToday, MetricTotalTokens); !errors.Is(err, servergroup.ErrNotFound) {
		t.Fatalf("bad group error = %v", err)
	}
	if _, err := service.Get(t.Context(), "credential", testGroupID, "future", MetricTotalTokens); !errors.Is(err, servergroup.ErrInvalidInput) {
		t.Fatalf("bad period error = %v", err)
	}
	unauthorized, err := NewService(fakeAuthenticator{err: serverauth.ErrUnauthorized}, repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unauthorized.Get(t.Context(), "bad", "bad", "future", "future"); !errors.Is(err, serverauth.ErrUnauthorized) {
		t.Fatalf("unauthorized validation error = %v", err)
	}
}

const (
	testGroupID = "50000000-0000-4000-8000-000000000001"
	testUserID  = "50000000-0000-4000-8000-000000000002"
)

type fakeAuthenticator struct {
	err error
}

func (f fakeAuthenticator) Authenticate(context.Context, string) (serverauth.Principal, error) {
	return serverauth.Principal{UserID: testUserID}, f.err
}

type fakeRepository struct {
	query Query
	board Board
	err   error
}

func (f *fakeRepository) Leaderboard(_ context.Context, _ serverauth.Principal, query Query) (Board, error) {
	f.query = query
	return f.board, f.err
}
