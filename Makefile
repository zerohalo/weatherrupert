.PHONY: up down dev

up:
	docker compose up --build; docker compose down

down:
	docker compose down

dev:
	git pull && docker compose up --build; docker compose down --rmi local
