#!/bin/sh
set -e

# Wait for PostgreSQL to be ready
until pg_isready -h postgres -U gitduppy -d gitduppy; do
  echo "Waiting for PostgreSQL to become available..."
  sleep 2
done

echo "PostgreSQL is ready!"

# Run database migrations (assuming your app has a migrate command)
# If your app doesn't have a specific migrate command, you might need to implement this
if [ -f ./gitduppy ]; then
  echo "Running database migrations..."
  # Replace this with your actual migration command
  # ./gitduppy migrate up
  echo "Migrations completed (placeholder - implement actual migration)"
fi

# Create default admin user if needed (optional - implement as needed)
# ./gitduppy create-admin --email admin@example.com --password admin123

# Start the application
exec "$@"