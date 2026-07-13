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
  --force            Reinstall when the target version is already installed.
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
  if [ -d "$src" ]; then
    mkdir -p "$dst"
    tar -C "$src" -cf - . | tar -C "$dst" -xf -
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

for command in curl tar awk chmod mkdir mv mktemp; do
  require_cmd "$command"
done
if command -v sha256sum >/dev/null 2>&1; then
  sha256_cmd="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  sha256_cmd="shasum -a 256"
else
  die "required command not found: sha256sum or shasum"
fi

if [ -x "${bin_dir}/apf" ] && [ "$force" = 0 ]; then
  current_version="$("${bin_dir}/apf" --version 2>/dev/null | awk '{print $2; exit}' || true)"
  if [ "$current_version" = "$version" ]; then
    log "apf ${version} is already installed at ${bin_dir}/apf"
    exit 0
  fi
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/alpineform-install.XXXXXX")"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup 0 HUP INT TERM

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
tar -xzf "$archive_path" -C "$extract_dir"
[ -f "${extract_dir}/apf" ] || die "archive does not contain apf"
mkdir -p "$bin_dir" "$share_dir"
install_tmp="${bin_dir}/.apf.${version}.$$"
cp "${extract_dir}/apf" "$install_tmp"
chmod 0755 "$install_tmp"
mv "$install_tmp" "${bin_dir}/apf"

for file in README.md LICENSE NOTICE.md CHANGELOG.md; do
  [ ! -f "${extract_dir}/${file}" ] || cp "${extract_dir}/${file}" "${share_dir}/${file}"
done
copy_tree "${extract_dir}/docs" "${share_dir}/docs"
copy_tree "${extract_dir}/examples" "${share_dir}/examples"

log "Installed apf ${version} to ${bin_dir}/apf"
"${bin_dir}/apf" version
