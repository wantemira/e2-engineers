.DEFAULT_GOAL := help
COMPOSE ?= docker compose
DEMO = $(COMPOSE) run --build --rm --no-deps --entrypoint ai-buddy-demo backend
CHECKS ?= UNKNOWN
ENVIRONMENT ?= STOPPED

ifeq ($(OS),Windows_NT)
DOCKER_READY = powershell -NoProfile -ExecutionPolicy Bypass -File scripts/docker-ready.ps1
else
DOCKER_READY = sh scripts/docker-ready.sh
endif

.NOTPARALLEL:
.PHONY: help doctor up build restart down reset ps logs config demo-reset demo-initial demo-fixed demo-ci demo-environment training-up training-down training-test training-solution-test

define help_text
make up       - build and start AI-Buddy
make restart  - recreate containers and keep demo data
make down     - stop the stack and release its port
make reset    - erase Compose demo data and start from scratch
make build    - build service images
make ps       - show service status
make logs     - follow service logs
make config   - validate Compose configuration
make doctor   - check Docker Engine and Compose; start Desktop on Windows if needed
make demo-reset - restore initial PR, fixture CI and mock environment; keep history
make demo-initial - select the initial PR and keep history
make demo-fixed   - select the corrected PR and keep history
make demo-ci CHECKS=PASSED - set CI to UNKNOWN, RUNNING, FAILED or PASSED
make demo-environment ENVIRONMENT=READY - set mock training status
make training-up - start the orders training service on port 8081
make training-down - stop the orders training service
make training-test - test the exercise (fails until the error cause is preserved)
make training-solution-test - test the prepared solution
endef

help:
	$(info $(help_text))

doctor:
	@$(DOCKER_READY)

up build restart reset demo-reset demo-initial demo-fixed demo-ci demo-environment training-up training-test training-solution-test: | doctor

up:
	$(COMPOSE) up --build --remove-orphans --wait

build:
	$(COMPOSE) build

restart:
	$(COMPOSE) down --remove-orphans
	$(COMPOSE) up --build --remove-orphans --wait

down:
	$(COMPOSE) down --remove-orphans

reset:
	$(COMPOSE) down --remove-orphans --volumes
	$(DEMO) -root /app -reset
	$(COMPOSE) up --build --remove-orphans --wait

ps:
	$(COMPOSE) ps

logs:
	$(COMPOSE) logs --follow

config:
	$(COMPOSE) config --quiet

demo-reset:
	$(DEMO) -root /app -reset

demo-initial:
	$(DEMO) -root /app -revision initial

demo-fixed:
	$(DEMO) -root /app -revision fixed

demo-ci:
	$(DEMO) -root /app -checks "$(CHECKS)"

demo-environment:
	$(DEMO) -root /app -environment "$(ENVIRONMENT)"

training-up:
	$(COMPOSE) up --build --wait training
	$(DEMO) -root /app -environment READY

training-down:
	$(COMPOSE) stop training
	$(DEMO) -root /app -environment STOPPED

training-test:
	$(COMPOSE) run --build --rm --no-deps training-tests

training-solution-test:
	$(COMPOSE) run --build --rm --no-deps training-tests go test -tags solution ./...
