[English](./README.md)

Небольшой SMTP клиент для отправки писем с кодами подтверждения.

Он предоставляет простой интерфейс для отправки писем через SMTP серверы с поддержкой TLS, автоматическим переподключением при сбоях соединения и настраиваемыми таймаутами.

## Как это работает

- Настройте параметры подключения SMTP с помощью структуры `SMTPConfig`
- Создайте клиент через `smtpsender.New(cfg, log)`
- Используйте `Send(ctx, data)` для отправки писем с JSON-полезной нагрузкой, содержащей получателя, код и срок действия
- Опционально используйте `Ping(ctx)` для проверки соединения
- Вызовите `Close(ctx)` для корректного завершения работы клиента

Клиент автоматически обрабатывает переподключение при сбоях соединения и поддерживает таймауты и отмену на основе контекста.

## Пример

```go
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	smtpsender "github.com/ttrtcixy/smtpsender"
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

	// Проверка соединения
	if err := client.Ping(ctx); err != nil {
		panic(err)
	}

	// Подготовка полезной нагрузки письма
	payload := map[string]interface{}{
		"to":      "user@example.com",
		"code":    "123456",
		"expires": time.Now().Add(15 * time.Minute),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}

	// Отправка письма
	if err := client.Send(ctx, data); err != nil {
		panic(err)
	}
}
```

## Конфигурация

Структура `SMTPConfig` поддерживает следующие поля:

- `Host`: Имя хоста SMTP сервера (обязательно)
- `Port`: Порт SMTP сервера (по умолчанию: "25")
- `Sender`: Email адрес отправителя (обязательно)
- `Password`: Пароль для SMTP аутентификации (обязательно)
- `ConnectTimeout`: Таймаут установки соединения (обязательно)
- `ConnectKeepAlive`: Интервал keep-alive для соединений (обязательно)
- `InsecureSkipVerify`: Пропустить проверку TLS сертификата (по умолчанию: false)

## Формат полезной нагрузки письма

Метод `Send` ожидает JSON-полезную нагрузку со следующей структурой:

```json
{
  "to": "recipient@example.com",
  "code": "123456",
  "expires": "2023-12-31T23:59:59Z"
}
```

## Обработка ошибок

Клиент автоматически обрабатывает распространенные проблемы с соединением:
- Автоматическое переподключение при сбоях соединения
- Логика повторных попыток для временных сбоев (до 2 попыток)
- Поддержка отмены и таймаутов на основе контекста
- Корректное завершение работы с помощью `Close()`
