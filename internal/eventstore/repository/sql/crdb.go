package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/cockroach-go/v2/crdb"
	"github.com/jackc/pgconn"
	"github.com/lib/pq"
	"github.com/zitadel/logging"

	"github.com/zitadel/zitadel/internal/api/authz"
	"github.com/zitadel/zitadel/internal/database"
	caos_errs "github.com/zitadel/zitadel/internal/errors"
	"github.com/zitadel/zitadel/internal/eventstore"
	"github.com/zitadel/zitadel/internal/eventstore/repository"
)

const (
	//as soon as stored procedures are possible in crdb
	// we could move the code to migrations and call the procedure
	// traking issue: https://github.com/cockroachdb/cockroach/issues/17511
	//
	//previous_data selects the needed data of the latest event of the aggregate
	// and buffers it (crdb inmemory)
	crdbInsert = "WITH previous_data (aggregate_type_sequence, aggregate_sequence, resource_owner) AS (" +
		"SELECT agg_type.seq, agg.seq, agg.ro FROM " +
		"(" +
		//max sequence of requested aggregate type
		" SELECT MAX(event_sequence) seq, 1 join_me" +
		" FROM eventstore.events" +
		" WHERE aggregate_type = $2" +
		" AND (CASE WHEN $9::TEXT IS NULL THEN instance_id is null else instance_id = $9::TEXT END)" +
		") AS agg_type " +
		// combined with
		"LEFT JOIN " +
		"(" +
		// max sequence and resource owner of aggregate root
		" SELECT event_sequence seq, resource_owner ro, 1 join_me" +
		" FROM eventstore.events" +
		" WHERE aggregate_type = $2 AND aggregate_id = $3" +
		" AND (CASE WHEN $9::TEXT IS NULL THEN instance_id is null else instance_id = $9::TEXT END)" +
		" ORDER BY event_sequence DESC" +
		" LIMIT 1" +
		") AS agg USING(join_me)" +
		") " +
		"INSERT INTO eventstore.events (" +
		" event_type," +
		" aggregate_type," +
		" aggregate_id," +
		" aggregate_version," +
		" creation_date," +
		" event_data," +
		" editor_user," +
		" editor_service," +
		" resource_owner," +
		" instance_id," +
		" event_sequence," +
		" previous_aggregate_sequence," +
		" previous_aggregate_type_sequence" +
		") " +
		// defines the data to be inserted
		"SELECT" +
		" $1::VARCHAR AS event_type," +
		" $2::VARCHAR AS aggregate_type," +
		" $3::VARCHAR AS aggregate_id," +
		" $4::VARCHAR AS aggregate_version," +
		" statement_timestamp() AS creation_date," +
		" $5::JSONB AS event_data," +
		" $6::VARCHAR AS editor_user," +
		" $7::VARCHAR AS editor_service," +
		" COALESCE((resource_owner), $8::VARCHAR) AS resource_owner," +
		" $9::VARCHAR AS instance_id," +
		" COALESCE(aggregate_sequence, 0)+1," +
		" aggregate_sequence AS previous_aggregate_sequence," +
		" aggregate_type_sequence AS previous_aggregate_type_sequence " +
		"FROM previous_data " +
		"RETURNING id, event_sequence, previous_aggregate_sequence, previous_aggregate_type_sequence, creation_date, resource_owner, instance_id"

	uniqueInsert = `INSERT INTO eventstore.unique_constraints
					(
						unique_type,
						unique_field,
						instance_id
					) 
					VALUES (  
						$1,
						$2,
						$3
					)`

	uniqueDelete = `DELETE FROM eventstore.unique_constraints
					WHERE unique_type = $1 and unique_field = $2 and instance_id = $3`
	uniqueDeleteInstance = `DELETE FROM eventstore.unique_constraints
					WHERE instance_id = $1`
)

type CRDB struct {
	*database.DB
}

func NewCRDB(client *database.DB) *CRDB {
	return &CRDB{client}
}

func (db *CRDB) Health(ctx context.Context) error { return db.Ping() }

// Push adds all events to the eventstreams of the aggregates.
// This call is transaction save. The transaction will be rolled back if one event fails
func (db *CRDB) Push(ctx context.Context, commands ...eventstore.Command) (events []eventstore.Event, err error) {
	events = make([]eventstore.Event, len(commands))

	err = crdb.ExecuteTx(ctx, db.DB.DB, nil, func(tx *sql.Tx) error {

		var (
			previousAggregateSequence     Sequence
			previousAggregateTypeSequence Sequence
		)

		var uniqueConstraints []*eventstore.UniqueConstraint

		for i, command := range commands {
			if command.Aggregate().InstanceID == "" {
				command.Aggregate().InstanceID = authz.GetInstance(ctx).InstanceID()
			}

			var data Data
			if command.Payload() != nil {
				data, err = json.Marshal(command.Payload())
				if err != nil {
					return err
				}
			}
			e := &repository.Event{
				Typ:           command.Type(),
				EditorService: "eventstore.v2",
				Data:          data,
				EditorUser:    command.Creator(),
				Version:       command.Aggregate().Version,
				AggregateID:   command.Aggregate().ID,
				AggregateType: command.Aggregate().Type,
				ResourceOwner: sql.NullString{String: command.Aggregate().ResourceOwner, Valid: command.Aggregate().ResourceOwner != ""},
				InstanceID:    command.Aggregate().InstanceID,
			}

			err := tx.QueryRowContext(ctx, crdbInsert,
				e.Type(),
				e.Aggregate().Type,
				e.Aggregate().ID,
				e.Aggregate().Version,
				data,
				e.Creator(),
				"zitadel",
				e.Aggregate().ResourceOwner,
				e.Aggregate().InstanceID,
			).Scan(&e.ID, &e.Seq, &previousAggregateSequence, &previousAggregateTypeSequence, &e.CreationDate, &e.ResourceOwner, &e.InstanceID)

			e.PreviousAggregateSequence = uint64(previousAggregateSequence)
			e.PreviousAggregateTypeSequence = uint64(previousAggregateTypeSequence)

			if err != nil {
				logging.WithFields(
					"aggregate", e.Aggregate().Type,
					"aggregateId", e.Aggregate().ID,
					"aggregateType", e.Aggregate().Type,
					"eventType", e.Type(),
					"instanceID", e.Aggregate().InstanceID,
				).WithError(err).Debug("query failed")
				return caos_errs.ThrowInternal(err, "SQL-SBP37", "unable to create event")
			}

			uniqueConstraints = append(uniqueConstraints, command.UniqueConstraints()...)
			events[i] = e
		}

		err := db.handleUniqueConstraints(ctx, tx, uniqueConstraints...)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil && !errors.Is(err, &caos_errs.CaosError{}) {
		err = caos_errs.ThrowInternal(err, "SQL-DjgtG", "unable to store events")
	}

	return events, err
}

var instanceRegexp = regexp.MustCompile(`eventstore\.i_[0-9a-zA-Z]{1,}_seq`)

func (db *CRDB) CreateInstance(ctx context.Context, instanceID string) error {
	var sequenceName string
	err := db.QueryRowContext(ctx,
		func(row *sql.Row) error {
			if err := row.Scan(&sequenceName); err != nil || !instanceRegexp.MatchString(sequenceName) {
				return caos_errs.ThrowInvalidArgument(err, "SQL-7gtFA", "Errors.InvalidArgument")
			}
			return nil
		},
		"SELECT CONCAT('eventstore.i_', $1::TEXT, '_seq')", instanceID,
	)
	if err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, "CREATE SEQUENCE "+sequenceName); err != nil {
		return caos_errs.ThrowInternal(err, "SQL-7gtFA", "Errors.Internal")
	}

	return nil
}

// handleUniqueConstraints adds or removes unique constraints
func (db *CRDB) handleUniqueConstraints(ctx context.Context, tx *sql.Tx, uniqueConstraints ...*eventstore.UniqueConstraint) (err error) {
	if len(uniqueConstraints) == 0 || (len(uniqueConstraints) == 1 && uniqueConstraints[0] == nil) {
		return nil
	}

	for _, uniqueConstraint := range uniqueConstraints {
		uniqueConstraint.UniqueField = strings.ToLower(uniqueConstraint.UniqueField)
		switch uniqueConstraint.Action {
		case eventstore.UniqueConstraintAdd:
			_, err := tx.ExecContext(ctx, uniqueInsert, uniqueConstraint.UniqueType, uniqueConstraint.UniqueField, authz.GetInstance(ctx).InstanceID())
			if err != nil {
				logging.WithFields(
					"unique_type", uniqueConstraint.UniqueType,
					"unique_field", uniqueConstraint.UniqueField).WithError(err).Info("insert unique constraint failed")

				if db.isUniqueViolationError(err) {
					return caos_errs.ThrowAlreadyExists(err, "SQL-M0dsf", uniqueConstraint.ErrorMessage)
				}

				return caos_errs.ThrowInternal(err, "SQL-dM9ds", "unable to create unique constraint")
			}
		case eventstore.UniqueConstraintRemove:
			_, err := tx.ExecContext(ctx, uniqueDelete, uniqueConstraint.UniqueType, uniqueConstraint.UniqueField, authz.GetInstance(ctx).InstanceID())
			if err != nil {
				logging.WithFields(
					"unique_type", uniqueConstraint.UniqueType,
					"unique_field", uniqueConstraint.UniqueField).WithError(err).Info("delete unique constraint failed")
				return caos_errs.ThrowInternal(err, "SQL-6n88i", "unable to remove unique constraint")
			}
		case eventstore.UniqueConstraintInstanceRemove:
			_, err := tx.ExecContext(ctx, uniqueDeleteInstance, authz.GetInstance(ctx).InstanceID())
			if err != nil {
				logging.WithFields(
					"instance_id", authz.GetInstance(ctx).InstanceID()).WithError(err).Info("delete instance unique constraints failed")
				return caos_errs.ThrowInternal(err, "SQL-6n88i", "unable to remove unique constraints of instance")
			}
		}
	}
	return nil
}

// Filter returns all events matching the given search query
func (crdb *CRDB) Filter(ctx context.Context, searchQuery *eventstore.SearchQueryBuilder) (events []eventstore.Event, err error) {
	events = make([]eventstore.Event, 0, searchQuery.GetLimit())
	err = query(ctx, crdb, searchQuery, &events)
	if err != nil {
		return nil, err
	}

	return events, nil
}

// LatestSequence returns the latest sequence found by the search query
func (db *CRDB) LatestSequence(ctx context.Context, searchQuery *eventstore.SearchQueryBuilder) (time.Time, error) {
	var createdAt sql.NullTime
	err := query(ctx, db, searchQuery, &createdAt)
	return createdAt.Time, err
}

// InstanceIDs returns the instance ids found by the search query
func (db *CRDB) InstanceIDs(ctx context.Context, searchQuery *eventstore.SearchQueryBuilder) ([]string, error) {
	var ids []string
	err := query(ctx, db, searchQuery, &ids)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (db *CRDB) db() *database.DB {
	return db.DB
}

func (db *CRDB) orderByEventSequence(desc bool) string {
	if desc {
		return " ORDER BY crdb_internal_mvcc_timestamp DESC, event_sequence DESC"
	}

	return " ORDER BY crdb_internal_mvcc_timestamp, event_sequence"
}

func (db *CRDB) eventQuery() string {
	return "SELECT" +
		" hlc_to_timestamp(crdb_internal_mvcc_timestamp)::TIMESTAMPTZ" +
		", event_type" +
		", event_sequence" +
		", previous_aggregate_sequence" +
		", previous_aggregate_type_sequence" +
		", event_data" +
		", editor_service" +
		", editor_user" +
		", resource_owner" +
		", instance_id" +
		", aggregate_type" +
		", aggregate_id" +
		", aggregate_version" +
		" FROM eventstore.events"
}

func (db *CRDB) maxSequenceQuery() string {
	return "SELECT hlc_to_timestamp(MAX(crdb_internal_mvcc_timestamp))::TIMESTAMPTZ FROM eventstore.events"
}

func (db *CRDB) instanceIDsQuery() string {
	return "SELECT DISTINCT instance_id FROM eventstore.events"
}

func (db *CRDB) columnName(col repository.Field) string {
	switch col {
	case repository.FieldAggregateID:
		return "aggregate_id"
	case repository.FieldAggregateType:
		return "aggregate_type"
	case repository.FieldSequence:
		return "event_sequence"
	case repository.FieldResourceOwner:
		return "resource_owner"
	case repository.FieldInstanceID:
		return "instance_id"
	case repository.FieldEditorService:
		return "editor_service"
	case repository.FieldEditorUser:
		return "editor_user"
	case repository.FieldEventType:
		return "event_type"
	case repository.FieldEventData:
		return "event_data"
	case repository.FieldCreationDate:
		return "hlc_to_timestamp(crdb_internal_mvcc_timestamp)::TIMESTAMPTZ"
	default:
		return ""
	}
}

func (db *CRDB) conditionFormat(operation repository.Operation) string {
	switch operation {
	case repository.OperationIn:
		return "%s %s ANY(?)"
	case repository.OperationNotIn:
		return "%s %s ALL(?)"
	}
	return "%s %s ?"
}

func (db *CRDB) operation(operation repository.Operation) string {
	switch operation {
	case repository.OperationEquals, repository.OperationIn:
		return "="
	case repository.OperationGreater:
		return ">"
	case repository.OperationLess:
		return "<"
	case repository.OperationJSONContains:
		return "@>"
	case repository.OperationNotIn:
		return "<>"
	}
	return ""
}

var (
	placeholder = regexp.MustCompile(`\?`)
)

// placeholder replaces all "?" with postgres placeholders ($<NUMBER>)
func (db *CRDB) placeholder(query string) string {
	occurances := placeholder.FindAllStringIndex(query, -1)
	if len(occurances) == 0 {
		return query
	}
	replaced := query[:occurances[0][0]]

	for i, l := range occurances {
		nextIDX := len(query)
		if i < len(occurances)-1 {
			nextIDX = occurances[i+1][0]
		}
		replaced = replaced + "$" + strconv.Itoa(i+1) + query[l[1]:nextIDX]
	}
	return replaced
}

func (db *CRDB) isUniqueViolationError(err error) bool {
	if pqErr, ok := err.(*pq.Error); ok {
		if pqErr.Code == "23505" {
			return true
		}
	}
	if pgxErr, ok := err.(*pgconn.PgError); ok {
		if pgxErr.Code == "23505" {
			return true
		}
	}
	return false
}
