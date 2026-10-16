// Package schedule contains deterministic duty-cycle state transitions.
package schedule

import "time"

type WakeKind string

const (
	WakeAbsoluteRTC WakeKind = "absolute-rtc"
	WakeRelative    WakeKind = "relative"
)

type ArmedPhase string

const (
	ArmedWakeVerified      ArmedPhase = "wake-verified"
	ArmedShutdownRequested ArmedPhase = "shutdown-requested"
	ArmedExpectedObserved  ArmedPhase = "expected-boot-observed"
)

type ArmedIntent struct {
	Enabled            bool
	Phase              ArmedPhase
	WakeKind           WakeKind
	ExpectedRTC        uint32
	ProgrammedRTCWake  uint32
	ProgrammedWakeMins uint16
}

// Observation brackets the wake-register reads with RTC reads so a delayed or
// incoherent sample can be rejected.
type Observation struct {
	RTCBefore   uint32
	RTCAfter    uint32
	RTCWake     uint32
	WakeMinutes uint16
}

type BootClass string

const (
	BootInactive   BootClass = "inactive"
	BootExpected   BootClass = "expected"
	BootManualSafe BootClass = "manual-safe"
	BootAmbiguous  BootClass = "ambiguous"
)

type Classification struct {
	Class  BootClass
	Reason string
}

type BootPolicy struct {
	EarlyTolerance  time.Duration
	MaxStartupDelay time.Duration
	MaxReadSpan     time.Duration
}

func DefaultBootPolicy() BootPolicy {
	return BootPolicy{
		EarlyTolerance:  2 * time.Second,
		MaxStartupDelay: 5 * time.Minute,
		MaxReadSpan:     2 * time.Second,
	}
}

func ClassifyBoot(intent ArmedIntent, observed Observation, policy BootPolicy) Classification {
	if policy.EarlyTolerance < 0 || policy.MaxStartupDelay <= 0 || policy.MaxReadSpan <= 0 {
		return ambiguous("boot policy contains invalid timing bounds")
	}
	if !intent.Enabled {
		return Classification{Class: BootInactive, Reason: "duty cycle was not armed"}
	}
	if intent.Phase != ArmedShutdownRequested {
		return ambiguous("persisted state does not prove that shutdown was requested after wake verification")
	}
	if intent.WakeKind != WakeAbsoluteRTC {
		return ambiguous("relative-wake automatic classification is not supported")
	}
	if intent.ExpectedRTC == 0 || intent.ProgrammedRTCWake != intent.ExpectedRTC || intent.ProgrammedWakeMins != 0 {
		return ambiguous("persisted absolute wake intent is incomplete or inconsistent")
	}
	if observed.RTCAfter < observed.RTCBefore || seconds(observed.RTCAfter-observed.RTCBefore) > policy.MaxReadSpan {
		return ambiguous("board clock sample was reversed or took too long")
	}
	if observed.WakeMinutes != 0 {
		return Classification{Class: BootManualSafe, Reason: "a relative wake countdown was still armed"}
	}

	earlyBoundary := subtractFloor(intent.ExpectedRTC, policy.EarlyTolerance)
	if observed.RTCWake == intent.ExpectedRTC {
		if observed.RTCAfter < earlyBoundary {
			return Classification{Class: BootManualSafe, Reason: "absolute wake remained armed before its expected time"}
		}
		return ambiguous("absolute wake remained armed at or after its expected time")
	}
	if observed.RTCWake != 0 {
		return ambiguous("board absolute wake differed from persisted intent")
	}
	if observed.RTCAfter < earlyBoundary {
		return ambiguous("absolute wake was cleared before its expected time")
	}
	lateBoundary := addSaturating(intent.ExpectedRTC, policy.MaxStartupDelay)
	if observed.RTCAfter > lateBoundary {
		return ambiguous("controller started outside the expected boot window")
	}
	return Classification{Class: BootExpected, Reason: "absolute wake cleared within the expected boot window"}
}

func ambiguous(reason string) Classification {
	return Classification{Class: BootAmbiguous, Reason: reason}
}

func seconds(value uint32) time.Duration { return time.Duration(value) * time.Second }

func subtractFloor(value uint32, duration time.Duration) uint32 {
	amount := uint64(duration / time.Second)
	if amount >= uint64(value) {
		return 0
	}
	return value - uint32(amount)
}

func addSaturating(value uint32, duration time.Duration) uint32 {
	amount := uint64(duration / time.Second)
	total := uint64(value) + amount
	if total > uint64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(total)
}
