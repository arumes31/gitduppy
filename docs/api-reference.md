# API Reference

GitDuppy provides a RESTful API for managing repositories, users, and configurations. All endpoints return JSON responses and require authentication unless otherwise specified.

## Authentication

### POST /api/v1/auth/login
Authenticate and receive a JWT token.

**Request:**
```json
{
  "email": "user@example.com",
  "password": "yourpassword"
}
```

**Response (200 OK):**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "user": {
    "id": 1,
    "email": "user@example.com",
    "role": "admin",
    "created_at": "2026-06-15T20:20:34Z"
  }
}
```

**Example with curl:**
```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email": "admin@example.com", "password": "yourpassword"}'
```

### POST /api/v1/auth/logout
Invalidate the current session.

**Headers:**
- `Authorization: Bearer <token>`

**Example with curl:**
```bash
curl -X POST http://localhost:8080/api/v1/auth/logout \
  -H "Authorization: Bearer your-jwt-token-here"
```

## Repositories

### GET /api/v1/repositories
List all repositories.

**Headers:**
- `Authorization: Bearer <token>`

**Query Parameters:**
- `page`: Page number (default: 1)
- `per_page`: Items per page (default: 20, max: 100)
- `search`: Search term to filter repositories
- `tag`: Filter by tag name

**Example with curl:**
```bash
curl -X GET "http://localhost:8080/api/v1/repositories?page=1&per_page=10" \
  -H "Authorization: Bearer your-jwt-token-here"
```

### GET /api/v1/repositories/{id}
Get details of a specific repository.

**Headers:**
- `Authorization: Bearer <token>`

**Example with curl:**
```bash
curl -X GET http://localhost:8080/api/v1/repositories/1 \
  -H "Authorization: Bearer your-jwt-token-here"
```

### POST /api/v1/repositories
Create a new repository.

**Headers:**
- `Authorization: Bearer <token>`
- `Content-Type: application/json`

**Request Body:**
```json
{
  "source_url": "https://github.com/user/repo.git",
  "destination_path": "/path/to/local/mirror",
  "schedule": "0 */6 * * *", // cron format
  "ssh_key": "-----BEGIN RSA PRIVATE KEY-----\n...\n-----END RSA PRIVATE KEY-----",
  "webhook_url": "https://example.com/webhook",
  "tags": ["production", "critical"]
}
```

**Example with curl:**
```bash
curl -X POST http://localhost:8080/api/v1/repositories \
  -H "Authorization: Bearer your-jwt-token-here" \
  -H "Content-Type: application/json" \
  -d '{"source_url": "https://github.com/user/repo.git", "destination_path": "/mirrors/repo", "schedule": "0 */6 * * *"}'
```

### PUT /api/v1/repositories/{id}
Update an existing repository.

**Headers:**
- `Authorization: Bearer <token>`
- `Content-Type: application/json`

**Request Body:** (same structure as POST, but only fields to update)

**Example with curl:**
```bash
curl -X PUT http://localhost:8080/api/v1/repositories/1 \
  -H "Authorization: Bearer your-jwt-token-here" \
  -H "Content-Type: application/json" \
  -d '{"schedule": "0 */12 * * *"}'
```

### DELETE /api/v1/repositories/{id}
Delete a repository.

**Headers:**
- `Authorization: Bearer <token>`

**Example with curl:**
```bash
curl -X DELETE http://localhost:8080/api/v1/repositories/1 \
  -H "Authorization: Bearer your-jwt-token-here"
```

## Clone Jobs

### GET /api/v1/clone-jobs
List all clone jobs.

**Headers:**
- `Authorization: Bearer <token>`

**Query Parameters:**
- `repository_id`: Filter by repository ID
- `status`: Filter by status (pending, running, completed, failed)
- `page`, `per_page`: Pagination

**Example with curl:**
```bash
curl -X GET "http://localhost:8080/api/v1/clone-jobs?status=failed" \
  -H "Authorization: Bearer your-jwt-token-here"
```

### POST /api/v1/clone-jobs
Trigger a manual clone job.

**Headers:**
- `Authorization: Bearer <token>`
- `Content-Type: application/json`

**Request Body:**
```json
{
  "repository_id": 1
}
```

**Example with curl:**
```bash
curl -X POST http://localhost:8080/api/v1/clone-jobs \
  -H "Authorization: Bearer your-jwt-token-here" \
  -H "Content-Type: application/json" \
  -d '{"repository_id": 1}'
```

## Users

### GET /api/v1/users
List all users (admin only).

**Headers:**
- `Authorization: Bearer <token>`

**Example with curl:**
```bash
curl -X GET http://localhost:8080/api/v1/users \
  -H "Authorization: Bearer your-jwt-token-here"
```

### POST /api/v1/users
Create a new user (admin only).

**Headers:**
- `Authorization: Bearer <token>`
- `Content-Type: application/json`

**Request Body:**
```json
{
  "email": "newuser@example.com",
  "password": "securepassword123",
  "role": "user" // or "admin"
}
```

**Example with curl:**
```bash
curl -X POST http://localhost:8080/api/v1/users \
  -H "Authorization: Bearer your-jwt-token-here" \
  -H "Content-Type: application/json" \
  -d '{"email": "newuser@example.com", "password": "securepassword123", "role": "user"}'
```

## Health Check

### GET /api/v1/health
Get system health status.

**No authentication required.**

**Example with curl:**
```bash
curl -X GET http://localhost:8080/api/v1/health
```

**Response (200 OK):**
```json
{
  "status": "healthy",
  "database": "connected",
  "version": "1.0.0",
  "uptime": "2h34m12s"
}
```

## Error Responses

All error responses follow this format:
```json
{
  "error": "error message",
  "code": "ERROR_CODE"
}
```

Common HTTP status codes:
- 400 Bad Request: Invalid request parameters
- 401 Unauthorized: Missing or invalid authentication
- 403 Forbidden: Insufficient permissions
- 404 Not Found: Resource not found
- 500 Internal Server Error: Server error