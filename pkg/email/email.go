package email

import (
	"crypto/tls"
	"fmt"
	"html/template"
	"net/smtp"
	"strings"

	"github.com/gitduppy/gitduppy/internal/config"
)

// EmailService handles email sending
type EmailService struct {
	config *config.EmailConfig
}

// NewEmailService creates a new email service
func NewEmailService(cfg *config.EmailConfig) *EmailService {
	return &EmailService{
		config: cfg,
	}
}

// IsEnabled returns true if email is enabled in config
func (s *EmailService) IsEnabled() bool {
	return s.config.Enabled
}

// SendEmail sends an email
func (s *EmailService) SendEmail(to []string, subject, body string) error {
	if !s.config.Enabled {
		return nil // Silently skip if email is disabled
	}

	auth := smtp.PlainAuth("", s.config.SMTPUser, s.config.SMTPPassword, s.config.SMTPHost)

	// Create message
	msg := "From: " + s.config.From + "\r\n" +
		"To: " + strings.Join(to, ",") + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n\r\n" +
		body

	// Connect to SMTP server
	var err error
	if s.config.SMTPPort == 465 {
		// SSL/TLS connection
		tlsConfig := &tls.Config{
			ServerName: s.config.SMTPHost,
		}
		conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort), tlsConfig)
		if err != nil {
			return err
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, s.config.SMTPHost)
		if err != nil {
			return err
		}
		defer client.Close()

		if err = client.Auth(auth); err != nil {
			return err
		}
		if err = client.Mail(s.config.From); err != nil {
			return err
		}
		for _, addr := range to {
			if err = client.Rcpt(addr); err != nil {
				return err
			}
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(msg))
		if err != nil {
			return err
		}
		err = w.Close()
		if err != nil {
			return err
		}
		err = client.Quit()
		if err != nil {
			return err
		}
	} else {
		// STARTTLS connection
		err = smtp.SendMail(
			fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort),
			auth,
			s.config.From,
			to,
			[]byte(msg),
		)
	}

	return err
}

// TemplateData represents data for email templates
type TemplateData struct {
	AppName    string
	Repository interface{}
	CloneJob   interface{}
	Error      string
	Timestamp  string
	AdminEmail string
	BaseURL    string
}

// RenderTemplate renders an email template
func (s *EmailService) RenderTemplate(templateName string, data TemplateData) (string, error) {
	// In a real implementation, this would load templates from files
	// For now, we'll use inline templates
	switch templateName {
	case "clone_failure":
		return s.renderCloneFailureTemplate(data)
	case "system_error":
		return s.renderSystemErrorTemplate(data)
	default:
		return "", fmt.Errorf("unknown template: %s", templateName)
	}
}

func (s *EmailService) renderCloneFailureTemplate(data TemplateData) (string, error) {
	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>{{.AppName}} - Clone Failure</title>
</head>
<body>
    <h2>Clone Failure Notification</h2>
    <p>A repository clone has failed.</p>
    <ul>
        <li><strong>Repository:</strong> {{if .Repository}}{{.Repository.Name}}{{end}}</li>
        <li><strong>URL:</strong> {{if .Repository}}{{.Repository.URL}}{{end}}</li>
        <li><strong>Error:</strong> {{.Error}}</li>
        <li><strong>Time:</strong> {{.Timestamp}}</li>
    </ul>
    <p>Please check the repository configuration and try again.</p>
    <p><a href="{{.BaseURL}}">View Dashboard</a></p>
</body>
</html>
`
	t, err := template.New("clone_failure").Parse(tmpl)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *EmailService) renderSystemErrorTemplate(data TemplateData) (string, error) {
	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>{{.AppName}} - System Error</title>
</head>
<body>
    <h2>System Error Notification</h2>
    <p>A critical system error has occurred.</p>
    <ul>
        <li><strong>Error:</strong> {{.Error}}</li>
        <li><strong>Time:</strong> {{.Timestamp}}</li>
    </ul>
    <p>Please check the system logs and configuration.</p>
    <p><a href="{{.BaseURL}}">View Dashboard</a></p>
</body>
</html>
`
	t, err := template.New("system_error").Parse(tmpl)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
