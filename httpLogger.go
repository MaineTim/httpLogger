//httpLogger

package main

import (
	"database/sql"
	"encoding/json"
	log "github.com/Sirupsen/logrus"
	"github.com/docopt/docopt-go"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
)

const version = ".01a-2017Nov17"

const usage = `
httpLogger

Usage: httpLogger <DBasePath>
`

type RunEntry struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

var db *sql.DB
var storeRunEntryHandlerError error
var storeRunTempHandlerError error
var storeTemperatureHandler error

func initDBase(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", filepath)
	if err != nil {
		return nil, errors.Wrap(err, "initDBase")
	}
	return db, nil
}

func createTable(db *sql.DB) error {
	sql_table := `
CREATE TABLE IF NOT EXISTS runtimes(
StartTime TEXT,
EndTime TEXT,
InsertedDatetime DATETIME
);
`
	if _, err := db.Exec(sql_table); err != nil {
		return errors.Wrap(err, "createTable")
	}
	return nil
}

func storeRunEntry(entry RunEntry) error {
	sql_additem := `
INSERT INTO runtimes(
StartTime,
EndTime,
InsertedDatetime
) values(?, ?, CURRENT_TIMESTAMP)
`
	stmt, err := db.Prepare(sql_additem)
	if err != nil {
		return errors.Wrap(err, "storeRunEntry:db.Prepare")
	}
	defer stmt.Close()

	if _, err2 := stmt.Exec(entry.StartTime, entry.EndTime); err2 != nil {
		return errors.Wrap(err, "storeRunEntry:stmt.Exec")
	}
	log.Info("Logged run time.")
	return nil
}

func storeRunEntryHandler(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	var entry RunEntry

	body, err := ioutil.ReadAll(request.Body)
	if err != nil {
		storeRunEntryHandlerError = errors.Wrap(err, "storeRunEntryHandler:ioutil.ReadAll")
		return
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		storeRunEntryHandlerError = errors.Wrap(err, "storeRunEntryHandler:json.Unmarshal")
		return
	}
	storeRunEntry(entry)
}

func main() {
	var err error

	defer os.Exit(0)

	Formatter := new(log.TextFormatter)
	Formatter.TimestampFormat = "02-Jan-2006 15:04:05"
	Formatter.FullTimestamp = true
	log.SetFormatter(Formatter)

	arguments, _ := docopt.Parse(usage, nil, true, version, false)
	dbpath := arguments["<DBasePath>"].(string)
	log.Info("Logging data to ", dbpath)

	if db, err = initDBase(dbpath); err != nil {
		log.Errorf("Database initialization failed with error: %s\n", err)
		runtime.Goexit()
	}
	defer db.Close()
	if err = createTable(db); err != nil {
		log.Errorf("Table creation failed with error: %s\n", err)
		runtime.Goexit()
	}

	request := mux.NewRouter()
	request.HandleFunc("/burnerlogger", storeRunEntryHandler).Methods("POST")

	http.Handle("/", request)
	if err = http.ListenAndServe(":8000", nil); err != nil {
		log.Errorf("HTTP server error: %s\n", err)
		runtime.Goexit()
	}
}
