assert_remote "hostname and timezone are converged" \
  "test \"\$(hostname)\" = apf-ci.alpineform.test && test \"\$(cat /etc/hostname)\" = apf-ci.alpineform.test && test \"\$(cat /etc/timezone)\" = Asia/Shanghai && cmp -s /etc/localtime /usr/share/zoneinfo/Asia/Shanghai"
assert_remote "timezone dependency is installed and tracked" \
  "apk info -e tzdata && grep -Fq 'packages.package[\\\"tzdata\\\"]' /var/lib/alpineform/state.json"
assert_remote "kernel module is loaded or built in and persisted" \
  "test -d /sys/module/loop && test \"\$(cat /etc/modules-load.d/alpineform-loop.conf)\" = loop"
assert_remote "sysctl runtime and persistence are converged" \
  "test \"\$(sysctl -n net.ipv4.ip_forward)\" = 1 && grep -Rqx 'net.ipv4.ip_forward = 1' /etc/sysctl.d/99-alpineform-net_ipv4_ip_forward-*.conf"
