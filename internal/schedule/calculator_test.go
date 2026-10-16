package schedule

import (
	"errors"
	"testing"
	"time"
)

func unixAt(t *testing.T, value string) uint32 {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return uint32(parsed.Unix())
}

func TestPreviewInitialCalendarChoices(t *testing.T) {
	tests := []struct {
		name      string
		schedule  Specification
		reference string
		want      string
	}{
		{name: "hourly skips current label", schedule: Specification{Kind: KindHourly, Timezone: "UTC", Minute: 15}, reference: "2026-10-11T10:15:00Z", want: "2026-10-11 11:15:00"},
		{name: "daily crosses month", schedule: Specification{Kind: KindDaily, Timezone: "UTC", LocalMinute: 9 * 60}, reference: "2026-01-31T10:00:00Z", want: "2026-02-01 09:00:00"},
		{name: "selected weekday", schedule: Specification{Kind: KindSelectedWeekday, Timezone: "UTC", LocalMinute: 8 * 60, Weekdays: []Weekday{Monday}}, reference: "2026-10-11T10:00:00Z", want: "2026-10-12 08:00:00"},
		{name: "one time", schedule: Specification{Kind: KindOneTime, Timezone: "UTC", LocalMinute: 12 * 60, OneTimeDate: "2026-12-31"}, reference: "2026-10-11T10:00:00Z", want: "2026-12-31 12:00:00"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Preview(test.schedule, unixAt(t, test.reference), 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Local != test.want || got[0].UTCOffset != "+00:00" {
				t.Fatalf("Preview() = %+v", got)
			}
		})
	}
}

func TestPreviewLeadAndMissedOccurrencesAreStrictlyFuture(t *testing.T) {
	specification := Specification{Kind: KindHourly, Timezone: "UTC", Minute: 0}
	got, err := Preview(specification, unixAt(t, "2026-10-11T10:59:30Z"), 30*time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Local != "2026-10-11 12:00:00" || got[1].Local != "2026-10-11 13:00:00" {
		t.Fatalf("Preview() = %+v", got)
	}
}

func TestPreviewQuietPeriodFiltersWithoutMovingOccurrences(t *testing.T) {
	specification := Specification{Kind: KindHourly, Timezone: "UTC", Minute: 0, QuietPeriod: &QuietPeriod{StartMinute: 22 * 60, EndMinute: 6 * 60}}
	got, err := Preview(specification, unixAt(t, "2026-10-11T21:00:00Z"), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Local != "2026-10-12 06:00:00" || got[1].Local != "2026-10-12 07:00:00" {
		t.Fatalf("Preview() = %+v", got)
	}
}

func TestAfterCompletionPreservesDelayCadenceThroughQuietPeriod(t *testing.T) {
	specification := Specification{Kind: KindAfterCompletion, Timezone: "UTC", DelaySeconds: 3600, QuietPeriod: &QuietPeriod{StartMinute: 22 * 60, EndMinute: 6 * 60}}
	got, err := Preview(specification, unixAt(t, "2026-10-11T21:30:00Z"), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Local != "2026-10-12 06:30:00" {
		t.Fatalf("Preview() = %+v", got)
	}
}

func TestPreviewDSTGapIsSkippedAndOverlapUsesFirstInstance(t *testing.T) {
	spring := Specification{Kind: KindDaily, Timezone: "Europe/London", LocalMinute: 90}
	got, err := Preview(spring, unixAt(t, "2026-03-27T02:00:00Z"), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Local != "2026-03-28 01:30:00" || got[1].Local != "2026-03-30 01:30:00" {
		t.Fatalf("spring Preview() = %+v", got)
	}

	autumn := Specification{Kind: KindDaily, Timezone: "Europe/London", LocalMinute: 90}
	got, err = Preview(autumn, unixAt(t, "2026-10-24T02:00:00Z"), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Local != "2026-10-25 01:30:00" || got[0].UTCOffset != "+01:00" || got[1].Local != "2026-10-26 01:30:00" {
		t.Fatalf("autumn Preview() = %+v", got)
	}
}

func TestPreviewRejectsQuietOneTimeAndFullyExcludedRecurrence(t *testing.T) {
	quiet := &QuietPeriod{StartMinute: 22 * 60, EndMinute: 6 * 60}
	oneTime := Specification{Kind: KindOneTime, Timezone: "UTC", LocalMinute: 23 * 60, OneTimeDate: "2026-10-12", QuietPeriod: quiet}
	if _, err := Preview(oneTime, unixAt(t, "2026-10-11T10:00:00Z"), 0, 1); !errors.Is(err, ErrNoOccurrence) {
		t.Fatalf("one-time error = %v", err)
	}
	daily := Specification{Kind: KindDaily, Timezone: "UTC", LocalMinute: 23 * 60, QuietPeriod: quiet}
	if _, err := Preview(daily, unixAt(t, "2026-10-11T10:00:00Z"), 0, 1); !errors.Is(err, ErrNoOccurrence) {
		t.Fatalf("excluded daily error = %v", err)
	}
}

func TestPreviewEnforcesBoardHorizonAndFiniteInputs(t *testing.T) {
	specification := Specification{Kind: KindHourly, Timezone: "UTC", Minute: 0}
	if _, err := Preview(specification, ^uint32(0)-10, 10*time.Second, 1); !errors.Is(err, ErrNoOccurrence) {
		t.Fatalf("horizon error = %v", err)
	}
	if _, err := Preview(specification, 1, 500*time.Millisecond, 1); err == nil {
		t.Fatal("accepted fractional-second lead")
	}
	if _, err := Preview(specification, 1, 0, MaxPreviewOccurrences+1); err == nil {
		t.Fatal("accepted excessive preview count")
	}
}

func TestDisabledPreviewHasNoOccurrences(t *testing.T) {
	got, err := Preview(Specification{Kind: KindDisabled}, 1, 0, 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("Preview() = %+v, %v", got, err)
	}
}

func TestBuildReviewBindsConfigurationAndCalculationInputs(t *testing.T) {
	configuration := Configuration{Revision: 4, Schedule: Specification{Kind: KindHourly, Timezone: "UTC", Minute: 15}}
	reference := unixAt(t, "2026-10-11T10:00:00Z")
	first, err := BuildReview(configuration, reference, 30*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildReview(configuration, reference, 30*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Identity) != 64 || first.Identity != second.Identity || len(first.Occurrences) != 5 || first.ConfigurationRevision != 4 {
		t.Fatalf("review = %+v", first)
	}
	changed, err := BuildReview(configuration, reference+1, 30*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Identity == first.Identity {
		t.Fatal("reference-time change did not change review identity")
	}
}

func TestQuietPeriodBoundariesAreStartInclusiveEndExclusive(t *testing.T) {
	quiet := &QuietPeriod{StartMinute: 22 * 60, EndMinute: 6 * 60}
	for _, test := range []struct {
		time string
		want bool
	}{
		{time: "2026-10-11T21:59:00Z", want: false},
		{time: "2026-10-11T22:00:00Z", want: true},
		{time: "2026-10-12T05:59:00Z", want: true},
		{time: "2026-10-12T06:00:00Z", want: false},
	} {
		value := time.Unix(int64(unixAt(t, test.time)), 0).UTC()
		if got := insideQuiet(value, quiet); got != test.want {
			t.Errorf("insideQuiet(%s) = %v, want %v", test.time, got, test.want)
		}
	}
}
