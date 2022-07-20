package initialise

import (
	"database/sql"
	"embed"
	_ "embed"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zitadel/logging"

	"github.com/zitadel/zitadel/internal/database"
	"github.com/zitadel/zitadel/internal/id"
)

var (
	//go:embed sql/cockroach/*
	//go:embed sql/postgres/*
	stmts embed.FS

	createUserStmt           string
	grantStmt                string
	databaseStmt             string
	createEventstoreStmt     string
	createProjectionsStmt    string
	createSystemStmt         string
	createEncryptionKeysStmt string
	createEventsStmt         string
	createSystemSequenceStmt string
	createUniqueConstraints  string

	roleAlreadyExistsCode = "42710"
	dbAlreadyExistsCode   = "42P04"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "initialize ZITADEL instance",
		Long: `Sets up the minimum requirements to start ZITADEL.

Prereqesits:
- cockroachdb

The user provided by flags needs privileges to
- create the database if it does not exist
- see other users and create a new one if the user does not exist
- grant all rights of the ZITADEL database to the user created if not yet set
`,
		Run: func(cmd *cobra.Command, args []string) {
			config := MustNewConfig(viper.GetViper())

			InitAll(config)
		},
	}

	cmd.AddCommand(newZitadel(), newDatabase(), newUser(), newGrant())
	return cmd
}

func InitAll(config *Config) {
	id.Configure(config.Machine)

	err := initialise(config.Database,
		VerifyUser(config.Database.Username(), config.Database.Password()),
		VerifyDatabase(config.Database.Database()),
		VerifyGrant(config.Database.Database(), config.Database.Username()),
	)

	logging.OnError(err).Fatal("unable to initialize the database")

	err = verifyZitadel(config.Database)
	logging.OnError(err).Fatal("unable to initialize ZITADEL")
}

func initialise(config database.Config, steps ...func(*sql.DB, database.Config) error) error {
	logging.Info("initialization started")

	readStmts(config.Type())

	db, err := database.Connect(config, true)
	if err != nil {
		return err
	}
	defer db.Close()

	return Init(db, config, steps...)
}

func Init(db *sql.DB, config database.Config, steps ...func(*sql.DB, database.Config) error) error {
	for _, step := range steps {
		if err := step(db, config); err != nil {
			return err
		}
	}

	return nil
}

func readStmts(typ string) (err error) {
	createUserStmt, err = readStmt(typ, "01_user")
	if err != nil {
		return err
	}

	databaseStmt, err = readStmt(typ, "02_database")
	if err != nil {
		return err
	}

	grantStmt, err = readStmt(typ, "03_grant_user")
	if err != nil {
		return err
	}

	createEventstoreStmt, err = readStmt(typ, "04_eventstore")
	if err != nil {
		return err
	}

	createProjectionsStmt, err = readStmt(typ, "05_projections")
	if err != nil {
		return err
	}

	createSystemStmt, err = readStmt(typ, "06_system")
	if err != nil {
		return err
	}

	createEncryptionKeysStmt, err = readStmt(typ, "07_encryption_keys_table")
	if err != nil {
		return err
	}

	createEventsStmt, err = readStmt(typ, "08_events_table")
	if err != nil {
		return err
	}

	createSystemSequenceStmt, err = readStmt(typ, "09_system_sequence")
	if err != nil {
		return err
	}

	createUniqueConstraints, err = readStmt(typ, "10_unique_constraints_table")
	if err != nil {
		return err
	}

	return nil
}

func readStmt(typ, step string) (string, error) {
	stmt, err := stmts.ReadFile("sql/" + typ + "/" + step + ".sql")
	return string(stmt), err
}
