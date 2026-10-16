package schedule

import (
	"testing"
	"time"
)

func TestClassifyBoot(t *testing.T) {
	policy := DefaultBootPolicy()
	intent := ArmedIntent{Enabled: true, Phase: ArmedShutdownRequested, WakeKind: WakeAbsoluteRTC, ExpectedRTC: 10_000, ProgrammedRTCWake: 10_000}
	tests := []struct {
		name        string
		intent      ArmedIntent
		observation Observation
		want        BootClass
	}{
		{name: "expected", intent: intent, observation: Observation{RTCBefore: 10_030, RTCAfter: 10_031}, want: BootExpected},
		{name: "expected tolerance", intent: intent, observation: Observation{RTCBefore: 9_998, RTCAfter: 9_999}, want: BootExpected},
		{name: "early manual", intent: intent, observation: Observation{RTCBefore: 9_000, RTCAfter: 9_001, RTCWake: 10_000}, want: BootManualSafe},
		{name: "relative countdown conflict", intent: intent, observation: Observation{RTCBefore: 9_000, RTCAfter: 9_001, RTCWake: 10_000, WakeMinutes: 4}, want: BootManualSafe},
		{name: "cleared early", intent: intent, observation: Observation{RTCBefore: 9_000, RTCAfter: 9_001}, want: BootAmbiguous},
		{name: "not cleared when due", intent: intent, observation: Observation{RTCBefore: 10_000, RTCAfter: 10_001, RTCWake: 10_000}, want: BootAmbiguous},
		{name: "different wake", intent: intent, observation: Observation{RTCBefore: 9_000, RTCAfter: 9_001, RTCWake: 11_000}, want: BootAmbiguous},
		{name: "late startup", intent: intent, observation: Observation{RTCBefore: 10_301, RTCAfter: 10_302}, want: BootAmbiguous},
		{name: "incoherent read", intent: intent, observation: Observation{RTCBefore: 10_000, RTCAfter: 10_003}, want: BootAmbiguous},
		{name: "relative unsupported", intent: withWakeKind(intent, WakeRelative), observation: Observation{RTCBefore: 10_000, RTCAfter: 10_001}, want: BootAmbiguous},
		{name: "wrong phase", intent: withPhase(intent, ArmedWakeVerified), observation: Observation{RTCBefore: 10_000, RTCAfter: 10_001}, want: BootAmbiguous},
		{name: "inactive", intent: ArmedIntent{}, observation: Observation{}, want: BootInactive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyBoot(test.intent, test.observation, policy)
			if got.Class != test.want || got.Reason == "" {
				t.Fatalf("ClassifyBoot() = %+v, want %s", got, test.want)
			}
		})
	}
}

func TestClassifyBootPolicyBoundaries(t *testing.T) {
	policy := BootPolicy{EarlyTolerance: 2 * time.Second, MaxStartupDelay: 5 * time.Minute, MaxReadSpan: 2 * time.Second}
	intent := ArmedIntent{Enabled: true, Phase: ArmedShutdownRequested, WakeKind: WakeAbsoluteRTC, ExpectedRTC: 1000, ProgrammedRTCWake: 1000}
	if got := ClassifyBoot(intent, Observation{RTCBefore: 1300, RTCAfter: 1300}, policy); got.Class != BootExpected {
		t.Fatalf("last accepted second = %+v", got)
	}
	if got := ClassifyBoot(intent, Observation{RTCBefore: 1301, RTCAfter: 1301}, policy); got.Class != BootAmbiguous {
		t.Fatalf("first late second = %+v", got)
	}
}

func withWakeKind(intent ArmedIntent, kind WakeKind) ArmedIntent {
	intent.WakeKind = kind
	return intent
}

func withPhase(intent ArmedIntent, phase ArmedPhase) ArmedIntent {
	intent.Phase = phase
	return intent
}
