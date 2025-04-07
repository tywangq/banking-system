package main

import (
	"database/sql"
	"log"

	"github.com/tywangq/banking-system/api"
	db "github.com/tywangq/banking-system/db/sqlc"

	_ "github.com/lib/pq" // 手动导入；且需要_
)

const (
	dbDriver      = "postgres"
	dbSource      = "postgresql://root:psql@localhost:5432/bank?sslmode=disable"
	serverAddress = "0.0.0.0:8080"
)

func main() {
	conn, err := sql.Open(dbDriver, dbSource)
	if err != nil {
		log.Fatal("cannot connect to db:", err)
	}

	store := db.NewStore(conn)
	server := api.NewServer(store)

	err = server.Start(serverAddress)
	if err != nil {
		log.Fatal("cannot start server:", err)
	}
}
