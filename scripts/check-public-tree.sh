#!/bin/sh
set -eu

fail() {
    printf '%s\n' "public-tree check failed: $*" >&2
    exit 1
}

for path in PROJECT_HANDOVER.md docs/planning .env; do
    [ ! -e "$path" ] || fail "private or local path is present: $path"
done

[ ! -L . ] || fail 'repository root must not be a symlink'
if find . -path './.git' -prune -o -path './.cache' -prune -o -path './build' -prune -o -path './dist' -prune -o -type l -print | grep -q .; then
    fail 'source tree contains a symlink'
fi
if find . -path './.git' -prune -o -path './.cache' -prune -o -path './build' -prune -o -path './dist' -prune -o ! -type f ! -type d -print | grep -q .; then
    fail 'source tree contains an unusual file type'
fi

if grep -RInE \
    --exclude-dir=.git --exclude-dir=.cache --exclude-dir=build \
    --exclude-dir=dist --exclude-dir=testdata \
    --exclude=check-public-tree.sh \
    'BEGIN (RSA |OPENSSH |EC |DSA )?PRIVATE KEY|github_pat_|gh[pousr]_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-' .; then
    fail 'source tree contains a credential or private-key signature'
fi
if grep -RInE \
    --exclude-dir=.git --exclude-dir=.cache --exclude-dir=build \
    --exclude-dir=dist --exclude=check-public-tree.sh \
    'PROJECT_HANDOVER|docs/planning|\.\./planning|Phase [0-9]+|vX\.Y\.Z|SELECTED_USER|CHANGEME' .; then
    fail 'source tree contains private-planning or placeholder language'
fi

if awk '
  /^[[:space:]]*uses:/ {
    ref = $0
    sub(/^.*@/, "", ref)
    sub(/[[:space:]].*$/, "", ref)
    if (length(ref) != 40 || ref !~ /^[0-9a-f]+$/) {
      print FILENAME ":" FNR ": action is not pinned to a full commit SHA: " $0
      failed = 1
    }
  }
  END { exit failed }
' .github/workflows/*.yml; then
    :
else
    fail 'workflow action pin check failed'
fi

if grep -RInE \
    'pull_request_target|runs-on:[[:space:]]*self-hosted|secrets\.' \
    .github/workflows/*.yml; then
    fail 'pull-request workflow has a forbidden privileged construct'
fi

perl -MFile::Find -MFile::Basename=dirname -MFile::Spec -e '
  my $failed = 0;
  find({no_chdir => 1, wanted => sub {
    return if $File::Find::name =~ m{/(?:\.git|\.cache|build|dist)/};
    return unless -f $_ && /\.md$/;
    my $file = $File::Find::name;
    open my $handle, "<", $file or die "$file: $!\n";
    local $/; my $text = <$handle>;
    while ($text =~ /\[[^\]]*\]\(([^)]+)\)/g) {
      my $raw = $1; my $path = $raw; $path =~ s/#.*//;
      next if $path eq q{} || $path =~ m{^(?:https?://|mailto:)};
      my $target = File::Spec->canonpath(File::Spec->catfile(dirname($file), $path));
      if (!-e $target) { print STDERR "$file: missing local link $raw\n"; $failed = 1; }
    }
  }}, q{.});
  exit $failed;
' || fail 'Markdown link check failed'

printf '%s\n' 'public-tree checks passed'
