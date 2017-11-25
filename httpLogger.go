//httpLogger

package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/boltdb/bolt"
	"github.com/docopt/docopt-go"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

const version = ".02alpha1-2017Nov25"
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

type RunEntry struct {
	SerialNumber int    `json:"serialNumber"`
	StartTime    string `json:"startTime"`
	EndTime      string `json:"endTime"`
}

type TempEntry struct {
	SerialNumber int     `json:"serialNumber"`
	Basement     float64 `json:"basement"`
	Bed          float64 `json:"bed"`
	Crawlspace   float64 `json:"crawlspace"`
	Downstairs   float64 `json:"downstairs"`
	Garage       float64 `json:"garage"`
	Upstairs     float64 `json:"upstairs"`
}

type ConfigFile struct {
	pathBurnerLogDB string
	urlTempServer   string
	loggerPort      string
}

type AppContext struct {
	burnerLogDB              *bolt.DB
	storeRunTempHandlerError error
	storeTemperatureHandler  error
	configFile               ConfigFile
	tempLoggerChan           chan int
	serialNumber             int
}

type appHandler struct {
	*AppContext
	H func(*AppContext, http.ResponseWriter, *http.Request) (int, error)
}

// Initializes the database named in filepath, and creates
// the required buckets.
// Returns a bolt.DB pointer and any setup errors.

func initDBase(filepath string) (*bolt.DB, error) {
	db, err := bolt.Open(filepath, 0777, nil)
	if err != nil {
		return nil, errors.Wrap(err, "initDBase:Open")
	}
	for _, bucket := range []string{"serial", "times", "temps"} {
		err = db.Update(func(tx *bolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists([]byte(bucket))
			if err != nil {
				return errors.Wrap(err, "initDBase:CreateBucket")
			}
			if err != nil {
				return errors.Wrap(err, "initDBase:Update")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return db, nil
}

// Stores a RunEntry in JSON in the "times" bucket.
// Adds a timestamp as key.
// Returns any errors.

func storeRunEntry(ah AppContext, entry RunEntry) error {
	log.Debug("Entered storeRunEntry")
	record, err := json.Marshal(entry)
	if err != nil {
		log.Debug("json.Marshal result: " + err.Error())
		return errors.Wrap(err, "storeRunEntry:json.Marshal")
	}
	err = ah.burnerLogDB.Update(func(tx *bolt.Tx) error {
		err := tx.Bucket([]byte("times")).Put([]byte(time.Now().UTC().Format(time.RFC3339)), record)
		if err != nil {
			return errors.Wrap(err, "storeRunEntry:DBPut")
		}
		return nil
	})
	log.Info("Logged run time")
	return err
	/* TODO: Look at Update error handling */
}

// Handlerfunc for "/burnerlogger".
// Sends any errors to the client.

func (ah AppContext) storeRunEntryHandler(writer http.ResponseWriter, request *http.Request) {
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
	ah.tempLoggerChan <- 0
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
	entry.SerialNumber = ah.serialNumber
	storeRunEntryHandlerError = storeRunEntry(ah, entry)
	if storeRunEntryHandlerError != nil {
		log.Debug("storeRunEntryHandlerError = " + storeRunEntryHandlerError.Error())
	}
	respond()
	log.Debug("Exiting storeRunEntryHandler")
}

// Retrieves a temperature set and puts it in a TempEntry.
// Returns the TempEntry and any errors.

func getTemperatures(ah *AppContext) (TempEntry, error) {

	var entry TempEntry
	var netClient = &http.Client{
		Timeout: time.Second * 30,
	}

	log.Debugf("Sending GET to: %s", ah.configFile.urlTempServer)
	response, err := netClient.Get(ah.configFile.urlTempServer)
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

// Stores a TempEntry in JSON in the "temps" bucket.
// Adds a timestamp as a key.
// Returns any errors.

func storeTempEntry(ah *AppContext, entry TempEntry, bucket string) error {
	log.Debug("Entered storeTempEntry")

	record, err := json.Marshal(entry)
	if err != nil {
		log.Debug("json.Marshal result: " + err.Error())
		return errors.Wrap(err, "storeTempEntry:json.Marshal")
	}
	err = ah.burnerLogDB.Update(func(tx *bolt.Tx) error {
		err := tx.Bucket([]byte(bucket)).Put([]byte(time.Now().UTC().Format(time.RFC3339)), record)
		if err != nil {
			return errors.Wrap(err, "storeTempEntry:DBPut")
		}
		return nil
	})
	log.Info("Logged temps")
	return err
	/* TODO: Look at Update error handling */
}

// Runs as goroutine, waits for signal to start recording
// temps. When end of run is received, records additional
// temps based on config file entry.

func tempLogger(app *AppContext) {

	log.Debug("tempLogger goroutine started")
	active := 0
	postp := 0
	loop := true
	for loop == true {
		select {
		case active = <-app.tempLoggerChan:
			log.Debugf("active = %d", active)
		default:
			if active == 1 || postp > 0 {
				log.Debugf("tempLogger passes: %d", postp)
				entry, _ := getTemperatures(app)
				storeTempEntry(app, entry, "temps")
				time.Sleep(3 * time.Second)
				//time.Sleep(2 * time.Minute)
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

// Handlerfunc for "/temps/{command}".
// Sends any errors to the client.

func (ah AppContext) storeTempsHandler(writer http.ResponseWriter, request *http.Request) {
	var storeTempsHandlerError error = nil

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
	log.Debug("Sent tempLogger signal 1")
	ah.tempLoggerChan <- 1
	if storeTempsHandlerError != nil {
		log.Debug("storeTempsHandlerError = " + storeTempsHandlerError.Error())
	}
	respond()
	log.Debug("Exiting storeTempsHandler")
}

// Main

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

	app := new(AppContext)
	app.tempLoggerChan = make(chan int, 1)
	viper.SetConfigFile("httpLogger.toml")
	if err = viper.ReadInConfig(); err != nil {
		log.Errorf("Config file error: %s", err)
		runtime.Goexit()
	} else {
		app.configFile.pathBurnerLogDB = viper.GetString("DBs.burnerlog")
		app.configFile.urlTempServer = viper.GetString("Servers.temperatures")
		app.configFile.loggerPort = viper.GetString("Servers.httploggerport")
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
	if app.burnerLogDB, err = initDBase(app.configFile.pathBurnerLogDB); err != nil {
		log.Errorf("Burnerlog database initialization failed with error: %s\n", err)
		runtime.Goexit()
	}
	defer app.burnerLogDB.Close()
	log.Debug("DBase access and/or creation completed")
	go tempLogger(app)
	request := mux.NewRouter()
	request.HandleFunc("/burnerlogger", app.storeRunEntryHandler).Methods("POST")
	request.HandleFunc("/temps/{command}", app.storeTempsHandler)
	http.Handle("/", request)
	log.Debug("HTTP handlers registered")
	log.Debugf("HTTP Server started, listening on :%s", app.configFile.loggerPort)
	if err = http.ListenAndServe(":"+app.configFile.loggerPort, nil); err != nil {
		log.Errorf("HTTP server error: %s", err)
		runtime.Goexit()
	}
	mainloop()
	app.tempLoggerChan <- 2
}
