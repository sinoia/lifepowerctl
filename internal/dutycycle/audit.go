package dutycycle

import (
	"github.com/sinoia/lifepowerctl/internal/activity"
	"github.com/sinoia/lifepowerctl/internal/schedule"
)

func recordLifecycleTransition(audit *activity.Audit, lifecycle schedule.Lifecycle) {
	if audit == nil {
		return
	}
	audit.Record(activity.DutyCycleTransition, activity.ActorSystem, "duty-cycle", struct {
		Phase                 schedule.Phase        `json:"phase"`
		ConfigurationRevision uint64                `json:"configuration_revision,omitempty"`
		IntendedOccurrence    uint32                `json:"intended_occurrence,omitempty"`
		Reason                string                `json:"reason,omitempty"`
		WakeDisarmStatus      schedule.DisarmStatus `json:"wake_disarm_status,omitempty"`
		Simulated             bool                  `json:"simulated"`
	}{
		Phase: lifecycle.Phase, ConfigurationRevision: lifecycle.ConfigurationRevision,
		IntendedOccurrence: lifecycle.IntendedOccurrence, Reason: lifecycle.Reason,
		WakeDisarmStatus: lifecycle.WakeDisarmStatus, Simulated: lifecycle.Simulated,
	})
}
