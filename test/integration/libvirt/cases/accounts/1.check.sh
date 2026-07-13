assert_remote "managed group identity is converged" \
  "test \"\$(getent group apfci | cut -d: -f3)\" = 2300"
assert_remote "managed user identity is converged" \
  "entry=\$(getent passwd apfci); test \"\$(printf '%s' \"\$entry\" | cut -d: -f3)\" = 2300 && test \"\$(printf '%s' \"\$entry\" | cut -d: -f4)\" = 2300 && test \"\$(printf '%s' \"\$entry\" | cut -d: -f6)\" = /home/apfci && test \"\$(printf '%s' \"\$entry\" | cut -d: -f7)\" = /bin/sh"
assert_remote "supplementary membership and authorized key are converged" \
  "id -nG apfci | tr ' ' '\n' | grep -qx wheel && grep -Fq 'AAAAC3NzaC1lZDI1NTE5AAAAIAaCeDgwFMdvRLHkB+Muja0bVQu1dxcrqB8tdD3o08Wl' /home/apfci/.ssh/authorized_keys && test \"\$(stat -c %a /home/apfci/.ssh)\" = 700 && test \"\$(stat -c %a /home/apfci/.ssh/authorized_keys)\" = 600"
