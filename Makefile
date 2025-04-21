postgres:
	docker run --name postgres --network bank-network -e POSTGRES_USER=root -e POSTGRES_PASSWORD=psql -p 5432:5432 -d postgres:16.8-alpine3.20

createdb:
	docker exec -it postgres createdb --username=root --owner=root bank

dropdb:
	docker exec -it postgres dropdb bank

migrateup:
	migrate -path db/migration -database "postgresql://root:psql@localhost:5432/bank?sslmode=disable" -verbose up

migrateup1:
	migrate -path db/migration -database "postgresql://root:psql@localhost:5432/bank?sslmode=disable" -verbose up 1

migratedown:
	migrate -path db/migration -database "postgresql://root:psql@localhost:5432/bank?sslmode=disable" -verbose down

migratedown1:
	migrate -path db/migration -database "postgresql://root:psql@localhost:5432/bank?sslmode=disable" -verbose down 1

sqlc:
	sqlc generate

test:
	go test -v -cover ./...

server:
	go run main.go

mock:
	mockgen -package mockdb -destination db/mock/store.go github.com/tywangq/banking-system/db/sqlc Store

.PHONY: postgres createdb dropdb migrateup migrateup1 migratedown migratedown1 sqlc test server mock
