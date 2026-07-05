package email

import (
	"crypto/tls"
	"fmt"
	"html/template"
	"net/smtp"
	"strings"

	"github.com/gitduppy/gitduppy/internal/config"
)

// Service handles email sending.
type Service struct {
	config *config.EmailConfig
}

// NewService creates a new email service.
func NewService(cfg *config.EmailConfig) *Service {
	return &Service{
		config: cfg,
	}
}

// IsEnabled returns true if email is enabled in config.
func (s *Service) IsEnabled() bool {
	return s.config != nil && s.config.Enabled
}

// SendEmail sends an email.
func (s *Service) SendEmail(to []string, subject, body string) error {
	if s.config == nil || !s.config.Enabled {
		return nil // Silently skip if email is disabled or not configured.
	}

	auth := smtp.PlainAuth("", s.config.SMTPUser, s.config.SMTPPassword, s.config.SMTPHost)

	// Create message.
	msg := "From: " + s.config.From + "\r\n" +
		"To: " + strings.Join(to, ",") + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n\r\n" +
		body

	// Connect to SMTP server.
	if s.config.SMTPPort == 465 {
		return s.sendEmailSSL(to, auth, msg)
	}

	// STARTTLS connection.
	return smtp.SendMail(
		fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort),
		auth,
		s.config.From,
		to,
		[]byte(msg),
	)
}

func (s *Service) sendEmailSSL(to []string, auth smtp.Auth, msg string) error {
	// SSL/TLS connection.
	tlsConfig := &tls.Config{
		ServerName: s.config.SMTPHost,
		MinVersion: tls.VersionTLS12,
	}
	conn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort), tlsConfig) //nolint:noctx
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
	if err = w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// TemplateData represents data for email templates.
type TemplateData struct {
	AppName    string
	Repository interface{}
	CloneJob   interface{}
	Error      string
	Timestamp  string
	AdminEmail string
	BaseURL    string
}

// RenderTemplate renders an email template.
func (s *Service) RenderTemplate(templateName string, data TemplateData) (string, error) {
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

func (s *Service) renderCloneFailureTemplate(data TemplateData) (string, error) {
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

func (s *Service) renderSystemErrorTemplate(data TemplateData) (string, error) {
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
