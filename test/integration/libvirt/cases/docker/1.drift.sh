run_remote "drift Docker daemon configuration and stop Compose project" \
  "printf '%s\n' '{\"log-driver\":\"local\"}' > /etc/docker/daemon.json && chmod 0600 /etc/docker/daemon.json && chmod 0700 /opt/alpineform-docker/smoke && chmod 0644 /opt/alpineform-docker/smoke/compose.yaml /opt/alpineform-docker/smoke/.env && docker compose --project-name smoke --project-directory /opt/alpineform-docker/smoke --file /opt/alpineform-docker/smoke/compose.yaml --env-file /opt/alpineform-docker/smoke/.env stop smoke"
docker_pid_before_drift="$(ssh_vm "pidof dockerd")"
