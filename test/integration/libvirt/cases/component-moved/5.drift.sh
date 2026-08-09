run_remote "drift the worker configuration after the completed moved blocks are removed" \
  "printf 'revision=drift\n' > /etc/alpineform-moved/worker.conf"
