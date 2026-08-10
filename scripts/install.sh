#!/bin/sh
set -eu

OWNER_REPO="mofelee/alpineform"
GITHUB_RELEASE_BASE_URL="https://github.com/${OWNER_REPO}/releases/download"
GITHUB_API_BASE_URL="${APF_GITHUB_API_BASE_URL:-https://api.github.com/repos/${OWNER_REPO}}"
github_token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"

version=""
prefix=""
bin_dir=""
os_override=""
arch_override=""
dry_run=0
force=0

usage() {
  cat <<'EOF'
Usage: install.sh [options]

Options:
  --version VERSION  Install a specific version, for example v0.1.0-alpha.5.
  --prefix DIR       Defaults to /usr/local when writable, otherwise $HOME/.local.
  --bin-dir DIR      Defaults to <prefix>/bin.
  --os OS            Override OS detection: linux or darwin.
  --arch ARCH        Override architecture detection: amd64 or arm64.
  --dry-run          Print planned actions without downloading or installing.
  --force            Suppress the same-version notice; package data is always refreshed.
  -h, --help         Show this help.
EOF
}

log() {
  printf '%s\n' "$*"
}

die() {
  printf 'apf install: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || die "--version requires a value"
      version="$2"
      shift 2
      ;;
    --prefix)
      [ "$#" -ge 2 ] || die "--prefix requires a value"
      prefix="${2%/}"
      shift 2
      ;;
    --bin-dir)
      [ "$#" -ge 2 ] || die "--bin-dir requires a value"
      bin_dir="${2%/}"
      shift 2
      ;;
    --os)
      [ "$#" -ge 2 ] || die "--os requires a value"
      os_override="$2"
      shift 2
      ;;
    --arch)
      [ "$#" -ge 2 ] || die "--arch requires a value"
      arch_override="$2"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --force)
      force=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) die "unknown option: $1" ;;
  esac
done

detect_os() {
  raw_os="${os_override:-$(uname -s)}"
  case "$raw_os" in
    Linux|linux) printf 'linux\n' ;;
    Darwin|darwin) printf 'darwin\n' ;;
    *) die "unsupported OS: $raw_os" ;;
  esac
}

detect_arch() {
  raw_arch="${arch_override:-$(uname -m)}"
  case "$raw_arch" in
    x86_64|amd64) printf 'amd64\n' ;;
    aarch64|arm64) printf 'arm64\n' ;;
    *) die "unsupported architecture: $raw_arch" ;;
  esac
}

default_prefix() {
  if [ "$(id -u)" = 0 ] || [ -w /usr/local ]; then
    printf '/usr/local\n'
  else
    printf '%s/.local\n' "$HOME"
  fi
}

request() {
  url=$1
  shift
  if [ -n "$github_token" ]; then
    curl --fail --location --retry 3 --show-error --silent \
      -H "Authorization: Bearer ${github_token}" \
      -H "Accept: application/vnd.github+json" \
      "$@" "$url"
  else
    curl --fail --location --retry 3 --show-error --silent "$@" "$url"
  fi
}

github_asset_url() {
  asset_name=$1
  request "${GITHUB_API_BASE_URL}/releases/tags/${version}" |
    awk -F\" -v wanted="$asset_name" '
      $2 == "url" && $4 ~ /\/releases\/assets\// {
        asset_url = $4
      }
      $2 == "name" && $4 == wanted && asset_url != "" {
        print asset_url
        found = 1
        exit
      }
      END { if (!found) exit 1 }
    '
}

latest_version() {
  if [ -n "${APF_RELEASE_BASE_URL:-}" ]; then
    die "--version is required when APF_RELEASE_BASE_URL is set"
  fi
  require_cmd curl
  request "https://api.github.com/repos/${OWNER_REPO}/releases?per_page=20" |
    awk -F\" '
      /"tag_name":/ { tag = $4 }
      /"draft": false/ && tag != "" { print tag; exit }
    '
}

download() {
  url=$1
  out=$2
  asset_name=$3
  case "$url" in
    file://*) cp "${url#file://}" "$out" ;;
    *)
      if [ -n "$github_token" ] && [ -z "${APF_RELEASE_BASE_URL:-}" ]; then
        api_url="$(github_asset_url "$asset_name")" ||
          die "release asset not found: ${asset_name}"
        curl --fail --location --retry 3 --show-error --silent \
          -H "Authorization: Bearer ${github_token}" \
          -H "Accept: application/octet-stream" \
          --output "$out" "$api_url"
      else
        request "$url" --output "$out"
      fi
      ;;
  esac
}

copy_tree() {
  src=$1
  dst=$2
  tree_archive=$3
  [ -d "$src" ] && [ ! -L "$src" ] || return 1
  mkdir -p "$dst" || return 1
  tar -C "$src" -cf "$tree_archive" . || return 1
  tar -C "$dst" -xf "$tree_archive" || return 1
  rm -f "$tree_archive" || return 1
}

validate_manifest_documents() {
  tree=$1
  manifest=$2
  package_error=""
  [ -f "$manifest" ] && [ ! -L "$manifest" ] &&
    [ -r "$manifest" ] && [ -s "$manifest" ] || {
    package_error="documentation package manifest is missing or unreadable"
    return 1
  }
  while IFS= read -r document; do
    [ -n "$document" ] || continue
    case "$document" in \#*) continue ;; esac
    case "$document" in
      /*|../*|*/../*|*/..|./*|*/./*|*/.)
        package_error="documentation package manifest contains an unsafe path: ${document}"
        return 1
        ;;
    esac
    if [ ! -f "${tree}/${document}" ] || [ -L "${tree}/${document}" ] ||
      [ ! -r "${tree}/${document}" ] || [ ! -s "${tree}/${document}" ]; then
      package_error="documentation package omits or cannot read ${document}"
      return 1
    fi
  done <"$manifest"
}

validate_package_tree() {
  tree=$1
  manifest="${tree}/documentation-package-files.txt"
  validate_manifest_documents "$tree" "$manifest" || return 1
  for required in LICENSE examples/quickstart.apf.hcl; do
    if [ ! -f "${tree}/${required}" ] || [ -L "${tree}/${required}" ] ||
      [ ! -r "${tree}/${required}" ] || [ ! -s "${tree}/${required}" ]; then
      package_error="documentation package omits or cannot read ${required}"
      return 1
    fi
  done
}

rollback_published_data() {
  rm -rf "$share_dir" || return 1
  if [ "$had_share" = 1 ]; then
    mv "$data_backup" "$share_dir" || return 1
    data_backup=""
  fi
  data_swapped=0
}

begin_critical_section() {
  critical_section=1
  trap '' HUP INT TERM
}

end_critical_section() {
  trap 'handle_signal HUP' HUP
  trap 'handle_signal INT' INT
  trap 'handle_signal TERM' TERM
  critical_section=0
  if [ -n "$pending_signal" ]; then
    interrupted_by=$pending_signal
    pending_signal=""
    die "interrupted by ${interrupted_by}"
  fi
}

handle_signal() {
  received_signal=$1
  if [ "$critical_section" = 1 ]; then
    [ -n "$pending_signal" ] || pending_signal=$received_signal
    return
  fi
  exit 1
}

lock_owner_is_alive() {
  lock_owner=$1
  if kill_error="$(LC_ALL=C kill -0 "$lock_owner" 2>&1)"; then
    return 0
  fi
  case "$kill_error" in
    *"Operation not permitted"*|*"Permission denied"*) return 0 ;;
  esac
  if [ -d /proc ]; then
    if [ -d "/proc/${lock_owner}" ]; then
      return 0
    fi
    return 1
  fi
  ps -A -o pid 2>/dev/null |
    awk -v expected="$lock_owner" '$1 == expected { found = 1 } END { exit !found }'
}

lock_process_identity() {
  identity_pid=$1
  if [ -r "/proc/${identity_pid}/stat" ]; then
    awk '
      {
        stat = $0
        sub(/^[0-9]+ \(.*\) /, "", stat)
        count = split(stat, field, " ")
        if (count >= 20) print "linux:" field[20]
        exit
      }
    ' "/proc/${identity_pid}/stat"
    return
  fi
  [ ! -d /proc ] || return 1
  LC_ALL=C TZ=UTC ps -p "$identity_pid" -o lstart= 2>/dev/null |
    awk '{$1 = $1; if (NF) print "ps:" $0}'
}

cleanup_dead_lock_claims() {
  target_lock=$1
  for lock_claim in "$target_lock"/choosing.* "$target_lock"/ticket.*.*; do
    [ -e "$lock_claim" ] || continue
    claim_name=${lock_claim##*/}
    case "$claim_name" in
      choosing.*.*)
        claim_fields=${claim_name#choosing.}
        lock_owner=${claim_fields%%.*}
        ;;
      ticket.*.*)
        claim_fields=${claim_name#ticket.}
        claim_fields=${claim_fields#*.}
        lock_owner=${claim_fields%%.*}
        ;;
      *) lock_owner="" ;;
    esac
    case "$lock_owner" in
      ''|*[!0-9]*) rm -f "$lock_claim" ;;
      *)
        if ! lock_owner_is_alive "$lock_owner"; then
          rm -f "$lock_claim"
        else
          claim_identity=""
          IFS= read -r claim_identity <"$lock_claim" || claim_identity=""
          current_identity="$(lock_process_identity "$lock_owner" || true)"
          if [ -n "$claim_identity" ] && [ -n "$current_identity" ] &&
            [ "$claim_identity" != "$current_identity" ]; then
            rm -f "$lock_claim"
          fi
        fi
        ;;
    esac
  done
}

acquire_one_lock() {
  target_lock=$1
  lock_slot=$2
  mkdir -p "$target_lock" ||
    die "could not initialize installer publication queue: ${target_lock}"
  rm -f "$target_lock"/choosing."$$".* "$target_lock"/ticket.*."$$".*
  our_choosing="$(mktemp "${target_lock}/choosing.$$.XXXXXX")" ||
    die "could not allocate installer publication choice: ${target_lock}"
  printf '%s\n' "$installer_identity" >"$our_choosing" &&
    chmod 0644 "$our_choosing" ||
    die "could not enter installer publication queue: ${target_lock}"

  cleanup_dead_lock_claims "$target_lock"
  max_ticket=0
  for lock_claim in "$target_lock"/ticket.*.*; do
    [ -e "$lock_claim" ] || continue
    claim_name=${lock_claim##*/}
    claim_ticket=${claim_name#ticket.}
    claim_ticket=${claim_ticket%%.*}
    case "$claim_ticket" in
      ''|*[!0-9]*) continue ;;
    esac
    if [ "$claim_ticket" -gt "$max_ticket" ]; then
      max_ticket=$claim_ticket
    fi
  done
  our_ticket=$((max_ticket + 1))
  our_claim="$(mktemp "${target_lock}/ticket.${our_ticket}.$$.XXXXXX")" ||
    die "could not allocate installer publication ticket: ${target_lock}"
  printf '%s\n' "$installer_identity" >"$our_claim" &&
    chmod 0644 "$our_claim" ||
    die "could not claim installer publication ticket: ${target_lock}"
  case "$lock_slot" in
    1) lock_one_claim=$our_claim ;;
    2) lock_two_claim=$our_claim ;;
  esac
  rm -f "$our_choosing" ||
    die "could not finalize installer publication ticket: ${target_lock}"

  lock_attempt=0
  while :; do
    cleanup_dead_lock_claims "$target_lock"
    lock_blocked=0
    for lock_claim in "$target_lock"/choosing.*; do
      [ -e "$lock_claim" ] || continue
      lock_blocked=1
      break
    done
    if [ "$lock_blocked" = 0 ]; then
      for lock_claim in "$target_lock"/ticket.*.*; do
        [ -e "$lock_claim" ] || continue
        [ "$lock_claim" != "$our_claim" ] || continue
        claim_name=${lock_claim##*/}
        claim_fields=${claim_name#ticket.}
        claim_ticket=${claim_fields%%.*}
        claim_fields=${claim_fields#*.}
        claim_owner=${claim_fields%%.*}
        case "${claim_ticket}:${claim_owner}" in
          *[!0-9:]*) continue ;;
        esac
        if [ "$claim_ticket" -lt "$our_ticket" ] ||
          { [ "$claim_ticket" -eq "$our_ticket" ] && [ "$claim_owner" -lt "$$" ]; }; then
          lock_blocked=1
          break
        fi
      done
    fi
    if [ "$lock_blocked" = 0 ]; then
      case "$lock_slot" in
        1) lock_one_acquired=1 ;;
        2) lock_two_acquired=1 ;;
      esac
      return
    fi
    lock_attempt=$((lock_attempt + 1))
    [ "$lock_attempt" -lt 300 ] ||
      die "timed out waiting for installer publication queue: ${target_lock}"
    sleep 1
  done
}

release_install_locks() {
  if [ "$lock_two_acquired" = 1 ]; then
    rm -f "$lock_two_claim" || return 1
    lock_two_acquired=0
    lock_two_claim=""
  fi
  if [ "$lock_one_acquired" = 1 ]; then
    rm -f "$lock_one_claim" || return 1
    lock_one_acquired=0
    lock_one_claim=""
  fi
}

version="${version:-$(latest_version)}"
[ -n "$version" ] || die "could not resolve latest release version"
case "$version" in
  *[!A-Za-z0-9._+-]*) die "invalid release version: $version" ;;
esac
os="$(detect_os)"
arch="$(detect_arch)"
prefix="${prefix:-$(default_prefix)}"
bin_dir="${bin_dir:-${prefix}/bin}"
share_dir="${prefix}/share/alpineform"
artifact="apf_${version}_${os}_${arch}.tar.gz"
release_base="${APF_RELEASE_BASE_URL:-${GITHUB_RELEASE_BASE_URL}/${version}}"
artifact_url="${release_base}/${artifact}"
checksums_url="${release_base}/checksums.txt"

if [ "$dry_run" = 1 ]; then
  log "version: ${version}"
  log "platform: ${os}/${arch}"
  log "download: ${artifact_url}"
  log "download: ${checksums_url}"
  log "install binary: ${bin_dir}/apf"
  log "install data: ${share_dir}"
  exit 0
fi

for command in curl tar awk chmod cp find mkdir mv mktemp ps rm sleep sort; do
  require_cmd "$command"
done
if command -v sha256sum >/dev/null 2>&1; then
  sha256_cmd="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  sha256_cmd="shasum -a 256"
else
  die "required command not found: sha256sum or shasum"
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/alpineform-install.XXXXXX")"
installer_identity="$(lock_process_identity "$$" || true)"
[ -n "$installer_identity" ] || installer_identity="pid:$$"
data_stage=""
data_backup=""
data_backup_container=""
install_tmp=""
had_share=0
data_swapped=0
critical_section=0
pending_signal=""
lock_order_file="${tmp_dir}/install-locks"
lock_dir_one=""
lock_dir_two=""
lock_one_acquired=0
lock_two_acquired=0
lock_one_claim=""
lock_two_claim=""
bin_owner_file=""
bin_owner_tmp=""
bin_owner_created=0
install_committed=0
cleanup() {
  trap '' HUP INT TERM
  preserve_data_backup=0
  if [ "$data_swapped" = 1 ]; then
    if ! rollback_published_data; then
      preserve_data_backup=1
      log "warning: interrupted install could not restore package data from ${data_backup}"
    fi
  fi
  rm -rf "$tmp_dir"
  [ -z "$data_stage" ] || rm -rf "$data_stage"
  if [ -n "$data_backup_container" ] && [ "$preserve_data_backup" = 0 ]; then
    rm -rf "$data_backup_container"
  fi
  [ -z "$install_tmp" ] || rm -f "$install_tmp"
  [ -z "$bin_owner_tmp" ] || rm -f "$bin_owner_tmp"
  if [ "$bin_owner_created" = 1 ] && [ "$install_committed" = 0 ]; then
    rm -f "$bin_owner_file"
  fi
  if [ -n "$lock_dir_one" ]; then
    rm -f "$lock_dir_one"/choosing."$$".* "$lock_dir_one"/ticket.*."$$".*
  fi
  if [ -n "$lock_dir_two" ]; then
    rm -f "$lock_dir_two"/choosing."$$".* "$lock_dir_two"/ticket.*."$$".*
  fi
  if ! release_install_locks; then
    log "warning: could not release installer publication locks"
  fi
}
trap cleanup 0
trap 'handle_signal HUP' HUP
trap 'handle_signal INT' INT
trap 'handle_signal TERM' TERM

mkdir -p "$prefix" "$bin_dir"
physical_prefix="$(CDPATH= cd -P "$prefix" && pwd)"
physical_bin_dir="$(CDPATH= cd -P "$bin_dir" && pwd)"
data_lock_dir="${physical_prefix}/.alpineform-install-locks"
binary_lock_dir="${physical_bin_dir}/.alpineform-install-locks"
bin_owner_file="${physical_bin_dir}/.alpineform-install-prefix"
printf '%s\n%s\n' "$data_lock_dir" "$binary_lock_dir" |
  LC_ALL=C sort -u >"$lock_order_file"
lock_dir_one="$(awk 'NR == 1 { print; exit }' "$lock_order_file")"
lock_dir_two="$(awk 'NR == 2 { print; exit }' "$lock_order_file")"
acquire_one_lock "$lock_dir_one" 1
[ -z "$lock_dir_two" ] || acquire_one_lock "$lock_dir_two" 2

if [ -e "$bin_owner_file" ] || [ -L "$bin_owner_file" ]; then
  [ -r "$bin_owner_file" ] && [ ! -d "$bin_owner_file" ] ||
    die "binary directory ownership marker is unreadable: ${bin_owner_file}"
  IFS= read -r bin_owner <"$bin_owner_file" || bin_owner=""
  [ "$bin_owner" = "$physical_prefix" ] ||
    die "binary directory ${physical_bin_dir} already belongs to prefix ${bin_owner}"
else
  bin_owner_tmp="$(mktemp "${physical_bin_dir}/.alpineform-install-prefix.XXXXXX")"
  printf '%s\n' "$physical_prefix" >"$bin_owner_tmp" ||
    die "could not stage binary directory ownership marker"
  chmod 0644 "$bin_owner_tmp" ||
    die "could not set binary directory ownership marker permissions"
  begin_critical_section
  mv "$bin_owner_tmp" "$bin_owner_file" ||
    die "could not publish binary directory ownership marker"
  bin_owner_tmp=""
  bin_owner_created=1
  end_critical_section
fi

if [ -x "${bin_dir}/apf" ] && [ "$force" = 0 ]; then
  current_version="$("${bin_dir}/apf" --version 2>/dev/null | awk '{print $2; exit}' || true)"
  if [ "$current_version" = "$version" ]; then
    log "apf ${version} is already installed at ${bin_dir}/apf; verifying package data"
  fi
fi

archive_path="${tmp_dir}/${artifact}"
checksums_path="${tmp_dir}/checksums.txt"
extract_dir="${tmp_dir}/extract"
log "Downloading ${artifact_url}"
download "$artifact_url" "$archive_path" "$artifact"
log "Downloading ${checksums_url}"
download "$checksums_url" "$checksums_path" checksums.txt

expected_sha="$(awk -v name="$artifact" '$2 == name {print $1; exit}' "$checksums_path")"
[ -n "$expected_sha" ] || die "checksum entry not found for ${artifact}"
actual_sha="$($sha256_cmd "$archive_path" | awk '{print $1; exit}')"
[ "$expected_sha" = "$actual_sha" ] || die "checksum mismatch for ${artifact}"

mkdir -p "$extract_dir"
archive_entries="${tmp_dir}/archive-entries"
tar -tzf "$archive_path" >"$archive_entries"
if ! awk '
  {
    path = $0
    sub(/^\.\//, "", path)
    if (path ~ /^\//) exit 1
    count = split(path, component, "/")
    for (i = 1; i <= count; i++) {
      if (component[i] == "..") exit 1
    }
  }
' "$archive_entries"; then
  die "archive contains an unsafe path"
fi
tar -xzf "$archive_path" -C "$extract_dir"
[ -f "${extract_dir}/apf" ] && [ ! -L "${extract_dir}/apf" ] ||
  die "archive does not contain a regular apf binary"
[ -z "$(find "$extract_dir" ! -type f ! -type d -print -quit)" ] ||
  die "archive contains an unsupported filesystem entry"
documentation_manifest="${extract_dir}/scripts/documentation-package-files.txt"
[ -s "$documentation_manifest" ] || die "archive does not contain the documentation package manifest"
validate_manifest_documents "$extract_dir" "$documentation_manifest" || die "$package_error"
[ -f "${extract_dir}/LICENSE" ] && [ ! -L "${extract_dir}/LICENSE" ] &&
  [ -r "${extract_dir}/LICENSE" ] && [ -s "${extract_dir}/LICENSE" ] ||
  die "archive does not contain a readable LICENSE"
[ -d "${extract_dir}/docs" ] && [ ! -L "${extract_dir}/docs" ] ||
  die "archive does not contain a regular docs directory"
[ -d "${extract_dir}/examples" ] && [ ! -L "${extract_dir}/examples" ] ||
  die "archive does not contain a regular examples directory"

share_parent=${share_dir%/*}
mkdir -p "$bin_dir" "$share_parent"
[ ! -d "${bin_dir}/apf" ] || die "install target is a directory: ${bin_dir}/apf"
data_stage="$(mktemp -d "${share_parent}/.alpineform-stage.${version}.XXXXXX")"
data_backup_container="$(mktemp -d \
  "${share_parent}/.alpineform-backup.${version}.XXXXXX")"
data_backup="${data_backup_container}/previous"
cp "$documentation_manifest" "${data_stage}/documentation-package-files.txt"
for file in README.md README.zh-CN.md LICENSE NOTICE.md NOTICE.zh-CN.md \
  SECURITY.md SECURITY.zh-CN.md CHANGELOG.md CHANGELOG.zh-CN.md; do
  [ -f "${extract_dir}/${file}" ] && [ ! -L "${extract_dir}/${file}" ] &&
    [ -r "${extract_dir}/${file}" ] && [ -s "${extract_dir}/${file}" ] ||
    die "archive does not contain readable ${file}"
  cp "${extract_dir}/${file}" "${data_stage}/${file}"
done
copy_tree "${extract_dir}/docs" "${data_stage}/docs" "${tmp_dir}/docs.tar" ||
  die "could not stage the documentation tree"
copy_tree "${extract_dir}/examples" "${data_stage}/examples" "${tmp_dir}/examples.tar" ||
  die "could not stage the examples tree"
validate_package_tree "$data_stage" || die "$package_error"

install_tmp="$(mktemp "${bin_dir}/.apf.${version}.XXXXXX")"
cp "${extract_dir}/apf" "$install_tmp"
chmod 0755 "$install_tmp"
if ! staged_version_output="$("$install_tmp" --version 2>/dev/null)"; then
  die "archive binary cannot run on ${os}/${arch}"
fi
[ "$staged_version_output" = "apf ${version}" ] ||
  die "archive binary reports '${staged_version_output}', expected 'apf ${version}'"

begin_critical_section
if [ -e "$share_dir" ] || [ -L "$share_dir" ]; then
  mv "$share_dir" "$data_backup" || die "could not preserve existing package data"
  had_share=1
  data_swapped=1
fi
if ! mv "$data_stage" "$share_dir"; then
  if [ "$had_share" = 1 ]; then
    mv "$data_backup" "$share_dir" ||
      die "could not publish or restore package data; previous data remains at ${data_backup}"
    data_backup=""
  fi
  data_swapped=0
  die "could not publish package data"
fi
data_stage=""
data_swapped=1
if ! validate_package_tree "$share_dir"; then
  publish_error=$package_error
  rollback_published_data ||
    die "${publish_error}; rollback failed and previous data remains at ${data_backup}"
  die "$publish_error"
fi
if ! mv "$install_tmp" "${bin_dir}/apf"; then
  rollback_published_data ||
    die "could not publish apf; package-data rollback failed at ${data_backup}"
  die "could not publish apf"
fi
install_tmp=""
data_swapped=0
if [ "$had_share" = 1 ]; then
  if ! rm -rf "$data_backup_container"; then
    log "warning: installed successfully but could not remove previous package data at ${data_backup_container}"
  fi
  data_backup=""
fi
if [ -n "$data_backup_container" ]; then
  rm -rf "$data_backup_container" ||
    log "warning: installed successfully but could not remove ${data_backup_container}"
  data_backup_container=""
fi
install_committed=1
end_critical_section

log "Installed apf ${version} to ${bin_dir}/apf"
installed_version_output="$("${bin_dir}/apf" --version)" ||
  die "installed apf failed final version verification"
[ "$installed_version_output" = "apf ${version}" ] ||
  die "installed apf reports '${installed_version_output}', expected 'apf ${version}'"
log "$installed_version_output"
begin_critical_section
release_install_locks || die "could not release installer publication locks"
end_critical_section
