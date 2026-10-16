package schedule

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	MaxPreviewOccurrences   = 32
	maxCalendarSearchDays   = 8 * 366
	maxDelayCandidates      = 100_000
	SimulatedMinimumLead    = 60 * time.Second
	VerifiedWakeMinimumLead = 5 * time.Minute
)

var ErrNoOccurrence = errors.New("no eligible schedule occurrence found within the supported horizon")

type Occurrence struct {
	Unix      uint32 `json:"unix"`
	Local     string `json:"local"`
	UTCOffset string `json:"utc_offset"`
	Timezone  string `json:"timezone"`
}

type Review struct {
	ConfigurationRevision uint64       `json:"configuration_revision"`
	Reference             uint32       `json:"reference"`
	MinimumLeadSeconds    uint32       `json:"minimum_lead_seconds"`
	Occurrences           []Occurrence `json:"occurrences"`
	Identity              string       `json:"identity"`
	RunListJobs           int          `json:"run_list_jobs,omitempty"`
	MaximumRunSeconds     uint64       `json:"maximum_run_seconds,omitempty"`
}

// BuildReview binds a displayed preview to the complete configuration and
// calculation inputs. The identity is not an authorization token; it lets the
// controller detect that a fresh enablement calculation differs from what the
// owner reviewed.
func BuildReview(configuration Configuration, reference uint32, minimumLead time.Duration, count int) (Review, error) {
	if err := configuration.Validate(); err != nil {
		return Review{}, err
	}
	if configuration.Enabled {
		return Review{}, fmt.Errorf("cannot preview an already enabled configuration")
	}
	occurrences, err := Preview(configuration.Schedule, reference, minimumLead, count)
	if err != nil {
		return Review{}, err
	}
	review := Review{
		ConfigurationRevision: configuration.Revision, Reference: reference,
		MinimumLeadSeconds: uint32(minimumLead / time.Second), Occurrences: occurrences,
	}
	encoded, err := json.Marshal(struct {
		Configuration Configuration `json:"configuration"`
		Reference     uint32        `json:"reference"`
		LeadSeconds   int64         `json:"lead_seconds"`
		Occurrences   []Occurrence  `json:"occurrences"`
	}{Configuration: configuration, Reference: reference, LeadSeconds: int64(minimumLead / time.Second), Occurrences: occurrences})
	if err != nil {
		return Review{}, fmt.Errorf("encode occurrence review: %w", err)
	}
	digest := sha256.Sum256(encoded)
	review.Identity = hex.EncodeToString(digest[:])
	return review, nil
}

// Preview calculates occurrences strictly after reference+minimumLead. For an
// after-completion schedule, reference is the displayed example completion
// time. The controller supplies a fresh board time during enablement.
func Preview(specification Specification, reference uint32, minimumLead time.Duration, count int) ([]Occurrence, error) {
	if err := specification.Validate(); err != nil {
		return nil, err
	}
	if count <= 0 || count > MaxPreviewOccurrences {
		return nil, fmt.Errorf("preview count must be between 1 and %d", MaxPreviewOccurrences)
	}
	if minimumLead < 0 || minimumLead%time.Second != 0 {
		return nil, fmt.Errorf("minimum lead must be a non-negative whole-second duration")
	}
	if specification.Kind == KindDisabled {
		return nil, nil
	}
	threshold := uint64(reference) + uint64(minimumLead/time.Second)
	if threshold >= uint64(^uint32(0)) {
		return nil, ErrNoOccurrence
	}
	location, err := time.LoadLocation(specification.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load schedule timezone: %w", err)
	}
	var times []time.Time
	switch specification.Kind {
	case KindAfterCompletion:
		times, err = previewDelay(specification, reference, threshold, location, count)
	case KindOneTime:
		times, err = previewOneTime(specification, threshold, location)
	default:
		times, err = previewCalendar(specification, threshold, location, count)
	}
	if err != nil {
		return nil, err
	}
	occurrences := make([]Occurrence, len(times))
	for index, value := range times {
		local := value.In(location)
		_, offsetSeconds := local.Zone()
		occurrences[index] = Occurrence{
			Unix: uint32(value.Unix()), Local: local.Format("2006-01-02 15:04:05"),
			UTCOffset: formatOffset(offsetSeconds), Timezone: specification.Timezone,
		}
	}
	return occurrences, nil
}

func previewDelay(specification Specification, reference uint32, threshold uint64, location *time.Location, count int) ([]time.Time, error) {
	delay := uint64(specification.DelaySeconds)
	firstMultiplier := (threshold-uint64(reference))/delay + 1
	result := make([]time.Time, 0, count)
	for attempt, multiplier := 0, firstMultiplier; attempt < maxDelayCandidates && len(result) < count; attempt, multiplier = attempt+1, multiplier+1 {
		if multiplier > (uint64(^uint32(0))-uint64(reference))/delay {
			break
		}
		candidate := uint64(reference) + multiplier*delay
		value := time.Unix(int64(candidate), 0)
		if !insideQuiet(value.In(location), specification.QuietPeriod) {
			result = append(result, value)
		}
	}
	if len(result) < count {
		return nil, ErrNoOccurrence
	}
	return result, nil
}

func previewOneTime(specification Specification, threshold uint64, location *time.Location) ([]time.Time, error) {
	date, _ := time.Parse("2006-01-02", specification.OneTimeDate)
	hour, minute := specification.LocalMinute/60, specification.LocalMinute%60
	value, ok := resolveLocal(location, date.Year(), date.Month(), date.Day(), hour, minute)
	if !ok || value.Unix() <= int64(threshold) || value.Unix() < 0 || uint64(value.Unix()) > uint64(^uint32(0)) || insideQuiet(value.In(location), specification.QuietPeriod) {
		return nil, ErrNoOccurrence
	}
	return []time.Time{value}, nil
}

func previewCalendar(specification Specification, threshold uint64, location *time.Location, count int) ([]time.Time, error) {
	thresholdTime := time.Unix(int64(threshold), 0).In(location)
	startDate := civilDate(thresholdTime)
	result := make([]time.Time, 0, count)
	for dayOffset := 0; dayOffset < maxCalendarSearchDays && len(result) < count; dayOffset++ {
		date := startDate.AddDate(0, 0, dayOffset)
		switch specification.Kind {
		case KindHourly:
			for hour := 0; hour < 24 && len(result) < count; hour++ {
				appendCalendarCandidate(&result, specification, threshold, location, date, hour, specification.Minute)
			}
		case KindDaily:
			appendCalendarCandidate(&result, specification, threshold, location, date, specification.LocalMinute/60, specification.LocalMinute%60)
		case KindSelectedWeekday:
			if includesWeekday(specification.Weekdays, date.Weekday()) {
				appendCalendarCandidate(&result, specification, threshold, location, date, specification.LocalMinute/60, specification.LocalMinute%60)
			}
		}
	}
	if len(result) < count {
		return nil, ErrNoOccurrence
	}
	return result, nil
}

func appendCalendarCandidate(result *[]time.Time, specification Specification, threshold uint64, location *time.Location, date time.Time, hour, minute int) {
	value, ok := resolveLocal(location, date.Year(), date.Month(), date.Day(), hour, minute)
	if !ok || value.Unix() <= int64(threshold) || value.Unix() < 0 || uint64(value.Unix()) > uint64(^uint32(0)) || insideQuiet(value.In(location), specification.QuietPeriod) {
		return
	}
	*result = append(*result, value)
}

// resolveLocal rejects nonexistent labels and returns the first chronological
// instance of a repeated label. Sampling nearby zone offsets avoids relying on
// time.Date's implementation-specific choice during an overlap.
func resolveLocal(location *time.Location, year int, month time.Month, day, hour, minute int) (time.Time, bool) {
	naive := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	offsets := make(map[int]struct{})
	for hours := -48; hours <= 48; hours += 6 {
		_, offset := naive.Add(time.Duration(hours) * time.Hour).In(location).Zone()
		offsets[offset] = struct{}{}
	}
	candidates := make([]time.Time, 0, len(offsets))
	for offset := range offsets {
		candidate := naive.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if local.Year() == year && local.Month() == month && local.Day() == day && local.Hour() == hour && local.Minute() == minute && local.Second() == 0 {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return time.Time{}, false
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].Before(candidates[right]) })
	return candidates[0], true
}

func insideQuiet(local time.Time, quiet *QuietPeriod) bool {
	if quiet == nil {
		return false
	}
	minute := local.Hour()*60 + local.Minute()
	if quiet.StartMinute < quiet.EndMinute {
		return minute >= quiet.StartMinute && minute < quiet.EndMinute
	}
	return minute >= quiet.StartMinute || minute < quiet.EndMinute
}

func includesWeekday(values []Weekday, weekday time.Weekday) bool {
	want := map[time.Weekday]Weekday{
		time.Monday: Monday, time.Tuesday: Tuesday, time.Wednesday: Wednesday,
		time.Thursday: Thursday, time.Friday: Friday, time.Saturday: Saturday, time.Sunday: Sunday,
	}[weekday]
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func civilDate(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func formatOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	return fmt.Sprintf("%s%02d:%02d", sign, seconds/3600, seconds%3600/60)
}
