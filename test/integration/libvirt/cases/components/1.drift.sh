run_remote "drift binary, archive tree, and both shared-script triggers" \
  "printf drift > /usr/local/bin/apf-ci-tool && printf 'enabled=false\n' > /etc/apf-ci-component.conf && printf unmanaged > /opt/apf-ci-bundle/unmanaged"
