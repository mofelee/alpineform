run_remote "remove managed membership and authorized key" \
  "delgroup apfci wheel && rm -f /home/apfci/.ssh/authorized_keys"
