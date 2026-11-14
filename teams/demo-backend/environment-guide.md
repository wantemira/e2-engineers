# Учебный сервис и тестовое окружение

Это синтетический учебный сценарий AI-Buddy. Внутренний инженерный инструмент представлен моками.

## Задание

Сервис orders-training создаёт заказ через POST /orders. Нужно сохранить исходную причину ошибки хранилища в методе Create. Для проверки используется TestCreateRepositoryError с errors.Is. Исходный код находится в training/orders/internal/orders/service.go.

Начальная версия намеренно теряет причину ошибки. Её регрессионный тест должен падать. Исправьте service.go, затем повторите проверку. Эталонное исправление доступно с Go build tag solution.

## Запуск с Docker

Из корня AI-Buddy выполните make training-up. Проверка здоровья доступна по http://127.0.0.1:8081/healthz. Порт можно изменить через BUDDY_TRAINING_PORT.

Запустите make training-test. После исправления ожидается успешный результат. Для показа готового исправления выполните make training-solution-test.

Чтобы остановить учебный сервис, выполните make training-down. Логи можно посмотреть командой docker compose logs training.

## Запуск без Docker

Перейдите в training/orders. Нужен Go 1.26.2. Команда go run ./cmd/api запускает сервис на 127.0.0.1:8081. Команда go test ./... проверяет рабочую версию, go test -tags solution ./... проверяет эталон.

Тест TestMemoryRepositoryConcurrentCreate создаёт заказы из нескольких горутин и проверяет уникальность ID. TestMemoryRepositoryCancellation проверяет отмену контекста. При доступном C-компиляторе дополнительно запустите go test -race -tags solution ./....

Пример запроса из PowerShell:

Invoke-RestMethod http://127.0.0.1:8081/orders -Method POST -ContentType application/json -Body '{"product":"book","quantity":1}'

## Статусы в AI-Buddy

Напишите «Где тестовое окружение?» или «Покажи результат тестов». AI-Buddy прочитает через MCP статус учебного контура и CI для выбранного SHA PR №17 и сохранит ответ в диалоге.

Статусы окружения и CI в чате являются результатом mock-инструмента. Реальный локальный запуск тестов показывает результат в терминале. Команда make training-test выполняет учебные тесты, а make demo-ci CHECKS=PASSED задаёт демонстрационный ответ CI. Для другого ответа доступны UNKNOWN, RUNNING и FAILED.

Оператор может задать статус окружения командой make demo-environment ENVIRONMENT=READY. Допустимы STOPPED, STARTING, READY и FAILED. Полный make reset возвращает исходную версию PR и сбрасывает демонстрационные статусы.

Без Docker оператор выполняет из backend команду go run ./cmd/demo -environment READY. Демонстрационный CI задаётся командой go run ./cmd/demo -checks PASSED.

## Следующий шаг

После исправления ошибки и проверки тестов запустите «Проверь PR #17», разберите замечания и подтвердите отправку наставнику. Демонстрационный снимок исправленного PR выбирается командой make demo-fixed. Решение о слиянии принимает наставник.
