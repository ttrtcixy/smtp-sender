package smtpsender

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"net/smtp"
)

var (
	ErrClientClosed   = errors.New("client is closed")
	ErrNeedConnection = errors.New("need connection")
)

type SMTPConfig struct {
	ConnectTimeout   time.Duration `env:"SMTP_CONNECT_TIMEOUT,required"`
	ConnectKeepAlive time.Duration `env:"SMTP_CONNECT_KEEP_ALIVE,required"`

	InsecureSkipVerify bool `env:"SMTP_INSECURE_SKIP_VERIFY" envDefault:"false"`

	Host     string `env:"SMTP_HOST,required"`
	Port     string `env:"SMTP_PORT"              envDefault:"25"`
	Sender   string `env:"SMTP_SENDER,required"`
	Password string `env:"SMTP_PASSWORD,required"`
	Addr     string
}

type SmtpClient struct {
	cfg *SMTPConfig
	log *slog.Logger

	// settings
	tlsCfg *tls.Config
	auth   smtp.Auth

	// state
	conn   net.Conn
	client *smtp.Client

	// sync
	mu     sync.Mutex // protects the state from a race between the consumer (Send) and graceful shutdown (Close)
	closed bool
}

func New(cfg *SMTPConfig, log *slog.Logger) (client *SmtpClient) {
	//const op = "smtpsender.New"

	cfg.Addr = net.JoinHostPort(cfg.Host, cfg.Port)

	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify, ServerName: cfg.Host}

	client = &SmtpClient{
		cfg:    cfg,
		log:    log,
		tlsCfg: tlsCfg,
		auth:   smtp.PlainAuth("", cfg.Sender, cfg.Password, cfg.Host),
	}

	//ctx := context.Background()
	//
	//if err = client.connect(ctx); err != nil {
	//	return nil, fmt.Errorf("%s -> %w", op, err)
	//}

	return client
}

func (c *SmtpClient) Ping(ctx context.Context) error {
	if err := c.connect(ctx); err != nil {
		return err
	}
	return c.Close(ctx)
}

func (c *SmtpClient) Close(_ context.Context) error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true

	if c.client == nil {
		return nil
	}

	if err := c.client.Close(); err != nil {
		return err
	}

	return nil
}

func (c *SmtpClient) dial(ctx context.Context) (net.Conn, error) {
	const op = "smtpsender.dial"

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{
			Timeout:   c.cfg.ConnectTimeout,
			KeepAlive: c.cfg.ConnectKeepAlive,
		},
		Config: c.tlsCfg,
	}

	conn, err := dialer.DialContext(ctx, "tcp", c.cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("%s - error connecting to smtp server -> %w", op, err)
	}

	return conn, nil
}

func (c *SmtpClient) connect(ctx context.Context) (err error) {
	const op = "smtpsender.connect"

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClientClosed
	}

	if c.client != nil {
		_ = c.client.Close()
	}

	// create s tsl connection, without checking whether this connection is possible
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("%s - error connecting to smtp server -> %w", op, err)
	}

	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()

	c.conn = conn

	client, err := smtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		return fmt.Errorf("%s - error creating smtp client -> %w", op, err)
	}

	defer func() {
		if err != nil {
			_ = client.Close()
		}
	}()

	if err = client.Auth(c.auth); err != nil {
		return fmt.Errorf("%s - error authenticating -> %w", op, err)
	}

	c.client = client

	return nil
}

//const message = "From: %s\nTo: %s\nSubject: Hello\n\nCode: %s\n"

// todo возможны проблемы, если мы отправляем сообщение, ловим ошибку, в этот момент есть окно для вызова Close() переподключение тогда не сработает, хотя возможно стоит это делать

// Send - send message to smtp server with deadline from context
func (c *SmtpClient) Send(ctx context.Context, data []byte) (err error) {
	const op = "smtpsender.Send"

	payload, err := c.parseData(data)
	if err != nil {
		return fmt.Errorf("%s - failed to parse email payload -> %w", op, err)
	}

	if err = ctx.Err(); err != nil {
		return fmt.Errorf("%s - ctx done -> %w", op, err)
	}

	for attempts := 2; attempts > 0; attempts-- {
		err = c.sendToSmtpServer(ctx, payload) // todo ловить разные ошибки, по типу лимит писем и подобное.
		if nil == err {
			return nil
		}

		if vErr := c.validState(ctx, err); vErr != nil {
			return vErr
		}

		continue
	}

	return fmt.Errorf("%s - failed to send code", op)
}

func (c *SmtpClient) validState(ctx context.Context, err error) error {
	const op = "smtpsender.validState"

	// if client is closed
	if errors.Is(err, ErrClientClosed) {
		return fmt.Errorf("%s - send message error, client is closed -> %w", op, err)
	}

	// request ctx error
	if errors.Is(err, os.ErrDeadlineExceeded) {
		c.closeClient()

		return fmt.Errorf("%s -> %w", op, err)
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s -> %w", op, err)
	}

	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, ErrNeedConnection) {
		if err := c.connect(ctx); err != nil {
			c.log.LogAttrs(ctx, slog.LevelError, "failed to connect to smtp server", slog.String("error", err.Error()))

			return fmt.Errorf("%s - error sending code -> %w", op, err)
		}
		return nil
	}

	// If we couldn’t determine the type of error, we try to check the connection.
	if err = c.client.Noop(); err != nil {
		// reconnect if connection is broken
		if err := c.connect(ctx); err != nil {
			c.log.LogAttrs(
				ctx,
				slog.LevelError,
				"failed to reconnect to smtp server",
				slog.String("error", err.Error()),
			)
			// return reconnect error
			return fmt.Errorf("%s - error sending code -> %w", op, err)
		}
		// if connection is reconnected, return nil to retry sending message
		return nil
	}

	// just an unsuccessful sending, maybe the message is broken or something else.
	return fmt.Errorf("%s - failed to send code -> %w", op, err)
}

func (c *SmtpClient) closeClient() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		_ = c.client.Close()
		c.client = nil
	}
}

type EmailPayload struct {
	To      string    `json:"to"`
	Code    string    `json:"code"`
	Expires time.Time `json:"expires"`
}

func (c *SmtpClient) parseData(data []byte) (*EmailPayload, error) {
	var payload EmailPayload

	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal email payload: %w", err)
	}

	return &payload, nil
}

// send message to smtp server with deadline from context
func (c *SmtpClient) sendToSmtpServer(ctx context.Context, payload *EmailPayload) (err error) {
	const op = "smtpsender.sendToSmtpServer"

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrClientClosed
	}

	if c.client == nil {
		return ErrNeedConnection
	}

	if err = ctx.Err(); err != nil {
		return fmt.Errorf("%s - send error, context is done -> %w", op, err)
	}

	// if ctx have deadline - add it to connection
	if t, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(t); err != nil { // todo может логировать ошибку? и продолжать работу?
			return fmt.Errorf("%s - failed to set deadline -> %w", op, err)
		} else {
			defer func() {
				_ = c.conn.SetDeadline(time.Time{})
			}()
		}
	}

	defer func() {
		if err != nil { // todo возможны проблемы если это ошибки подключения
			_ = c.client.Reset()
		}
	}()

	if err = c.client.Mail(c.cfg.Sender); err != nil {
		return fmt.Errorf("%s -> %w", op, err)
	}

	if err = c.client.Rcpt(payload.To); err != nil {
		return fmt.Errorf("%s -> %w", op, err)
	}

	wc, err := c.client.Data()
	if err != nil {
		return fmt.Errorf("%s -> %w", op, err)
	}

	_, err = fmt.Fprintf(
		wc,
		"From: %s\r\nTo: %s\r\nSubject: Hello\r\n\r\nCode: %s\r\n",
		c.cfg.Sender,
		payload.To,
		payload.Code,
	)
	if err != nil {
		_ = wc.Close()
		return fmt.Errorf("%s - write payload error -> %w", op, err)
	}

	_ = wc.Close()

	return nil
}
