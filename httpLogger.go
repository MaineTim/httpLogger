//httpLogger

package main

import (
	"database/sql"
	"encoding/json"
	log "github.com/Sirupsen/logrus"
	//"github.com/davecgh/go-spew/spew"
	"github.com/docopt/docopt-go"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"io/ioutil"
	"net/http"
	"os"
	"runtime"
	"time"
)

const version = ".01a-2017Nov18"
const usage = `
httpLogger

Usage: httpLogger
`
const runtimesSQLTable = `
CREATE TABLE IF NOT EXISTS runtimes(
StartTime TEXT,
EndTime TEXT,
InsertedDatetime DATETIME
);
`
const tempSQLTable = `
CREATE TABLE IF NOT EXISTS temps(
Basement FLOAT,
Bed FLOAT, 
Crawlspace FLOAT, 
Downstairs FLOAT,
Garage FLOAT,
Upstairs FLOAT,
InsertedDatetime DATETIME
);
`

type RunEntry struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

type TempEntry struct {
	Basement   float32 `json:"basement"`
	Bed        float32 `json:"bed"`
	Crawlspace float32 `json:"crawlspace"`
	Downstairs float32 `json:"downstairs"`
	Garage     float32 `json:"garage"`
	Upstairs   float32 `json:"upstairs"`
}

type ConfigFile struct {
	pathRuntimesDB string
	pathAlltempsDB string
	pathRunTempsDB string
	urlTempServer  string
}

var (
	runtimesDB               *sql.DB
	alltempsDB               *sql.DB
	runtempsDB               *sql.DB
	storeRunTempHandlerError error
	storeTemperatureHandler  error
	configFile               ConfigFile
)

func initDBase(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", filepath)
	if err != nil {
		return nil, errors.Wrap(err, "initDBase")
	}
	return db, nil
}

func createTable(db *sql.DB, sqlTable string) error {
	if _, err := db.Exec(sqlTable); err != nil {
		return errors.Wrap(err, "createTable")
	}
	return nil
}

func storeRunEntry(entry RunEntry) error {
	log.Debug("Entered storeRunEntry")
	sqlAdditem := `
INSERT INTO runtimes(
StartTime,
EndTime,
InsertedDatetime
) values(?, ?, CURRENT_TIMESTAMP)
`
	stmt, err := runtimesDB.Prepare(sqlAdditem)
	if err != nil {
		log.Debug("Prepare result: " + err.Error())
		return errors.Wrap(err, "storeRunEntry:db.Prepare")
	}
	defer stmt.Close()

	_, err = stmt.Exec(entry.StartTime, entry.EndTime)
	if err != nil {
		log.Debug("Exec result: " + err.Error())
		return errors.Wrap(err, "storeRunEntry:stmt.Exec")
	}
	log.Info("Logged run time")
	return nil
}

func storeRunEntryHandler(writer http.ResponseWriter, request *http.Request) {
	var storeRunEntryHandlerError error = nil
	var entry RunEntry

	respond := func() {
		if storeRunEntryHandlerError != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			writer.Write([]byte(storeRunEntryHandlerError.Error()))
		} else {
			writer.WriteHeader(http.StatusCreated)
			writer.Write([]byte("RunEntry stored"))
		}
	}

	writer.Header().Set("Content-Type", "application/json")
	log.Debug("Entered storeRunEntryHandler")
	body, err := ioutil.ReadAll(request.Body)
	if err != nil {
		storeRunEntryHandlerError = errors.Wrap(err, "storeRunEntryHandler:ioutil.ReadAll")
		respond()
		return
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		storeRunEntryHandlerError = errors.Wrap(err, "storeRunEntryHandler:json.Unmarshal")
		respond()
		return
	}
	storeRunEntryHandlerError = storeRunEntry(entry)
	if storeRunEntryHandlerError != nil {
		log.Debug("storeRunEntryHandlerError = " + storeRunEntryHandlerError.Error())
	}
	respond()
	log.Debug("Exiting storeRunEntryHandler")
}

func storeTempEntry(entry TempEntry, db *sql.DB) error {
	log.Debug("Entered storeTempEntry")
	sqlAdditem := `
INSERT INTO temps (
Basement,
Bed, 
Crawlspace, 
Downstairs,
Garage,
Upstairs,
InsertedDatetime
) values(?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
`
	stmt, err := db.Prepare(sqlAdditem)
	if err != nil {
		log.Debug("Prepare result: " + err.Error())
		return errors.Wrap(err, "storeTempEntry:db.Prepare")
	}
	defer stmt.Close()

	_, err = stmt.Exec(entry.Basement, entry.Bed, entry.Crawlspace,
		entry.Downstairs, entry.Garage, entry.Upstairs)
	if err != nil {
		log.Debug("Exec result: " + err.Error())
		return errors.Wrap(err, "storeTempEntry:stmt.Exec")
	}
	log.Info("Logged temps")
	return nil
}

func storeTempsHandler(writer http.ResponseWriter, request *http.Request) {
	var storeTempsHandlerError error = nil
	var entry TempEntry

	respond := func() {
		if storeTempsHandlerError != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			writer.Write([]byte(storeTempsHandlerError.Error()))
		} else {
			writer.WriteHeader(http.StatusCreated)
			writer.Write([]byte("Temperatures stored"))
		}
	}

	writer.Header().Set("Content-Type", "application/json")
	log.Debug("Entered storeTempsHandler")
	vars := mux.Vars(request)
	command := vars["command"]
	log.Debugf("command = %s\n", command)
	var netClient = &http.Client{
		Timeout: time.Second * 30,
	}
	log.Debugf("Sending GET to: %s\n", configFile.urlTempServer)
	response, err := netClient.Get(configFile.urlTempServer)
	if err != nil {
		storeTempsHandlerError = errors.Wrap(err, "storeTempsHandler:netClient.Get")
		respond()
		return
	}
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		storeTempsHandlerError = errors.Wrap(err, "storeTempsHandler:ioutil.ReadAll")
		respond()
		return
	}
	if err = json.Unmarshal(body, &entry); err != nil {
		storeTempsHandlerError = errors.Wrap(err, "storeTempsHandler:json.Unmarshal")
		respond()
		return
	}
	switch command {
	case "store":
		storeTempsHandlerError = storeTempEntry(entry, alltempsDB)
	case "run":
		storeTempsHandlerError = storeTempEntry(entry, runtempsDB)
	}
	if storeTempsHandlerError != nil {
		log.Debug("storeTempsHandlerError = " + storeTempsHandlerError.Error())
	}
	respond()
	log.Debug("Exiting storeTempsHandler")
}

func main() {
	var (
		err error
	)
	defer os.Exit(0)

	viper.SetConfigFile("httpLogger.toml")
	//	viper.AddConfigPath(".")
	if err = viper.ReadInConfig(); err != nil {
		log.Errorf("Config file error: %s", err)
		runtime.Goexit()
	} else {
		configFile.pathRuntimesDB = viper.GetString("DBs.runtimes")
		configFile.pathAlltempsDB = viper.GetString("DBs.alltemps")
		configFile.pathRunTempsDB = viper.GetString("DBs.runtemps")
		configFile.urlTempServer = viper.GetString("Servers.temperatures")
	}
	Formatter := new(log.TextFormatter)
	Formatter.TimestampFormat = "02-Jan-2006 15:04:05"
	Formatter.FullTimestamp = true
	log.SetFormatter(Formatter)
	log.SetLevel(log.DebugLevel)

	_, _ = docopt.Parse(usage, nil, true, version, false)
	log.Info("httpLogger " + version + " starting")
	if runtimesDB, err = initDBase(configFile.pathRuntimesDB); err != nil {
		log.Errorf("Runtimes database initialization failed with error: %s\n", err)
		runtime.Goexit()
	}
	defer runtimesDB.Close()
	if err = createTable(runtimesDB, runtimesSQLTable); err != nil {
		log.Errorf("Table creation failed with error: %s\n", err)
		runtime.Goexit()
	}
	if alltempsDB, err = initDBase(configFile.pathAlltempsDB); err != nil {
		log.Errorf("Alltemps database initialization failed with error: %s\n", err)
		runtime.Goexit()
	}
	defer alltempsDB.Close()
	if err = createTable(alltempsDB, tempSQLTable); err != nil {
		log.Errorf("Table creation failed with error: %s\n", err)
		runtime.Goexit()
	}
	if runtempsDB, err = initDBase(configFile.pathRunTempsDB); err != nil {
		log.Errorf("Runtemps database initialization failed with error: %s\n", err)
		runtime.Goexit()
	}
	defer runtempsDB.Close()
	if err = createTable(runtempsDB, tempSQLTable); err != nil {
		log.Errorf("Table creation failed with error: %s\n", err)
		runtime.Goexit()
	}
	log.Debug("DBase access and/or creation completed")
	request := mux.NewRouter()
	request.HandleFunc("/burnerlogger", storeRunEntryHandler).Methods("POST")
	request.HandleFunc("/temps/{command}", storeTempsHandler)
	log.Debug("HTTP handlers registered")
	http.Handle("/", request)
	if err = http.ListenAndServe(":8000", nil); err != nil {
		log.Errorf("HTTP server error: %s\n", err)
		runtime.Goexit()
	}
}
