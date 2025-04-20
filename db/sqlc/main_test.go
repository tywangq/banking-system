package db

import (
	"database/sql"
	"log"
	"os"
	"testing"

	"github.com/tywangq/banking-system/util"

	_ "github.com/lib/pq" // 手动导入；且需要_
)

var testQueries *Queries
var testDB *sql.DB

func TestMain(m *testing.M) {
	config, err := util.LoadConfig("../..")
	if err != nil {
		log.Fatal("cannot load config:", err)
	}

	testDB, err = sql.Open(config.DbDriver, config.DbSource)
	if err != nil {
		log.Fatal("cannot connect to db:", err)
	}

	testQueries = New(testDB)

	os.Exit(m.Run())
}
