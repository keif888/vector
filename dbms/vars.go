package dbms

import (
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var Drivers = map[string]func(string) gorm.Dialector{
	MySQL:    mysql.Open,
	SQLite3:  sqlite.Open,
	Postgres: postgres.Open,
}

var log *logrus.Logger

func SetLog(logger *logrus.Logger) {
	log = logger
}
