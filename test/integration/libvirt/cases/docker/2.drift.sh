run_remote "start the Compose project outside AlpineForm" \
  "docker compose --project-name smoke --project-directory /opt/alpineform-docker/smoke --file /opt/alpineform-docker/smoke/compose.yaml --env-file /opt/alpineform-docker/smoke/.env start"
