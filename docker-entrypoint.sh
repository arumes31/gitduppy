#!/bin/sh
set -e

# Ensure bind-mounted directories exist
mkdir -p /app/repos /app/keys /app/backups

# Adjust ownership so they are writable by the appuser
chown -R appuser:appgroup /app/repos /app/keys /app/backups

# Switch to appuser and run the command
exec su-exec appuser "$@"
