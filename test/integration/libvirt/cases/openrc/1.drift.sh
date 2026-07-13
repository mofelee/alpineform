run_remote "stop the generated service and drift the raw init script" \
  "rc-service apf-ci-worker stop && rc-update del apf-ci-worker default && printf '%s\n' '# drift' >> /etc/init.d/apf-ci-raw"
