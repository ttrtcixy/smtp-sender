[Russian](./README.ru.md)

Is a small SMTP client for sending emails with verification codes.

It provides a simple interface for sending emails through SMTP servers with TLS support, automatic reconnection on connection failures, and configurable timeouts.

## How it works

- Configure SMTP connection settings using `SMTPConfig` struct
- Create the client with `smtpsender.New(cfg, log)`
- Use `Send(ctx, data)` to send emails with JSON payload containing recipient, code, and expiration
- Optionally use `Ping(ctx)` to test connection
- Call `Close(ctx)` to gracefully shutdown the client

The client handles automatic reconnection on connection failures and supports context-based timeouts and cancellation.

## Example

```go
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	smtpsender "github.com/ttrtcixy/smtp-sender"
)

func main() {
	ctx := context.Background()

	cfg := &smtpsender.SMTPConfig{
		Host:              "smtp.example.com",
		Port:              "587",
		Sender:            "noreply@example.com",
		Password:          "your-password",
		ConnectTimeout:    10 * time.Second,
		ConnectKeepAlive:  30 * time.Second,
		InsecureSkipVerify: false,
	}

	client := smtpsender.New(cfg, slog.Default())
	defer client.Close(ctx)

	// Test connection
	if err := client.Ping(ctx); err != nil {
		panic(err)
	}

	// Prepare email payload
	payload := map[string]interface{}{
		"to":      "user@example.com",
		"code":    "123456",
		"expires": time.Now().Add(15 * time.Minute),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}

	// Send email
	if err := client.Send(ctx, data); err != nil {
		panic(err)
	}
}
```

## Configuration

The `SMTPConfig` struct supports the following fields:

- `Host`: SMTP server hostname (required)
- `Port`: SMTP server port (default: "25")
- `Sender`: Email address used as sender (required)
- `Password`: Password for SMTP authentication (required)
- `ConnectTimeout`: Timeout for establishing connection (required)
- `ConnectKeepAlive: Keep-alive interval for connections (required)
- `InsecureSkipVerify`: Skip TLS certificate verification (default: false)

## Email Payload Format

The `Send` method expects a JSON payload with the following structure:

```json
{
  "to": "recipient@example.com",
  "code": "123456",
  "expires": "2023-12-31T23:59:59Z"
}
```

## Error Handling

The client automatically handles common connection issues:
- Automatic reconnection on connection failures
- Retry logic for temporary failures (up to 2 attempts)
- Context-based cancellation and timeout support
- Graceful shutdown with `Close()`
