#!/bin/sh

set -eu
umask 077
export LC_ALL=C

usage() {
    printf '%s\n' 'usage: scripts/render-installer.sh VERSION COMMIT ARCHIVE BASE_URL OUTPUT' >&2
    exit 2
}

[ "$#" -eq 5 ] || usage
release_version=$1
release_commit=$2
release_archive=$3
release_base_url=$4
output=$5

printf '%s\n' "$release_version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$' || usage
printf '%s\n' "$release_commit" | grep -Eq '^[0-9a-f]{40}$' || usage
printf '%s\n' "$release_archive" | grep -Eq '^lifepowerctl_[0-9A-Za-z.-]+_linux_arm64\.tar\.gz$' || usage
printf '%s\n' "$release_base_url" | grep -Eq '^https?://[0-9A-Za-z.:-]+(/[0-9A-Za-z._/-]+)*$' || usage
[ ! -e "$output" ] && [ ! -L "$output" ] || {
    printf '%s\n' "installer render refused: output already exists: $output" >&2
    exit 1
}

script_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_directory/.." && pwd)
temporary=$(mktemp "${TMPDIR:-/tmp}/lifepowerctl-installer.XXXXXX")
output_directory=$(CDPATH= cd -- "$(dirname -- "$output")" && pwd)
rendered=$(mktemp "$output_directory/.lifepowerctl-installer.XXXXXX")
cleanup() {
    rm -f -- "$temporary" "$rendered"
}
trap cleanup EXIT HUP INT TERM

emit_asset() {
    function_name=$1
    source=$2
    printf '%s() {\n' "$function_name"
    printf "%s\n" "    cat <<'__LIFEPOWERCTL_EMBEDDED_ASSET__'"
    cat "$source"
    printf '%s\n' '__LIFEPOWERCTL_EMBEDDED_ASSET__' '}'
}

sed \
    -e "s|@RELEASE_VERSION@|$release_version|g" \
    -e "s|@RELEASE_COMMIT@|$release_commit|g" \
    -e "s|@RELEASE_ARCHIVE@|$release_archive|g" \
    -e "s|@RELEASE_BASE_URL@|$release_base_url|g" \
    "$project_root/packaging/release/install.sh.in" >"$temporary"

{
    while IFS= read -r line
    do
        if [ "$line" = '# @EMBED_POLICY_FUNCTIONS@' ]; then
            emit_asset embedded_config "$project_root/packaging/systemd/config.json.in"
            emit_asset embedded_controller_unit "$project_root/packaging/systemd/lifepowerctl-controller.service.in"
            emit_asset embedded_runner_unit "$project_root/packaging/systemd/lifepowerctl-job-runner.service.in"
            emit_asset embedded_poweroff_unit "$project_root/packaging/systemd/lifepowerctl-poweroff.service.in"
            emit_asset embedded_poweroff_path "$project_root/packaging/systemd/lifepowerctl-poweroff.path.in"
            emit_asset embedded_web_unit "$project_root/packaging/systemd/lifepowerctl.service.in"
            emit_asset embedded_sudoers "$project_root/packaging/systemd/lifepowerctl-sudoers.in"
            emit_asset embedded_tmpfiles "$project_root/packaging/systemd/lifepowerctl-tmpfiles.conf.in"
            emit_asset embedded_layout "$project_root/packaging/systemd/install-layout.txt.in"
        else
            printf '%s\n' "$line"
        fi
    done <"$temporary"
} >"$rendered"

chmod 0755 "$rendered"
/bin/sh -n "$rendered"
if grep -Eq '@(RELEASE|EMBED)_' "$rendered"; then
    printf '%s\n' 'installer render refused: unresolved template token' >&2
    exit 1
fi
mv -- "$rendered" "$output"
