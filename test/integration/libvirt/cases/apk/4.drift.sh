run_remote "add an undeclared repository under authoritative ownership" \
  "printf '%s\n' 'https://dl-cdn.alpinelinux.org/alpine/v3.24/community' >> /etc/apk/repositories"
