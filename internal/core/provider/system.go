package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
	corestate "github.com/mofelee/alpineform/internal/core/state"
)

var (
	providerHostnameLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	providerTimezonePattern      = regexp.MustCompile(`^[A-Za-z0-9_+.-]+(?:/[A-Za-z0-9_+.-]+)*$`)
)

const systemHostnameInspectScript = `set -eu
if [ -L /etc/hostname ]; then echo 'refusing symbolic-link /etc/hostname' >&2; exit 1; fi
file_exists=false
file_hostname=
if [ -e /etc/hostname ]; then
  if [ ! -f /etc/hostname ]; then echo '/etc/hostname is not a regular file' >&2; exit 1; fi
  file_exists=true
  file_hostname=$(sed -n '1p' /etc/hostname | tr -d '\r\n')
fi
echo hostname
echo "$file_exists"
echo "$file_hostname"
hostname
`

const systemHostnameApplyScript = `set -eu
want=$1
if [ -L /etc/hostname ]; then echo 'refusing symbolic-link /etc/hostname' >&2; exit 1; fi
if [ -e /etc/hostname ] && [ ! -f /etc/hostname ]; then echo '/etc/hostname is not a regular file' >&2; exit 1; fi
tmp=$(mktemp /etc/.alpineform-hostname.XXXXXX)
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT HUP INT TERM
printf '%s\n' "$want" >"$tmp"
chmod 0644 "$tmp"
chown root:root "$tmp"
mv -f "$tmp" /etc/hostname
trap - EXIT HUP INT TERM
hostname "$want"
`

const systemTimezoneInspectScript = `set -eu
timezone=$1
zone=/usr/share/zoneinfo/$timezone
zone_exists=false
localtime_matches=false
timezone_matches=false
paths_exist=false
if [ -e "$zone" ]; then
  resolved=$(readlink -f "$zone" 2>/dev/null || true)
  case "$resolved" in /usr/share/zoneinfo/*) [ -f "$resolved" ] && zone_exists=true ;; esac
fi
if [ -e /etc/localtime ] || [ -L /etc/localtime ]; then
  paths_exist=true
  if [ -L /etc/localtime ]; then
    current=$(readlink -f /etc/localtime 2>/dev/null || true)
    desired=$(readlink -f "$zone" 2>/dev/null || true)
    [ -n "$desired" ] && [ "$current" = "$desired" ] && localtime_matches=true
  elif [ ! -f /etc/localtime ]; then
    echo '/etc/localtime is not a regular file or symbolic link' >&2
    exit 1
  fi
fi
if [ -L /etc/timezone ]; then echo 'refusing symbolic-link /etc/timezone' >&2; exit 1; fi
if [ -e /etc/timezone ]; then
  paths_exist=true
  if [ ! -f /etc/timezone ]; then echo '/etc/timezone is not a regular file' >&2; exit 1; fi
  current_timezone=$(sed -n '1p' /etc/timezone | tr -d '\r\n')
  [ "$current_timezone" = "$timezone" ] && timezone_matches=true
fi
echo timezone
echo "$zone_exists"
echo "$localtime_matches"
echo "$timezone_matches"
echo "$paths_exist"
`

const systemTimezoneApplyScript = `set -eu
timezone=$1
zone=/usr/share/zoneinfo/$timezone
resolved=$(readlink -f "$zone" 2>/dev/null || true)
case "$resolved" in /usr/share/zoneinfo/*) ;; *) echo 'timezone resolves outside /usr/share/zoneinfo' >&2; exit 1 ;; esac
[ -f "$resolved" ] || { echo 'timezone zoneinfo file is missing' >&2; exit 1; }
if [ -e /etc/localtime ] && [ -d /etc/localtime ]; then echo '/etc/localtime is a directory' >&2; exit 1; fi
tmp_link=/etc/.alpineform-localtime.$$
tmp_timezone=$(mktemp /etc/.alpineform-timezone.XXXXXX)
cleanup() { rm -f "$tmp_link" "$tmp_timezone"; }
trap cleanup EXIT HUP INT TERM
ln -s "$zone" "$tmp_link"
printf '%s\n' "$timezone" >"$tmp_timezone"
chmod 0644 "$tmp_timezone"
chown root:root "$tmp_timezone"
mv -f "$tmp_link" /etc/localtime
mv -f "$tmp_timezone" /etc/timezone
trap - EXIT HUP INT TERM
`

func inspectSystemHostname(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	desired := stringValue(node.Desired, "hostname")
	if !validProviderHostname(desired) {
		return engine.ObservedResource{}, fmt.Errorf("invalid system hostname %q", desired)
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.system_hostname", Script: systemHostnameInspectScript})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) != 4 || lines[0] != "hostname" || (lines[1] != "true" && lines[1] != "false") {
		return engine.ObservedResource{}, fmt.Errorf("inspect system hostname returned an invalid response")
	}
	observed := cloneDesired(node.Desired)
	observed["file_hostname"] = lines[2]
	observed["runtime_hostname"] = lines[3]
	digest := ""
	if lines[1] == "true" && lines[2] == desired && lines[3] == desired {
		digest = corestate.Digest(node.Desired)
	}
	return engine.ObservedResource{Exists: lines[1] == "true", Values: observed, Digest: digest}, nil
}

func applySystemHostname(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	desired := stringValue(node.Desired, "hostname")
	if !validProviderHostname(desired) {
		return engine.ObservedResource{}, fmt.Errorf("invalid system hostname %q", desired)
	}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.system_hostname", Script: systemHostnameApplyScript, Arguments: []string{desired}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectSystemHostname(ctx, runner, node)
}

func inspectSystemTimezone(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	timezone := stringValue(node.Desired, "timezone")
	if !validProviderTimezone(timezone) {
		return engine.ObservedResource{}, fmt.Errorf("invalid system timezone %q", timezone)
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.system_timezone", Script: systemTimezoneInspectScript, Arguments: []string{timezone}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 5 || lines[0] != "timezone" {
		return engine.ObservedResource{}, fmt.Errorf("inspect system timezone returned an invalid response")
	}
	for _, value := range lines[1:] {
		if value != "true" && value != "false" {
			return engine.ObservedResource{}, fmt.Errorf("inspect system timezone returned an invalid boolean")
		}
	}
	observed := cloneDesired(node.Desired)
	observed["zone_exists"] = lines[1] == "true"
	observed["localtime_matches"] = lines[2] == "true"
	observed["timezone_file_matches"] = lines[3] == "true"
	digest := ""
	if lines[1] == "true" && lines[2] == "true" && lines[3] == "true" {
		digest = corestate.Digest(node.Desired)
	}
	exists := lines[4] == "true"
	return engine.ObservedResource{Exists: exists, Values: observed, Digest: digest}, nil
}

func applySystemTimezone(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	timezone := stringValue(node.Desired, "timezone")
	if !validProviderTimezone(timezone) {
		return engine.ObservedResource{}, fmt.Errorf("invalid system timezone %q", timezone)
	}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.system_timezone", Script: systemTimezoneApplyScript, Arguments: []string{timezone}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectSystemTimezone(ctx, runner, node)
}

func validProviderHostname(value string) bool {
	if value == "" || len(value) > 253 || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if !providerHostnameLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func validProviderTimezone(value string) bool {
	if value == "" || !providerTimezonePattern.MatchString(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
