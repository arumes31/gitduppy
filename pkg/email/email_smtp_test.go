package email

import (
	"bufio"
	"errors"
	"mime"
	"net"
	"strings"
	"testing"

	"github.com/gitduppy/gitduppy/internal/config"
)

func TestBuildMessageRejectsHeaderInjection(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		to      []string
		subject string
	}{
		{"subject CRLF", "a@b.c", []string{"x@y.z"}, "Clone Failure: repo\r\nBcc: victim@evil.test"},
		{"subject LF", "a@b.c", []string{"x@y.z"}, "repo\nX-Injected: 1"},
		{"from CR", "a@b.c\rBcc: v@e.t", []string{"x@y.z"}, "s"},
		{"recipient LF", "a@b.c", []string{"x@y.z\nBcc: v@e.t"}, "s"},
	}
	for _, c := range cases {
		if _, err := buildMessage(c.from, c.to, c.subject, "body"); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestBuildMessageEncodesSubject(t *testing.T) {
	subject := "GitDuppy - Clone Failure: répo-ü"
	msg, err := buildMessage("a@b.c", []string{"x@y.z"}, subject, "<p>hi</p>")
	if err != nil {
		t.Fatal(err)
	}
	headers, _, _ := strings.Cut(msg, "\r\n\r\n")
	var got string
	for _, line := range strings.Split(headers, "\r\n") {
		if v, ok := strings.CutPrefix(line, "Subject: "); ok {
			got = v
		}
	}
	if !strings.HasPrefix(got, "=?utf-8?q?") {
		t.Fatalf("non-ASCII subject not Q-encoded: %q", got)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(got)
	if err != nil || decoded != subject {
		t.Errorf("decoded subject = %q (%v), want %q", decoded, err, subject)
	}
}

// fakeSMTP is a minimal plaintext SMTP server that never offers STARTTLS. It
// records whether a message was accepted (DATA completed).
func fakeSMTP(t *testing.T) (port int, delivered <-chan bool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	done := make(chan bool, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- false
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		write("220 fake ESMTP")
		inData, got := false, false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				done <- got
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData, got = false, true
					write("250 queued")
				}
				continue
			}
			switch cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0]); cmd {
			case "EHLO", "HELO":
				write("250-fake")
				write("250 8BITMIME")
			case "MAIL", "RCPT", "RSET", "NOOP":
				write("250 ok")
			case "DATA":
				inData = true
				write("354 go ahead")
			case "QUIT":
				write("221 bye")
				done <- got
				return
			default:
				write("502 unsupported")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, done
}

func TestSendEmailRequiresSTARTTLS(t *testing.T) {
	port, delivered := fakeSMTP(t)
	s := NewService(&config.EmailConfig{
		Enabled: true, SMTPHost: "127.0.0.1", SMTPPort: port, From: "a@b.c",
	})
	err := s.SendEmail([]string{"x@y.z"}, "subject", "body")
	if !errors.Is(err, ErrSTARTTLSUnavailable) {
		t.Fatalf("expected ErrSTARTTLSUnavailable, got %v", err)
	}
	if <-delivered {
		t.Error("message must not be delivered over plaintext without opt-in")
	}
}

func TestSendEmailAllowInsecureOptIn(t *testing.T) {
	port, delivered := fakeSMTP(t)
	s := NewService(&config.EmailConfig{
		Enabled: true, SMTPHost: "127.0.0.1", SMTPPort: port, From: "a@b.c", AllowInsecure: true,
	})
	if err := s.SendEmail([]string{"x@y.z"}, "subject", "body"); err != nil {
		t.Fatalf("send with allow_insecure: %v", err)
	}
	if !<-delivered {
		t.Error("message was not delivered with allow_insecure")
	}
}

func TestSendEmailRejectsInjectedSubjectBeforeConnecting(t *testing.T) {
	// Port 1 on localhost is closed; the header check must fail first.
	s := NewService(&config.EmailConfig{Enabled: true, SMTPHost: "127.0.0.1", SMTPPort: 1, From: "a@b.c"})
	err := s.SendEmail([]string{"x@y.z"}, "x\r\nBcc: v@e.t", "body")
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("expected header injection error, got %v", err)
	}
}
