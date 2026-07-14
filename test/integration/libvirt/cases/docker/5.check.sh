assert_remote "explicitly absent Docker packages are removed from world" \
  "! apk info -e docker && ! apk info -e docker-cli-compose && ! grep -qx docker /etc/apk/world && ! grep -qx docker-cli-compose /etc/apk/world"
assert_remote "explicitly absent Docker service and configuration are gone" \
  "test ! -e /etc/init.d/docker && test ! -e /etc/docker/daemon.json && test ! -e /etc/runlevels/default/docker"
assert_remote "explicitly absent Docker membership is removed" \
  "! awk -F: '\$1 == \"docker\" && (\",\" \$4 \",\") ~ /,operator,/' /etc/group | grep -q ."
