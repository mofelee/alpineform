run_remote "drift managed file content and metadata" \
  "printf 'enabled=false\n' > /etc/alpineform-ci/app.conf && chmod 0666 /etc/alpineform-ci/app.conf && chmod 0777 /etc/alpineform-ci"
