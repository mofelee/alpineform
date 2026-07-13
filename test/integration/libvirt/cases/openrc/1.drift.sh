run_remote "stop and disable the managed OpenRC service" \
  "rc-service apf-ci-worker stop && rc-update del apf-ci-worker default"
