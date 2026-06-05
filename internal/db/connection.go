package db

import (
	"fmt"

	"github.com/katuva/wallet/config"
	logger "github.com/katuva/wallet/dpk/logger"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	logger2 "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// DB is the global variable that holds the database connection object
var DB *gorm.DB

type DatabaseConnection struct {
	dbHost string
	dbPort string
	dbName string
	dbUser string
	dbPass string
}

func NewDatabase(cfg *config.Config) *DatabaseConnection {
	return &DatabaseConnection{
		dbHost: cfg.DbHost,
		dbName: cfg.DbName,
		dbUser: cfg.DbUser,
		dbPass: cfg.DbPassword,
		dbPort: cfg.DbPort,
	}
}

func (d *DatabaseConnection) Connect() {
	var err error

	dbUrl := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s", d.dbHost,
		d.dbUser, d.dbPass, d.dbName, d.dbPort, "disable")
	DB, err = gorm.Open(postgres.Open(dbUrl), &gorm.Config{NamingStrategy: schema.NamingStrategy{
		SingularTable: true,
	}, Logger: logger2.Default.LogMode(logger2.Silent)})

	if err != nil {
		logger.ErrorLog.Println(err)
	} else {
		logger.InfoLog.Println("Database connection established successfully")
	}
}
