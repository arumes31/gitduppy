# Deployment Guide

GitDuppy can be deployed in various environments, from simple Docker setups to complex Kubernetes clusters. This guide covers the different deployment options.

## Docker Deployment

The simplest way to deploy GitDuppy is using Docker Compose.

### Basic Deployment
```bash
# Create a .env file with your configuration
cp .env.example .env
# Edit .env with your actual values (database URL, secrets, etc.)

# Start the services
docker-compose up -d
```

### Production Deployment
For production, use the provided `docker-compose.prod.yml` which includes Caddy as a reverse proxy with automatic HTTPS:

```bash
# Create production .env file
cp .env.example .env.prod
# Edit .env.prod with production values

# Start production services
docker-compose -f docker-compose.prod.yml --env-file .env.prod up -d
```

### Custom Configuration
You can override the default configuration by creating a `config.yaml` file and mounting it into the container:

```yaml
# docker-compose.override.yml
version: '3.8'
services:
  gitduppy:
    volumes:
      - ./config.yaml:/app/config.yaml
    environment:
      - CONFIG_FILE=/app/config.yaml
```

## Kubernetes Deployment

GitDuppy can be deployed to Kubernetes using the provided Helm chart or manifests.

### Using Helm Chart
```bash
# Add the GitDuppy Helm repository
helm repo add gitduppy https://charts.gitduppy.example.com
helm repo update

# Install GitDuppy
helm install gitduppy gitduppy/gitduppy \
  --set database.url="postgres://user:password@postgres:5432/gitduppy" \
  --set security.jwtSecret="your-jwt-secret" \
  --set security.encryptionKey="your-32-byte-key"
```

### Using Manifests
Create the necessary Kubernetes manifests:

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gitduppy
spec:
  replicas: 2
  selector:
    matchLabels:
      app: gitduppy
  template:
    metadata:
      labels:
        app: gitduppy
    spec:
      containers:
      - name: gitduppy
        image: gitduppy/gitduppy:latest
        ports:
        - containerPort: 8080
        envFrom:
        - secretRef:
            name: gitduppy-secrets
        - configMapRef:
            name: gitduppy-config
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: gitduppy-data
---
# service.yaml
apiVersion: v1
kind: Service
metadata:
  name: gitduppy
spec:
  selector:
    app: gitduppy
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
  type: ClusterIP
```

## Bare Metal Deployment

For bare metal deployment, you can build and run GitDuppy directly on your server.

### Building from Source
```bash
# Clone the repository
git clone https://github.com/yourorg/gitduppy.git
cd gitduppy

# Build the binary
go build -o gitduppy cmd/server/main.go

# Create configuration file
cp config.example.yaml config.yaml
# Edit config.yaml with your settings

# Run the server
./gitduppy
```

### Systemd Service
Create a systemd service file for automatic startup:

```ini
# /etc/systemd/system/gitduppy.service
[Unit]
Description=GitDuppy Server
After=network.target

[Service]
Type=simple
User=gitduppy
Group=gitduppy
WorkingDirectory=/opt/gitduppy
ExecStart=/opt/gitduppy/gitduppy
Restart=always
EnvironmentFile=/etc/gitduppy.env

[Install]
WantedBy=multi-user.target
```

Then enable and start the service:
```bash
sudo systemctl daemon-reload
sudo systemctl enable gitduppy
sudo systemctl start gitduppy
```

## Database Setup

GitDuppy requires PostgreSQL 12 or higher. You can set up the database in several ways:

### Using Docker
```bash
# Start PostgreSQL container
docker run -d \
  --name gitduppy-db \
  -e POSTGRES_DB=gitduppy \
  -e POSTGRES_USER=gitduppy \
  -e POSTGRES_PASSWORD=yourpassword \
  -v gitduppy-db-data:/var/lib/postgresql/data \
  postgres:14
```

### Using Managed Database
For production, consider using a managed database service like AWS RDS, Google Cloud SQL, or Azure Database for PostgreSQL.

Database connection string format:
```
postgres://username:password@host:port/database?sslmode=require
```

## Reverse Proxy Setup

For production deployments, it's recommended to use a reverse proxy like Nginx or Caddy.

### Caddy Configuration
```caddyfile
# Caddyfile
gitduppy.example.com {
    reverse_proxy localhost:8080
    
    # Enable automatic HTTPS
    tls admin@example.com
    
    # Security headers
    header {
        X-Content-Type-Options nosniff
        X-Frame-Options DENY
        Referrer-Policy no-referrer-when-downgrade
    }
}
```

### Nginx Configuration
```nginx
server {
    listen 80;
    server_name gitduppy.example.com;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name gitduppy.example.com;
    
    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;
    
    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## Monitoring and Logging

Set up monitoring and logging for production deployments:

### Health Checks
GitDuppy provides a health check endpoint at `/api/v1/health` that returns JSON status information.

### Log Collection
Configure log collection using tools like:
- ELK Stack (Elasticsearch, Logstash, Kibana)
- Loki + Grafana
- Splunk
- Datadog

### Metrics
GitDuppy exposes Prometheus metrics at `/metrics` endpoint when enabled in configuration.