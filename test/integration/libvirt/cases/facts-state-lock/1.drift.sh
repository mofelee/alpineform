run_remote "drift the managed facts marker" \
  "printf 'alpine=drifted\n' > /etc/alpineform-ci-facts"
