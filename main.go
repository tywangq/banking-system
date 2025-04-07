package main

import (
	"database/sql"
	"log"

	"github.com/tywangq/banking-system/api"
	db "github.com/tywangq/banking-system/db/sqlc"
	"github.com/tywangq/banking-system/util"

	_ "github.com/lib/pq" // 手动导入；且需要_
)

func main() {
	config, err := util.LoadConfig(".")
	if err != nil {
		log.Fatal("cannot load config:", err)
	}

	conn, err := sql.Open(config.DbDriver, config.DbSource)
	if err != nil {
		log.Fatal("cannot connect to db:", err)
	}

	store := db.NewStore(conn)
	server := api.NewServer(store)

	err = server.Start(config.ServerAddress)
	if err != nil {
		log.Fatal("cannot start server:", err)
	}
}
