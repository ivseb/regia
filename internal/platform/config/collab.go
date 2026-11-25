package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Collab è la configurazione di allegati ed email delle notifiche (piano, passo 3).
type Collab struct {
	// MaxUploadMB è il limite di un allegato in MB (REGIA_MAX_UPLOAD_MB, default 25).
	MaxUploadMB int
	// SMTP: con host e mittente impostati il worker invia le notifiche per email.
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	// MailInterval è la finestra di raggruppamento delle email (REGIA_MAIL_INTERVAL, default 3m).
	MailInterval time.Duration
	// BaseURL è l'indirizzo pubblico dell'app, per i link nelle email (REGIA_BASE_URL).
	BaseURL string
}

func loadCollab() (Collab, error) {
	c := Collab{
		MaxUploadMB:  25,
		SMTPHost:     os.Getenv("REGIA_SMTP_HOST"),
		SMTPPort:     587,
		SMTPUser:     os.Getenv("REGIA_SMTP_USER"),
		SMTPPassword: os.Getenv("REGIA_SMTP_PASSWORD"),
		SMTPFrom:     os.Getenv("REGIA_SMTP_FROM"),
		MailInterval: envDuration("REGIA_MAIL_INTERVAL", 3*time.Minute),
		BaseURL:      os.Getenv("REGIA_BASE_URL"),
	}
	if v := os.Getenv("REGIA_MAX_UPLOAD_MB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 4096 {
			return c, fmt.Errorf("REGIA_MAX_UPLOAD_MB non valido: %q (da 1 a 4096)", v)
		}
		c.MaxUploadMB = n
	}
	if v := os.Getenv("REGIA_SMTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("REGIA_SMTP_PORT non valido: %q", v)
		}
		c.SMTPPort = n
	}
	if c.MailInterval < 10*time.Second {
		return c, fmt.Errorf("REGIA_MAIL_INTERVAL troppo breve: %s (minimo 10s)", c.MailInterval)
	}
	return c, nil
}
