#!/bin/sh
set -eu

# Dangerous hardware-gate fault proxy. This is installed only temporarily in the
# controller service's mount namespace; it must never replace the vendor CLI.
real_cli=${LIFEPOWERCTL_GATE_REAL_CLI:-/usr/lib/lifepowerctl/hardware-gate/lifepo4wered-cli.real}
state_dir=${LIFEPOWERCTL_GATE_STATE_DIR:-/var/lib/lifepowerctl/hardware-gate}
mode_file=$state_dir/mode
mode=
if [ -r "$mode_file" ]; then
	mode=$(sed -n '1p' "$mode_file")
fi

case "$mode:$#:$1:${2-}:${3-}" in
	fail-absolute-write:3:SET:RTC_WAKE_TIME:0)
		;;
	fail-absolute-write:3:SET:RTC_WAKE_TIME:*)
		rm -f "$mode_file"
		echo "hardware-gate: injected RTC_WAKE_TIME write failure" >&2
		exit 70
		;;
	mismatch-absolute-read:3:SET:RTC_WAKE_TIME:0)
		;;
	mismatch-absolute-read:3:SET:RTC_WAKE_TIME:*)
		"$real_cli" "$@"
		: >"$state_dir/mismatch-armed"
		exit 0
		;;
	mismatch-absolute-read:2:GET:RTC_WAKE_TIME:)
		if [ -f "$state_dir/mismatch-armed" ]; then
			value=$("$real_cli" "$@")
			rm -f "$state_dir/mismatch-armed" "$mode_file"
			case "$value" in *[!0-9]*|'') exit 71 ;; esac
			echo $((value + 1))
			exit 0
		fi
		;;
	pause-absolute-write:3:SET:RTC_WAKE_TIME:0)
		;;
	pause-absolute-write:3:SET:RTC_WAKE_TIME:*)
		: >"$state_dir/pause-entered"
		while [ -f "$mode_file" ]; do sleep 1; done
		;;
esac

exec "$real_cli" "$@"
