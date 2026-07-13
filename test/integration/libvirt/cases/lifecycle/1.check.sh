assert_remote "destroy-tracked directory and file are converged" \
  "test -d /srv/alpineform-lifecycle && grep -qx managed /srv/alpineform-lifecycle/managed.txt"
assert_remote "state records destroy behavior" \
  "test \"\$(grep -Fc '\"delete_behavior\": \"destroy\"' /var/lib/alpineform/state.json)\" -ge 2"
