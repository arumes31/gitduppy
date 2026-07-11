#!/bin/sh
set -e

# The application's writable paths, matching config.yaml's storage.* settings
# (base_path, ssh_path, backup_path) and the compose bind mounts. These live
# under /app/storage so the root filesystem can be mounted read-only in
# production — only the bind mounts and /tmp need to be writable.
STORAGE_DIRS="/app/storage/repos /app/storage/ssh /app/storage/backups"

# Ensure the bind-mounted directories exist (no-op when compose created them).
mkdir -p $STORAGE_DIRS

if [ "$(id -u)" = "0" ]; then
	# Started as root (the default). The bind-mounted volumes arrive with arbitrary
	# host-side ownership, so make them writable by the appuser, then drop privileges
	# to that unprivileged user to run the server. This root -> appuser drop is the
	# reason the image ships no hard USER directive (see Dockerfile) and is fully
	# compatible with `no-new-privileges` since it lowers privileges, never raises.
	#
	# Only recurse when the top-level dir is not already owned by appuser (UID
	# 1000): a chown -R across every mirrored repository on each boot would make
	# restarts scale with the size of the mirror store.
	for dir in $STORAGE_DIRS; do
		if [ "$(stat -c %u "$dir")" != "1000" ]; then
			chown -R appuser:appgroup "$dir"
		fi
	done
	exec su-exec appuser "$@"
fi

# Already running unprivileged (e.g. `docker run --user` or compose `user:`). We
# cannot chown as a non-root user, so trust the caller to have provisioned writable
# volumes and exec the command directly.
exec "$@"
