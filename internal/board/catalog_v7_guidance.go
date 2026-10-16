package board

type authoredGuidance struct {
	purpose  string
	behavior string
	safety   string
}

// These summaries are original application text derived from interoperable
// facts in Product Brief revision 13. They are intentionally concise and do
// not reproduce the manufacturer's descriptions.
var registerSet7Guidance = map[Register]authoredGuidance{
	"I2C_REG_VER": {
		purpose:  "Identifies the register layout implemented by the connected board.",
		behavior: "LifePower Control uses this read-only value to select an exact catalogue. Unknown values remain unsupported.",
		safety:   "Never infer compatibility by choosing the nearest known version.",
	},
	"I2C_ADDRESS": {
		purpose:  "Selects the board's seven-bit I2C bus address.",
		behavior: "Changing it moves the board away from the address compiled into the vendor host software.",
		safety:   "An unmatched address can make the board unreachable. This setting requires a dedicated recovery workflow.",
	},
	"LED_STATE": {
		purpose:  "Selects the power LED pattern while the host is running.",
		behavior: "Available modes are off, on, pulsing, and fast flash. Other board states may control the LED independently.",
		safety:   "Turning the LED off removes a useful visible indication but does not turn off the board.",
	},
	"TOUCH_STATE": {
		purpose:  "Reports recent samples from the on/off button input.",
		behavior: "The low four bits form a short history that software can use to distinguish press and release transitions.",
	},
	"TOUCH_CAP_CYCLES": {
		purpose:  "Selects mechanical-button mode or configures capacitive-touch measurement cycles.",
		behavior: "Zero selects the mechanical button. A nonzero value enables capacitive-touch operation and controls its measurement effort.",
		safety:   "Do not enable capacitive mode unless the matching external touch hardware and threshold settings have been reviewed.",
	},
	"TOUCH_THRESHOLD": {
		purpose:  "Sets the signal threshold used to recognize capacitive touch.",
		behavior: "It works with the touch baseline and hysteresis. Mechanical-button mode expects the documented default.",
		safety:   "A poor threshold can cause missed or unintended button activation.",
	},
	"TOUCH_HYSTERESIS": {
		purpose:  "Adds separation between capacitive-touch activation and release levels.",
		behavior: "It is meaningful only in capacitive-touch mode. Mechanical-button mode expects the documented default.",
		safety:   "A poor value can make touch detection unstable or prevent reliable release detection.",
	},
	"DCO_RSEL": {
		purpose:  "Stores part of the factory microcontroller clock calibration.",
		behavior: "The calibrated value contributes to the board controller's clock configuration.",
		safety:   "Treat this factory value as read-only. An incorrect value can disrupt timing throughout the board firmware.",
	},
	"DCO_DCOMOD": {
		purpose:  "Stores part of the factory microcontroller clock calibration.",
		behavior: "The calibrated value controls clock modulation together with DCO_RSEL.",
		safety:   "Treat this factory value as read-only. An incorrect value can disrupt timing throughout the board firmware.",
	},
	"VIN": {
		purpose:  "Reports voltage present at the board's external power input.",
		behavior: "The raw ADC measurement is presented in millivolts using register-set 7 scaling.",
	},
	"VBAT": {
		purpose:  "Reports the battery voltage.",
		behavior: "The raw ADC measurement is presented in millivolts using register-set 7 scaling.",
	},
	"VOUT": {
		purpose:  "Reports the voltage supplied to the host output.",
		behavior: "The raw ADC measurement is presented in millivolts using register-set 7 scaling.",
	},
	"IOUT": {
		purpose:  "Estimates current drawn from the host output.",
		behavior: "The raw measurement is presented in milliamps. It is approximate and should not be treated as precision metering.",
	},
	"VBAT_MIN": {
		purpose:  "Sets the emergency battery voltage at which host power is removed.",
		behavior: "This is the final battery-protection cutoff when orderly shutdown has not completed first.",
		safety:   "Incorrect settings can permit over-discharge or cut power before Linux shuts down. Review it with VBAT_SHDN and VBAT_BOOT.",
	},
	"VBAT_SHDN": {
		purpose:  "Sets the battery voltage at which an orderly host shutdown is requested.",
		behavior: "It should trigger before the emergency VBAT_MIN cutoff and remain below the boot threshold.",
		safety:   "Review the complete battery-threshold ordering; poor hysteresis can cause repeated boot and shutdown cycles.",
	},
	"VBAT_BOOT": {
		purpose:  "Sets the minimum battery voltage that permits host startup.",
		behavior: "The threshold provides recovery margin above the shutdown level before another boot is allowed.",
		safety:   "Keep this above VBAT_SHDN with enough margin for the expected startup load.",
	},
	"VOUT_MAX": {
		purpose:  "Prevents board startup while another source appears to be powering the host output.",
		behavior: "The board refuses to enable its output when the measured output voltage exceeds this guard threshold.",
		safety:   "Changing this protection can allow conflicting power sources. Keep it outside the ordinary editor.",
	},
	"VIN_THRESHOLD": {
		purpose:  "Defines when external input power is considered present.",
		behavior: "Input-dependent automatic boot and shutdown modes compare VIN with this threshold.",
		safety:   "Review it together with AUTO_BOOT and AUTO_SHDN_TIME to avoid unexpected startup or shutdown.",
	},
	"IOUT_SHDN_THRESHOLD": {
		purpose:  "Optionally detects completed host shutdown from low output current.",
		behavior: "Zero disables current-based detection. A nonzero value declares shutdown complete when load current falls sufficiently low.",
		safety:   "A threshold above normal running current can remove power too early; one below shutdown current may never detect completion.",
	},
	"VBAT_OFFSET": {
		purpose:  "Stores the factory correction for battery-voltage measurements.",
		behavior: "The signed offset adjusts the reported battery voltage.",
		safety:   "Treat this factory calibration as read-only unless a documented calibration and recovery procedure is being followed.",
	},
	"VOUT_OFFSET": {
		purpose:  "Stores the factory correction for output-voltage measurements.",
		behavior: "The signed offset adjusts the reported host output voltage.",
		safety:   "Treat this factory calibration as read-only unless a documented calibration and recovery procedure is being followed.",
	},
	"VIN_OFFSET": {
		purpose:  "Stores the factory correction for input-voltage measurements.",
		behavior: "The signed offset adjusts the reported external input voltage.",
		safety:   "Treat this factory calibration as read-only unless a documented calibration and recovery procedure is being followed.",
	},
	"IOUT_OFFSET": {
		purpose:  "Applies an application-specific correction to output-current measurements.",
		behavior: "The signed offset shifts the reported current near the operating region being calibrated.",
		safety:   "Changing it also affects any shutdown decision based on measured output current.",
	},
	"AUTO_BOOT": {
		purpose:  "Selects whether battery and input-power conditions can start the host automatically.",
		behavior: "Choose when the board should start the host automatically. Every automatic mode requires sufficient battery voltage. Modes based on external power also use the configured input-voltage threshold. ‘Respect manual shutdown’ modes keep the host off after it was shut down by the button or software.",
		safety:   "An unsuitable mode can cause unexpected unattended startup. Review battery and input thresholds at the same time.",
	},
	"WAKE_TIME": {
		purpose:  "Schedules a relative wake after the host powers off.",
		behavior: "The value is a countdown in minutes; zero disables it. A remaining value after boot is evidence that another event started the host early.",
		safety:   "Only the duty-cycle coordinator may change this register, and every programmed wake requires read-back verification.",
	},
	"SHDN_DELAY": {
		purpose:  "Adds a delay between detected Linux shutdown and removal of host power.",
		behavior: "The value uses board task ticks rather than precise wall-clock seconds.",
		safety:   "Too little delay can interrupt final shutdown work; changing it also affects systems without reliable UART shutdown detection.",
	},
	"AUTO_SHDN_TIME": {
		purpose:  "Controls delayed shutdown after external input power disappears.",
		behavior: "Zero requests immediate shutdown, values specify minutes, and 65535 disables input-loss shutdown.",
		safety:   "A long delay may exhaust the battery; an immediate delay can make brief input interruptions disruptive.",
	},
	"PI_BOOT_TO": {
		purpose:  "Limits how long the board waits for the host to report a completed startup.",
		behavior: "The value is stored in 10-second increments from 0 to 2550 seconds. Zero disables the timeout; the default is 300 seconds.",
		safety:   "Disabling this timeout can leave the board indefinitely waiting for an unresponsive host to boot.",
	},
	"PI_SHDN_TO": {
		purpose:  "Limits how long the board waits for the host to finish shutting down.",
		behavior: "The value is stored in 10-second increments from 0 to 2550 seconds. Zero disables the timeout; the default is 120 seconds.",
		safety:   "Too short a timeout can cut power during shutdown; disabling it can leave power on indefinitely when detection fails.",
	},
	"RTC_TIME": {
		purpose:  "Stores the board's current Unix time while the host is off.",
		behavior: "The vendor daemon normally reconciles this clock with host time during startup and shutdown.",
		safety:   "Manual changes can invalidate wake calculations and boot classification. Keep this daemon-owned.",
	},
	"RTC_WAKE_TIME": {
		purpose:  "Schedules host wake at an absolute Unix time.",
		behavior: "Zero means no absolute wake. A due or past time can cause the next shutdown to be followed by an immediate restart.",
		safety:   "Only the duty-cycle coordinator may set it, after checking clock validity, timing tolerance, and read-back.",
	},
	"WATCHDOG_CFG": {
		purpose:  "Selects the action taken when the application watchdog expires.",
		behavior: "The watchdog can be disabled, signal failure through the LED, or request host shutdown.",
		safety:   "Shutdown mode can stop an unattended system. Enable it only with a tested timer-servicing process and recovery policy.",
	},
	"WATCHDOG_GRACE": {
		purpose:  "Sets the initial watchdog allowance after host startup.",
		behavior: "The value is stored in 10-second increments and seeds the live watchdog timer when startup completes.",
		safety:   "Allow enough time for every required application dependency to become ready.",
	},
	"WATCHDOG_TIMER": {
		purpose:  "Holds the live application watchdog countdown.",
		behavior: "The owning application refreshes this volatile value; it counts down in 10-second steps when the watchdog is enabled.",
		safety:   "Ordinary register editing must not impersonate watchdog servicing or conceal a failed application.",
	},
	"PI_RUNNING": {
		purpose:  "Coordinates host running and shutdown state between the board and vendor daemon.",
		behavior: "The daemon sets it after startup and clears it during shutdown; clearing it can initiate the shutdown sequence.",
		safety:   "This is an operational control, not a normal boolean setting. Keep it daemon-owned.",
	},
	"CFG_WRITE": {
		purpose:  "Commits eligible configuration registers to board flash.",
		behavior: "Writing the action value 70 starts the commit; reads return zero and other writes do not perform the commit.",
		safety:   "A bad persistent configuration can make recovery difficult or make the device unusable. Require a separate verified flash workflow.",
	},
}
