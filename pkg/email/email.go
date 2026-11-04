package email

import (
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

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

// smtpTimeout bounds one whole SMTP session (dial, handshake and transfer) so
// a stalled server cannot hang the notifying goroutine.
const smtpTimeout = 30 * time.Second

// ErrSTARTTLSUnavailable is returned when the server on a non-465 port does not
// offer STARTTLS and email.allow_insecure is not set.
var ErrSTARTTLSUnavailable = errors.New("smtp server does not offer STARTTLS; refusing to send in plaintext (set email.allow_insecure to allow)")

// checkHeaderValue rejects CR/LF (and NUL) in a value that is written into a
// message header, which would otherwise let it inject extra headers or body.
func checkHeaderValue(field, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("email %s contains a line break", field)
	}
	return nil
}

// buildMessage assembles the RFC 5322 message, validating every header value at
// this boundary and MIME-encoding the subject (which carries repository names).
func buildMessage(from string, to []string, subject, body string) (string, error) {
	if err := checkHeaderValue("from", from); err != nil {
		return "", err
	}
	for _, addr := range to {
		if err := checkHeaderValue("recipient", addr); err != nil {
			return "", err
		}
	}
	if err := checkHeaderValue("subject", subject); err != nil {
		return "", err
	}
	return "From: " + from + "\r\n" +
		"To: " + strings.Join(to, ",") + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n\r\n" +
		body, nil
}

// SendEmail sends an email. Port 465 uses implicit TLS; any other port must
// upgrade with STARTTLS before authenticating or sending, unless
// email.allow_insecure explicitly permits a plaintext relay.
func (s *Service) SendEmail(to []string, subject, body string) error {
	if s.config == nil || !s.config.Enabled {
		return nil // Silently skip if email is disabled or not configured.
	}

	msg, err := buildMessage(s.config.From, to, subject, body)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.config.SMTPHost, strconv.Itoa(s.config.SMTPPort))
	tlsConfig := &tls.Config{
		ServerName: s.config.SMTPHost,
		MinVersion: tls.VersionTLS12,
	}
	dialer := &net.Dialer{Timeout: smtpTimeout}

	var conn net.Conn
	if s.config.SMTPPort == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig) //nolint:noctx
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(smtpTimeout))

	client, err := smtp.NewClient(conn, s.config.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()

	if s.config.SMTPPort != 465 {
		// Require STARTTLS: unlike smtp.SendMail, never silently continue in
		// plaintext when the server does not advertise the extension.
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err = client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("smtp STARTTLS: %w", err)
			}
		} else if !s.config.AllowInsecure {
			return ErrSTARTTLSUnavailable
		}
	}

	return s.deliver(client, to, msg)
}

// deliver authenticates (when credentials are configured) and transfers msg on
// an already-secured client. smtp.PlainAuth itself refuses to send credentials
// over an unencrypted connection to a non-localhost server.
func (s *Service) deliver(client *smtp.Client, to []string, msg string) error {
	var err error
	if s.config.SMTPUser != "" {
		auth := smtp.PlainAuth("", s.config.SMTPUser, s.config.SMTPPassword, s.config.SMTPHost)
		if err = client.Auth(auth); err != nil {
			return err
		}
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
	Repository any
	CloneJob   any
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
