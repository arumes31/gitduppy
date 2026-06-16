# Email Templates

This directory contains HTML email templates used by the email notification system.

## Available Templates

- `clone_failure.html` - Sent when a repository clone fails
- `system_error.html` - Sent when a critical system error occurs

## Template Variables

All templates support the following variables:

- `{{.AppName}}` - Application name
- `{{.Repository}}` - Repository object (for clone failure)
- `{{.Error}}` - Error message
- `{{.Timestamp}}` - Timestamp of the event
- `{{.BaseURL}}` - Base URL of the application
- `{{.AdminEmail}}` - Admin email address