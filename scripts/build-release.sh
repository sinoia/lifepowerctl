#!/bin/sh

set -eu
umask 077
export LC_ALL=C

usage() {
    printf '%s\n' 'usage: scripts/build-release.sh vMAJOR.MINOR.PATCH[-PRERELEASE]' >&2
    exit 2
}

[ "$#" -eq 1 ] || usage
release_version=$1
printf '%s\n' "$release_version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$' || usage

script_directory=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH='' cd -- "$script_directory/.." && pwd)
cd "$project_root"

source_status=$(git status --porcelain=v1 --untracked-files=all | sed '\|^?? \.review/|d')
if [ -n "$source_status" ]; then
    printf '%s\n' 'release build refused: the source tree has tracked or untracked changes' >&2
    printf '%s\n' "$source_status" >&2
    exit 1
fi

release_commit=$(git rev-parse --verify HEAD)
printf '%s\n' "$release_commit" | grep -Eq '^[0-9a-f]{40}$' || {
    printf '%s\n' 'release build refused: HEAD is not a full Git commit ID' >&2
    exit 1
}

require_release_tag=${REQUIRE_RELEASE_TAG:-0}
case "$require_release_tag" in
    0) ;;
    1)
        tag_commit=$(git rev-parse --verify "refs/tags/$release_version^{commit}" 2>/dev/null) || {
            printf '%s\n' "release build refused: required tag does not exist: $release_version" >&2
            exit 1
        }
        [ "$tag_commit" = "$release_commit" ] || {
            printf '%s\n' "release build refused: $release_version does not point to HEAD" >&2
            exit 1
        }
        ;;
    *)
        printf '%s\n' 'release build refused: REQUIRE_RELEASE_TAG must be 0 or 1' >&2
        exit 1
        ;;
esac
source_epoch=$(git show -s --format=%ct HEAD)
printf '%s\n' "$source_epoch" | grep -Eq '^[1-9][0-9]*$' || {
    printf '%s\n' 'release build refused: commit timestamp is invalid' >&2
    exit 1
}

version_number=${release_version#v}
archive_name="lifepowerctl_${version_number}_linux_arm64.tar.gz"
output_parent="$project_root/dist"
output_directory="$output_parent/$release_version"
[ ! -e "$output_directory" ] || {
    printf '%s\n' "release build refused: output already exists: $output_directory" >&2
    exit 1
}
mkdir -p "$output_parent" "$project_root/.cache/go-build" "$project_root/.cache/go-mod"

work_directory=$(mktemp -d "$output_parent/.release.XXXXXX")
cleanup() {
    case "$work_directory" in
        "$output_parent"/.release.*) rm -rf -- "$work_directory" ;;
        *) printf '%s\n' "refusing unsafe temporary cleanup: $work_directory" >&2 ;;
    esac
}
trap cleanup EXIT HUP INT TERM

stage_parent="$work_directory/stage"
stage_directory="$stage_parent/lifepowerctl"
asset_directory="$work_directory/assets"
mkdir -p "$stage_directory" "$asset_directory"

go_command=${GO:-go}
linker_flags="-s -w -X main.version=$release_version -X main.commit=$release_commit -X main.sourceState=clean"
env GOMAXPROCS=1 GOMEMLIMIT=64MiB GOGC=5 \
    GOCACHE="$project_root/.cache/go-build" \
    GOMODCACHE="$project_root/.cache/go-mod" \
    GOFLAGS=-p=1 CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    "$go_command" build -buildvcs=false -mod=readonly -trimpath -p=1 \
    -ldflags "$linker_flags" -o "$stage_directory/lifepowerctl" ./cmd/lifepowerctl

target_build_info=$("$go_command" version -m "$stage_directory/lifepowerctl")
for expected_setting in 'CGO_ENABLED=0' 'GOOS=linux' 'GOARCH=arm64'
do
    printf '%s\n' "$target_build_info" | grep -Eq "^[[:space:]]+build[[:space:]]+$expected_setting$" || {
        printf '%s\n' "release build refused: staged binary is missing build setting $expected_setting" >&2
        exit 1
    }
done

metadata_binary="$stage_directory/lifepowerctl"
host_goos=$("$go_command" env GOOS)
host_goarch=$("$go_command" env GOARCH)
if [ "$host_goos:$host_goarch" != linux:arm64 ]; then
    metadata_binary="$work_directory/metadata-verifier"
    env GOMAXPROCS=1 GOMEMLIMIT=64MiB GOGC=5 \
        GOCACHE="$project_root/.cache/go-build" \
        GOMODCACHE="$project_root/.cache/go-mod" \
        GOFLAGS=-p=1 CGO_ENABLED=0 \
        "$go_command" build -buildvcs=false -mod=readonly -trimpath -p=1 \
        -ldflags "$linker_flags" -o "$metadata_binary" ./cmd/lifepowerctl
fi

machine_info=$("$metadata_binary" version --machine)
for expected in \
    'format=1' \
    'product=lifepowerctl' \
    "version=$release_version" \
    "commit=$release_commit" \
    'source_state=clean'
do
    printf '%s\n' "$machine_info" | grep -Fqx "$expected" || {
        printf '%s\n' "release build refused: staged binary is missing $expected" >&2
        exit 1
    }
done
state_schema=$(printf '%s\n' "$machine_info" | sed -n 's/^state_schema_current=//p')
minimum_schema=$(printf '%s\n' "$machine_info" | sed -n 's/^state_schema_min_readable=//p')
case "$state_schema:$minimum_schema" in
    *[!0-9:]*|:*|*:|*:*:*)
        printf '%s\n' 'release build refused: staged binary schema metadata is invalid' >&2
        exit 1
        ;;
esac
[ "$state_schema" -ge 1 ] && [ "$minimum_schema" -ge 1 ] && [ "$minimum_schema" -le "$state_schema" ] || {
    printf '%s\n' 'release build refused: staged binary schema range is invalid' >&2
    exit 1
}

install -m 0644 LICENSE "$stage_directory/LICENSE"
install -m 0644 README.md "$stage_directory/README.md"
{
    printf '%s\n' \
        'format=1' \
        'product=lifepowerctl' \
        "version=$release_version" \
        "commit=$release_commit" \
        'source_state=clean' \
        'target_os=linux' \
        'target_arch=arm64' \
        "state_schema_current=$state_schema" \
        "state_schema_min_readable=$minimum_schema"
} >"$stage_directory/release-manifest.txt"
chmod 0644 "$stage_directory/release-manifest.txt"

release_base_url="https://github.com/sinoia/lifepowerctl/releases/download/$release_version"
scripts/render-installer.sh \
    "$release_version" "$release_commit" "$archive_name" "$release_base_url" \
    "$asset_directory/install.sh"
sed \
    -e "s|@RELEASE_VERSION@|$release_version|g" \
    -e "s|@RELEASE_COMMIT@|$release_commit|g" \
    packaging/release/uninstall.sh.in >"$asset_directory/uninstall.sh"
chmod 0755 "$asset_directory/uninstall.sh"

tar --sort=name --format=ustar --mtime="@$source_epoch" \
    --owner=0 --group=0 --numeric-owner \
    -C "$stage_parent" -cf "$work_directory/archive.tar" lifepowerctl
gzip -n "$work_directory/archive.tar"
mv "$work_directory/archive.tar.gz" "$asset_directory/$archive_name"

tar -tzf "$asset_directory/$archive_name" >"$work_directory/archive-members"
tar -tvzf "$asset_directory/$archive_name" >"$work_directory/archive-members-long"
{
    printf '%s\n' \
        'lifepowerctl/' \
        'lifepowerctl/LICENSE' \
        'lifepowerctl/README.md' \
        'lifepowerctl/lifepowerctl' \
        'lifepowerctl/release-manifest.txt'
} >"$work_directory/expected-members"
diff -u "$work_directory/expected-members" "$work_directory/archive-members"
if awk 'substr($1,1,1) != "d" && substr($1,1,1) != "-" { exit 1 }' "$work_directory/archive-members-long"; then
    :
else
    printf '%s\n' 'release build refused: archive contains a non-file member' >&2
    exit 1
fi

archive_bytes=$(wc -c <"$asset_directory/$archive_name")
binary_bytes=$(wc -c <"$stage_directory/lifepowerctl")
[ "$archive_bytes" -le 33554432 ] || {
    printf '%s\n' 'release build refused: compressed archive exceeds 32 MiB' >&2
    exit 1
}
[ "$binary_bytes" -le 25165824 ] || {
    printf '%s\n' 'release build refused: binary exceeds 24 MiB' >&2
    exit 1
}
for text_file in LICENSE README.md release-manifest.txt
do
    text_bytes=$(wc -c <"$stage_directory/$text_file")
    [ "$text_bytes" -le 1048576 ] || {
        printf '%s\n' "release build refused: $text_file exceeds 1 MiB" >&2
        exit 1
    }
done

(
    cd "$asset_directory"
    sha256sum "$archive_name" install.sh uninstall.sh >checksums.txt
)

mv "$asset_directory" "$output_directory"
printf '%s\n' "release assets written to $output_directory"
