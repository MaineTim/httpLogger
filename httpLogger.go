//httpLogger

package main

import (
	"database/sql"
	"encoding/json"
	log "github.com/Sirupsen/logrus"
	// "github.com/davecgh/go-spew/spew"
	"github.com/docopt/docopt-go"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

const version = ".01b-2017Nov21"
const usage = `
httpLogger

Usage: httpLogger [options]

Options:
 -d LEVEL  Set logging level. 
             i = Info
             e = Error
             d = Debug
             [default: e]
 -v         Show version.
`
const runtimesSQLTable = `
CREATE TABLE IF NOT EXISTS runtimes(
StartTime TEXT,
EndTime TEXT,
InsertionTime TEXT
);
`
const tempSQLTable = `
CREATE TABLE IF NOT EXISTS temps(
Basement REAL,
Bed REAL, 
Crawlspace REAL, 
Downstairs REAL,
Garage REAL,
Upstairs REAL,
InsertedDatetime TEXT
);
`

type RunEntry struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

type TempEntry struct {
	Basement   float64 `json:"basement"`
	Bed        float64 `json:"bed"`
	Crawlspace float64 `json:"crawlspace"`
	Downstairs float64 `json:"downstairs"`
	Garage     float64 `json:"garage"`
	Upstairs   float64 `json:"upstairs"`
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
	tempLoggerChan           = make(chan int, 1)
)

func initDBase(filepath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", filepath)
	if err == nil && db != nil {
		_, err = db.Exec("pragma synchronous")
	}
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
InsertionTime
) values(?, ?, ?)
`
	stmt, err := runtimesDB.Prepare(sqlAdditem)
	if err != nil {
		log.Debug("Prepare result: " + err.Error())
		return errors.Wrap(err, "storeRunEntry:db.Prepare")
	}
	defer stmt.Close()

	_, err = stmt.Exec(entry.StartTime, entry.EndTime, time.Now().Format(time.RFC3339))
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
	tempLoggerChan <- 0
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

func getTemperatures() (TempEntry, error) {

	var entry TempEntry
	var netClient = &http.Client{
		Timeout: time.Second * 30,
	}

	log.Debugf("Sending GET to: %s", configFile.urlTempServer)
	response, err := netClient.Get(configFile.urlTempServer)
	if err != nil {
		return TempEntry{}, errors.Wrap(err, "getTemperatures:netClient.Get")
	}
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return TempEntry{}, errors.Wrap(err, "getTemperatures:ioutil.ReadAll")
	}
	if err = json.Unmarshal(body, &entry); err != nil {
		return TempEntry{}, errors.Wrap(err, "getTemperatures:json.Unmarshal")
	}
	return entry, nil
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
) values(?, ?, ?, ?, ?, ?, ?)
`
	stmt, err := db.Prepare(sqlAdditem)
	if err != nil {
		log.Debug("Prepare result: " + err.Error())
		return errors.Wrap(err, "storeTempEntry:db.Prepare")
	}
	defer stmt.Close()

	_, err = stmt.Exec(entry.Basement, entry.Bed, entry.Crawlspace,
		entry.Downstairs, entry.Garage, entry.Upstairs, time.Now().Format(time.RFC3339))
	if err != nil {
		log.Debug("Exec result: " + err.Error())
		return errors.Wrap(err, "storeTempEntry:stmt.Exec")
	}
	log.Info("Logged temps")
	return nil
}

// Runs as goroutine, waits for signal to start recording
// temps. When end of run is received, records additional
// temps based on config file entry.
func tempLogger(tempLoggerChan chan int) {

	log.Debug("tempLogger goroutine started")
	active := 0
	postp := 0
	loop := true
	for loop == true {
		select {
		case active = <-tempLoggerChan:
			log.Debugf("active = %d", active)
		default:
			if active == 1 || postp > 0 {
				log.Debugf("tempLogger passes: %d", postp)
				entry, _ := getTemperatures()
				storeTempEntry(entry, runtempsDB)
				//time.Sleep(3 * time.Second)
				time.Sleep(2 * time.Minute)
				if active == 1 {
					postp = 11 // 10 passes
				}
			} else if active == 2 {
				loop = false
			}
			if postp > 0 {
				postp--
			}
		}
	}
	log.Debug("tempLogger goroutine ended")
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
	log.Debugf("command = %s", command)
	switch command {
	case "store":
		storeTempsHandlerError = storeTempEntry(entry, alltempsDB)
	case "run":
		log.Debug("Sent tempLogger signal 1")
		tempLoggerChan <- 1
	}
	if storeTempsHandlerError != nil {
		log.Debug("storeTempsHandlerError = " + storeTempsHandlerError.Error())
	}
	respond()
	log.Debug("Exiting storeTempsHandler")
}

func mainloop() {
	exitSignal := make(chan os.Signal)
	signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)
	<-exitSignal
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
	arguments, _ := docopt.Parse(usage, nil, true, version, false)
	logLevel := arguments["-d"]
	switch logLevel {
	case "d":
		log.SetLevel(log.DebugLevel)
	case "i":
		log.SetLevel(log.InfoLevel)
	default:
		log.SetLevel(log.ErrorLevel)
	}
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
	go tempLogger(tempLoggerChan)
	request := mux.NewRouter()
	request.HandleFunc("/burnerlogger", storeRunEntryHandler).Methods("POST")
	request.HandleFunc("/temps/{command}", storeTempsHandler)
	log.Debug("HTTP handlers registered")
	http.Handle("/", request)
	if err = http.ListenAndServe(":8000", nil); err != nil {
		log.Errorf("HTTP server error: %s\n", err)
		runtime.Goexit()
	}
	mainloop()
	tempLoggerChan <- 2
}
