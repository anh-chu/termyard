package sessionattrs

import "testing"

func TestSetsIncludesScheduleIDs(t *testing.T) {
	s := &Store{attrs: map[string]Attr{
		"host-1/session-a": {
			Background: true,
			ScheduleID: "sched-123",
		},
	}}

	got := s.Sets()
	if len(got.Background) != 1 || got.Background[0] != "host-1/session-a" || len(got.Hidden) != 0 {
		t.Fatalf("sets = %#v", got)
	}
	if got.ScheduleIDs["host-1/session-a"] != "sched-123" {
		t.Fatalf("schedule ids = %#v", got.ScheduleIDs)
	}
}
