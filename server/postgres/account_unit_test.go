package postgres

import (
	"testing"

	serveraccount "github.com/ansonnchan/overtok/server/account"
)

func TestAggregateCoverageStates(t *testing.T) {
	value := "7"
	tests := []struct {
		name  string
		scan  aggregateScan
		total int64
		state serveraccount.Availability
	}{
		{name: "empty", total: 0, state: serveraccount.AvailabilityEmpty},
		{name: "unavailable", total: 2, state: serveraccount.AvailabilityUnavailable},
		{name: "missing bucket", scan: aggregateScan{value: &value, known: 1, complete: true}, total: 2, state: serveraccount.AvailabilityPartial},
		{name: "reported partial", scan: aggregateScan{value: &value, known: 2, complete: false}, total: 2, state: serveraccount.AvailabilityPartial},
		{name: "complete", scan: aggregateScan{value: &value, known: 2, complete: true}, total: 2, state: serveraccount.AvailabilityComplete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.scan.coverage(test.total)
			if result.State != test.state {
				t.Fatalf("coverage() = %#v, want %s", result, test.state)
			}
		})
	}
}
