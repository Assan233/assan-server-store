# SQLite for single-instance deployment

Assan is deployed as one API container on a personal NAS, so we use SQLite on a persistent Docker volume instead of operating a separate database service. This keeps deployment and backups simple; running multiple API replicas or requiring concurrent multi-host access would be a reason to revisit this choice.
