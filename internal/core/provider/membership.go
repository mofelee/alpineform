package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/mofelee/alpineform/internal/core/backend"
	"github.com/mofelee/alpineform/internal/core/engine"
	"github.com/mofelee/alpineform/internal/core/graph"
)

const membershipInspectScript = `set -eu
user=$1
group=$2
user_entry=$(awk -F: -v user="$user" '$1 == user { print; exit }' /etc/passwd)
group_entry=$(awk -F: -v group="$group" '$1 == group { print; exit }' /etc/group)
if [ -z "$user_entry" ] || [ -z "$group_entry" ]; then
  echo missing
  exit 0
fi
if printf '%s\n' "$group_entry" | awk -F: -v user="$user" '
{
  count=split($4, members, ",")
  for (i=1; i<=count; i++) {
    if (members[i] == user) found=1
  }
}
END { exit found ? 0 : 1 }
'; then
  echo membership
else
  echo missing
fi
`

const membershipApplyScript = `set -eu
user=$1
group=$2
user_entry=$(awk -F: -v user="$user" '$1 == user { print; exit }' /etc/passwd)
group_entry=$(awk -F: -v group="$group" '$1 == group { print; exit }' /etc/group)
if [ -z "$user_entry" ]; then
  echo 'user does not exist' >&2
  exit 1
fi
if [ -z "$group_entry" ]; then
  echo 'supplementary group does not exist' >&2
  exit 1
fi
primary_gid=$(printf '%s\n' "$user_entry" | awk -F: '{ print $4 }')
group_gid=$(printf '%s\n' "$group_entry" | awk -F: '{ print $3 }')
if [ "$primary_gid" = "$group_gid" ]; then
  echo 'refusing to add the primary group as a supplementary membership' >&2
  exit 1
fi
if ! printf '%s\n' "$group_entry" | awk -F: -v user="$user" '
{
  count=split($4, members, ",")
  for (i=1; i<=count; i++) {
    if (members[i] == user) found=1
  }
}
END { exit found ? 0 : 1 }
'; then
  addgroup "$user" "$group"
fi
`

const membershipDeleteScript = `set -eu
user=$1
group=$2
user_entry=$(awk -F: -v user="$user" '$1 == user { print; exit }' /etc/passwd)
group_entry=$(awk -F: -v group="$group" '$1 == group { print; exit }' /etc/group)
if [ -z "$user_entry" ] || [ -z "$group_entry" ]; then
  exit 0
fi
primary_gid=$(printf '%s\n' "$user_entry" | awk -F: '{ print $4 }')
group_gid=$(printf '%s\n' "$group_entry" | awk -F: '{ print $3 }')
if [ "$primary_gid" = "$group_gid" ]; then
  echo 'refusing to remove a primary group as a supplementary membership' >&2
  exit 1
fi
if printf '%s\n' "$group_entry" | awk -F: -v user="$user" '
{
  count=split($4, members, ",")
  for (i=1; i<=count; i++) {
    if (members[i] == user) found=1
  }
}
END { exit found ? 0 : 1 }
'; then
  delgroup "$user" "$group"
fi
`

func inspectMembership(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	user, group, err := desiredMembershipIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	output, err := runner.Run(ctx, backend.Command{Name: "inspect.membership", Script: membershipInspectScript, Arguments: []string{user, group}})
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if strings.TrimSpace(string(output)) != "membership" {
		return engine.ObservedResource{}, nil
	}
	return engine.ObservedResource{Exists: true, Values: cloneDesired(node.Desired)}, nil
}

func applyMembership(ctx context.Context, runner backend.Runner, node graph.Node) (engine.ObservedResource, error) {
	user, group, err := desiredMembershipIdentity(node)
	if err != nil {
		return engine.ObservedResource{}, err
	}
	if _, err := runner.Run(ctx, backend.Command{Name: "apply.membership", Script: membershipApplyScript, Arguments: []string{user, group}}); err != nil {
		return engine.ObservedResource{}, err
	}
	return inspectMembership(ctx, runner, node)
}

func deleteMembership(ctx context.Context, runner backend.Runner, step engine.Step) error {
	user, group := deletionIdentity(step, "user", "group")
	if err := validateManagedUserName(user); err != nil {
		return err
	}
	if err := validateManagedAccountName(group); err != nil {
		return fmt.Errorf("supplementary group: %w", err)
	}
	_, err := runner.Run(ctx, backend.Command{Name: "delete.membership", Script: membershipDeleteScript, Arguments: []string{user, group}})
	return err
}

func desiredMembershipIdentity(node graph.Node) (string, string, error) {
	user := stringValue(node.Desired, "user")
	group := stringValue(node.Desired, "group")
	if err := validateManagedUserName(user); err != nil {
		return "", "", err
	}
	if err := validateManagedAccountName(group); err != nil {
		return "", "", fmt.Errorf("supplementary group: %w", err)
	}
	return user, group, nil
}

func deletionIdentity(step engine.Step, first, second string) (string, string) {
	values := map[string]any(nil)
	if step.Node.Desired != nil {
		values, _ = step.Node.Desired["delete"].(map[string]any)
	}
	if values == nil && step.Prior != nil {
		values = step.Prior.Delete
	}
	return stringValue(values, first), stringValue(values, second)
}
