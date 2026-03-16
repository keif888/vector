package main

import (
	"flag"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"

	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/files"
	"github.com/keif888/vector/markers"
	"github.com/keif888/vector/pkg/dsn"
	"github.com/sirupsen/logrus"
)

var log *logrus.Logger

var characterRunes = []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randomSHA1() string {
	result := make([]rune, 32)
	for i := range result {
		result[i] = characterRunes[rand.IntN(len(characterRunes))]
	}
	return string(result)
}

func main() {
	var (
		action          string
		fileName        string
		force           bool
		numberOfMarkers int
		generateCSV     bool
		driver          string
		dsnString       string
		markerUID       string
	)

	log = logrus.New()
	log.Formatter.(*logrus.TextFormatter).DisableColors = true     // remove colors
	log.Formatter.(*logrus.TextFormatter).DisableTimestamp = false // remove timestamp from test output
	log.Level = logrus.TraceLevel
	log.Out = os.Stdout

	dbms.SetLog(log)
	markers.SetLog(log)

	flag.StringVar(&action, "action", "gencsv", "Take one of the following actions gencsv, loadcsv, cluster, query, match")
	flag.StringVar(&fileName, "filename", "", "The name of the CSV file to generate or load")
	flag.BoolVar(&force, "overwrite", false, "Overwrite the csv file?")
	flag.IntVar(&numberOfMarkers, "markers", 100, "Number of markers to generate")
	flag.BoolVar(&generateCSV, "makecsv", false, "Create a CSV file for ")
	flag.StringVar(&driver, "db", "sqlite", "driver to use.  Choose from sqlite, mysql, postgres and qdrant")
	flag.StringVar(&dsnString, "dsn", "testdb.db", "DSN to access the database")
	flag.StringVar(&markerUID, "uid", markers.QueryMarkerUID, "MarkerUID to use as the face to match against")
	flag.Parse()

	action = strings.ToLower(action)
	switch action {
	case "gencsv":
		if len(fileName) == 0 {
			flagErrorAndExit("action %s and no file name provided", action)
		}
		fileName = filepath.Clean(fileName)
		if files.FileExists(fileName) && !force {
			flagErrorAndExit("action %s and file %s already exists with overwrite not enabled", action, fileName)
		}
		if numberOfMarkers < 10 {
			flagErrorAndExit("Number of markers is not enough %d", numberOfMarkers)
		}
		if err := markers.GenerateMarkers(fileName, numberOfMarkers, log); err != nil {
			flagErrorAndExit("Generation failed with %s", err)
		}
	case "loadcsv":
		if len(fileName) == 0 {
			flagErrorAndExit("action %s and no file name provided", action)
		}
		fileName = filepath.Clean(fileName)
		if !files.FileExists(fileName) {
			flagErrorAndExit("action %s and file %s does not exist", action, fileName)
		}
		if _, ok := dsn.Params[driver]; !ok {
			flagErrorAndExit("driver %s is not valid", driver)
		}
		d, s := dsn.Parse(dsnString)
		log.Infof("main: %+v with %t success", d, s)
		if !s {
			flagErrorAndExit("dsn %s failed to parse", dsnString)
		}
		if d.Driver != strings.ToLower(driver) {
			flagErrorAndExit("driver %s does not match %s from dsn %s failed to parse", driver, d.Driver, dsnString)
		}
		if err := markers.LoadMarkers(fileName, d, log); err != nil {
			flagErrorAndExit("Generation failed with %s", err)
		}
	case "cluster":
		if _, ok := dsn.Params[driver]; !ok {
			flagErrorAndExit("driver %v is not valid", driver)
		}
		d, s := dsn.Parse(dsnString)
		if !s {
			flagErrorAndExit("dsn %s failed to parse", dsnString)
		}
		if d.Driver != strings.ToLower(driver) {
			flagErrorAndExit("driver %s does not match %s from dsn %s failed to parse", driver, d.Driver, dsnString)
		}
	case "query":
		if _, ok := dsn.Params[driver]; !ok {
			flagErrorAndExit("driver %v is not valid", driver)
		}
		d, s := dsn.Parse(dsnString)
		if !s {
			flagErrorAndExit("dsn %s failed to parse", dsnString)
		}
		if d.Driver != strings.ToLower(driver) {
			flagErrorAndExit("driver %s does not match %s from dsn %s failed to parse", driver, d.Driver, dsnString)
		}
		if err := markers.QueryMarkers(d, markerUID, log); err != nil {
			flagErrorAndExit("Query failed with %s", err)
		}
	case "match":
		if _, ok := dsn.Params[driver]; !ok {
			flagErrorAndExit("driver %v is not valid", driver)
		}
		d, s := dsn.Parse(dsnString)
		if !s {
			flagErrorAndExit("dsn %s failed to parse", dsnString)
		}
		if d.Driver != strings.ToLower(driver) {
			flagErrorAndExit("driver %s does not match %s from dsn %s failed to parse", driver, d.Driver, dsnString)
		}
		if err := markers.QueryMatch(d, markerUID, log); err != nil {
			flagErrorAndExit("QueryMatch failed with %s", err)
		}
	default:
		flagErrorAndExit("action %s was not recognised", action)
	}
}

func flagErrorAndExit(errorString string, args ...any) {
	log.Errorf(errorString, args...)
	flag.PrintDefaults()
	os.Exit(1)
}
