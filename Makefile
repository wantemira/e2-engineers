.DEFAULT_GOAL := help
COMPOSE ?= docker compose

.NOTPARALLEL:
.PHONY: help up build restart down reset ps logs config

define help_text
make up       - build and start AI-Buddy
make restart  - recreate containers and keep demo data
make down     - stop the stack and release its port
make reset    - erase Compose demo data and start from scratch
make build    - build service images
make ps       - show service status
make logs     - follow service logs
make config   - validate Compose configuration
endef

help:
	$(info $(help_text))

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
	$(COMPOSE) up --build --remove-orphans --wait

ps:
	$(COMPOSE) ps

logs:
	$(COMPOSE) logs --follow

config:
	$(COMPOSE) config --quiet
