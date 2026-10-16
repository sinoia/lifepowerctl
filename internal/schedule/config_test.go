package schedule

import "testing"

func TestInitialScheduleLanguageValidates(t *testing.T) {
	tests := []Specification{
		{Kind: KindDisabled},
		{Kind: KindHourly, Timezone: "Europe/London", Minute: 59},
		{Kind: KindDaily, Timezone: "UTC", LocalMinute: 0},
		{Kind: KindSelectedWeekday, Timezone: "UTC", LocalMinute: 9 * 60, Weekdays: []Weekday{Monday, Friday}},
		{Kind: KindAfterCompletion, Timezone: "UTC", DelaySeconds: 3600, QuietPeriod: &QuietPeriod{StartMinute: 22 * 60, EndMinute: 6 * 60}},
		{Kind: KindOneTime, Timezone: "UTC", LocalMinute: 12 * 60, OneTimeDate: "2026-10-12"},
	}
	for _, specification := range tests {
		if err := specification.Validate(); err != nil {
			t.Errorf("%s: %v", specification.Kind, err)
		}
	}
}

func TestScheduleRejectsAmbiguousOrConflictingFields(t *testing.T) {
	tests := []Specification{
		{Kind: KindDisabled, Timezone: "UTC"},
		{Kind: KindHourly, Timezone: "UTC", Minute: 60},
		{Kind: KindDaily, Timezone: "Missing/Timezone", LocalMinute: 1},
		{Kind: KindSelectedWeekday, Timezone: "UTC", Weekdays: []Weekday{Monday, Monday}},
		{Kind: KindAfterCompletion, Timezone: "UTC"},
		{Kind: KindOneTime, Timezone: "UTC", OneTimeDate: "2026-02-30"},
		{Kind: KindDaily, Timezone: "UTC", QuietPeriod: &QuietPeriod{StartMinute: 60, EndMinute: 60}},
	}
	for _, specification := range tests {
		if err := specification.Validate(); err == nil {
			t.Errorf("accepted invalid specification: %+v", specification)
		}
	}
}

func TestConfigurationRejectsDuplicateRunListJobs(t *testing.T) {
	configuration := Configuration{Revision: 1, Schedule: Specification{Kind: KindDisabled}, RunList: []RunListEntry{
		{JobID: "job", JobRevision: 1}, {JobID: "job", JobRevision: 1},
	}}
	if err := configuration.Validate(); err == nil {
		t.Fatal("accepted duplicate run-list job")
	}
}

func TestConfiguredScheduleCanBeSavedWhileAutomationIsDisabled(t *testing.T) {
	configuration := Configuration{Revision: 1, Schedule: Specification{Kind: KindHourly, Timezone: "UTC", Minute: 15}}
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	configuration.Enabled = true
	configuration.Schedule = Specification{Kind: KindDisabled}
	if err := configuration.Validate(); err == nil {
		t.Fatal("enabled disabled-choice schedule was accepted")
	}
}
