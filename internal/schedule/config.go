package schedule

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type Kind string

const (
	KindDisabled        Kind = "disabled"
	KindHourly          Kind = "hourly"
	KindDaily           Kind = "daily"
	KindSelectedWeekday Kind = "selected-weekdays"
	KindAfterCompletion Kind = "after-completion"
	KindOneTime         Kind = "one-time"
)

type Weekday string

const (
	Monday    Weekday = "monday"
	Tuesday   Weekday = "tuesday"
	Wednesday Weekday = "wednesday"
	Thursday  Weekday = "thursday"
	Friday    Weekday = "friday"
	Saturday  Weekday = "saturday"
	Sunday    Weekday = "sunday"
)

const MaxRunListEntries = 64

type QuietPeriod struct {
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

type Specification struct {
	Kind         Kind         `json:"kind"`
	Timezone     string       `json:"timezone,omitempty"`
	Minute       int          `json:"minute,omitempty"`
	LocalMinute  int          `json:"local_minute,omitempty"`
	Weekdays     []Weekday    `json:"weekdays,omitempty"`
	DelaySeconds int64        `json:"delay_seconds,omitempty"`
	OneTimeDate  string       `json:"one_time_date,omitempty"`
	QuietPeriod  *QuietPeriod `json:"quiet_period,omitempty"`
}

type RunListEntry struct {
	JobID       string `json:"job_id"`
	JobRevision uint64 `json:"job_revision"`
}

type Configuration struct {
	Revision uint64         `json:"revision"`
	Enabled  bool           `json:"enabled"`
	Schedule Specification  `json:"schedule"`
	RunList  []RunListEntry `json:"run_list"`
}

func (configuration Configuration) Validate() error {
	if configuration.Revision == 0 {
		return fmt.Errorf("configuration revision must be positive")
	}
	if err := configuration.Schedule.Validate(); err != nil {
		return err
	}
	if configuration.Enabled && configuration.Schedule.Kind == KindDisabled {
		return fmt.Errorf("disabled schedule choice cannot be enabled")
	}
	if len(configuration.RunList) > MaxRunListEntries {
		return fmt.Errorf("run list exceeds %d entries", MaxRunListEntries)
	}
	seen := make(map[string]struct{}, len(configuration.RunList))
	for _, entry := range configuration.RunList {
		if entry.JobID == "" || len(entry.JobID) > 128 || strings.ContainsRune(entry.JobID, 0) || entry.JobRevision == 0 {
			return fmt.Errorf("run list contains an invalid job reference")
		}
		if _, exists := seen[entry.JobID]; exists {
			return fmt.Errorf("run list contains duplicate job %q", entry.JobID)
		}
		seen[entry.JobID] = struct{}{}
	}
	return nil
}

func (specification Specification) Validate() error {
	if specification.Kind == KindDisabled {
		if specification.Timezone != "" || specification.Minute != 0 || specification.LocalMinute != 0 || len(specification.Weekdays) != 0 || specification.DelaySeconds != 0 || specification.OneTimeDate != "" || specification.QuietPeriod != nil {
			return fmt.Errorf("disabled schedule cannot contain timing fields")
		}
		return nil
	}
	if specification.Timezone == "" || len(specification.Timezone) > 128 || strings.TrimSpace(specification.Timezone) != specification.Timezone {
		return fmt.Errorf("schedule requires a bounded named timezone")
	}
	if _, err := time.LoadLocation(specification.Timezone); err != nil {
		return fmt.Errorf("load schedule timezone: %w", err)
	}
	if specification.QuietPeriod != nil {
		if err := specification.QuietPeriod.Validate(); err != nil {
			return err
		}
	}
	switch specification.Kind {
	case KindHourly:
		if specification.Minute < 0 || specification.Minute > 59 || specification.LocalMinute != 0 || len(specification.Weekdays) != 0 || specification.DelaySeconds != 0 || specification.OneTimeDate != "" {
			return fmt.Errorf("hourly schedule has invalid or conflicting fields")
		}
	case KindDaily:
		if !validMinuteOfDay(specification.LocalMinute) || specification.Minute != 0 || len(specification.Weekdays) != 0 || specification.DelaySeconds != 0 || specification.OneTimeDate != "" {
			return fmt.Errorf("daily schedule has invalid or conflicting fields")
		}
	case KindSelectedWeekday:
		if !validMinuteOfDay(specification.LocalMinute) || specification.Minute != 0 || len(specification.Weekdays) == 0 || specification.DelaySeconds != 0 || specification.OneTimeDate != "" {
			return fmt.Errorf("selected-weekdays schedule has invalid or conflicting fields")
		}
		if err := validateWeekdays(specification.Weekdays); err != nil {
			return err
		}
	case KindAfterCompletion:
		if specification.DelaySeconds <= 0 || specification.DelaySeconds > int64((365*24*time.Hour)/time.Second) || specification.Minute != 0 || specification.LocalMinute != 0 || len(specification.Weekdays) != 0 || specification.OneTimeDate != "" {
			return fmt.Errorf("after-completion schedule has invalid or conflicting fields")
		}
	case KindOneTime:
		if !validMinuteOfDay(specification.LocalMinute) || !validDate(specification.OneTimeDate) || specification.Minute != 0 || len(specification.Weekdays) != 0 || specification.DelaySeconds != 0 {
			return fmt.Errorf("one-time schedule has invalid or conflicting fields")
		}
	default:
		return fmt.Errorf("unsupported schedule kind %q", specification.Kind)
	}
	return nil
}

func (period QuietPeriod) Validate() error {
	if !validMinuteOfDay(period.StartMinute) || !validMinuteOfDay(period.EndMinute) || period.StartMinute == period.EndMinute {
		return fmt.Errorf("quiet period requires distinct valid local minutes")
	}
	return nil
}

func validMinuteOfDay(value int) bool { return value >= 0 && value < 24*60 }

func validDate(value string) bool {
	if len(value) != len("2006-01-02") {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validateWeekdays(values []Weekday) error {
	seen := make(map[Weekday]struct{}, len(values))
	valid := []Weekday{Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday}
	for _, value := range values {
		if !slices.Contains(valid, value) {
			return fmt.Errorf("invalid weekday %q", value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate weekday %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func CloneConfiguration(value Configuration) Configuration {
	value.Schedule.Weekdays = append([]Weekday(nil), value.Schedule.Weekdays...)
	if value.Schedule.QuietPeriod != nil {
		quiet := *value.Schedule.QuietPeriod
		value.Schedule.QuietPeriod = &quiet
	}
	value.RunList = append([]RunListEntry(nil), value.RunList...)
	return value
}
