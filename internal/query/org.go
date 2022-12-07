package query

import (
	"context"
	"database/sql"
	errs "errors"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/zitadel/zitadel/internal/api/authz"
	"github.com/zitadel/zitadel/internal/domain"
	"github.com/zitadel/zitadel/internal/errors"
	"github.com/zitadel/zitadel/internal/projection"
	projection_old "github.com/zitadel/zitadel/internal/query/projection"
	"github.com/zitadel/zitadel/internal/telemetry/tracing"
)

var (
	orgsTable = table{
		name:          projection_old.OrgProjectionTable,
		instanceIDCol: projection_old.OrgColumnInstanceID,
	}
	OrgColumnID = Column{
		name:  projection_old.OrgColumnID,
		table: orgsTable,
	}
	OrgColumnCreationDate = Column{
		name:  projection_old.OrgColumnCreationDate,
		table: orgsTable,
	}
	OrgColumnChangeDate = Column{
		name:  projection_old.OrgColumnChangeDate,
		table: orgsTable,
	}
	OrgColumnResourceOwner = Column{
		name:  projection_old.OrgColumnResourceOwner,
		table: orgsTable,
	}
	OrgColumnInstanceID = Column{
		name:  projection_old.OrgColumnInstanceID,
		table: orgsTable,
	}
	OrgColumnState = Column{
		name:  projection_old.OrgColumnState,
		table: orgsTable,
	}
	OrgColumnSequence = Column{
		name:  projection_old.OrgColumnSequence,
		table: orgsTable,
	}
	OrgColumnName = Column{
		name:  projection_old.OrgColumnName,
		table: orgsTable,
	}
	OrgColumnDomain = Column{
		name:  projection_old.OrgColumnDomain,
		table: orgsTable,
	}
)

type Orgs struct {
	SearchResponse
	Orgs []*Org
}

type Org struct {
	ID            string
	CreationDate  time.Time
	ChangeDate    time.Time
	ResourceOwner string
	State         domain.OrgState
	Sequence      uint64

	Name   string
	Domain string
}

type OrgSearchQueries struct {
	SearchRequest
	Queries []SearchQuery
}

func (q *OrgSearchQueries) toQuery(query sq.SelectBuilder) sq.SelectBuilder {
	query = q.SearchRequest.toQuery(query)
	for _, q := range q.Queries {
		query = q.toQuery(query)
	}
	return query
}

func (q *Queries) OrgByID(ctx context.Context, shouldTriggerBulk bool, id string) (_ *Org, err error) {
	ctx, span := tracing.NewSpan(ctx)
	defer func() { span.EndWithError(err) }()

	org := projection.NewOrg(id, authz.GetInstance(ctx).InstanceID())
	events, err := q.eventstore.Filter(ctx, org.SearchQuery(ctx))
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, errors.ThrowNotFound(err, "QUERY-iTTGJ", "Errors.Org.NotFound")
	}
	org.Reduce(events)

	return mapOrg(org), nil
}

func mapOrg(org *projection.Org) *Org {
	return &Org{
		ID:            org.ID,
		CreationDate:  org.CreationDate,
		ChangeDate:    org.ChangeDate,
		ResourceOwner: org.ResourceOwner,
		State:         org.State,
		Sequence:      org.Sequence,
		Name:          org.Name,
		Domain:        org.Domain,
	}
}

func (q *Queries) OrgByPrimaryDomain(ctx context.Context, domain string) (_ *Org, err error) {
	ctx, span := tracing.NewSpan(ctx)
	defer func() { span.EndWithError(err) }()

	stmt, scan := prepareOrgQuery()
	query, args, err := stmt.Where(sq.Eq{
		OrgColumnDomain.identifier():     domain,
		OrgColumnInstanceID.identifier(): authz.GetInstance(ctx).InstanceID(),
	}).ToSql()
	if err != nil {
		return nil, errors.ThrowInternal(err, "QUERY-TYUCE", "Errors.Query.SQLStatement")
	}

	row := q.client.QueryRowContext(ctx, query, args...)
	return scan(row)
}

func (q *Queries) OrgByVerifiedDomain(ctx context.Context, domain string) (_ *Org, err error) {
	ctx, span := tracing.NewSpan(ctx)
	defer func() { span.EndWithError(err) }()

	stmt, scan := prepareOrgWithDomainsQuery()
	query, args, err := stmt.Where(sq.Eq{
		OrgDomainDomainCol.identifier():     domain,
		OrgDomainIsVerifiedCol.identifier(): true,
		OrgColumnInstanceID.identifier():    authz.GetInstance(ctx).InstanceID(),
	}).ToSql()
	if err != nil {
		return nil, errors.ThrowInternal(err, "QUERY-TYUCE", "Errors.Query.SQLStatement")
	}

	row := q.client.QueryRowContext(ctx, query, args...)
	return scan(row)
}

func (q *Queries) IsOrgUnique(ctx context.Context, name, domain string) (isUnique bool, err error) {
	ctx, span := tracing.NewSpan(ctx)
	defer func() { span.EndWithError(err) }()

	if name == "" && domain == "" {
		return false, errors.ThrowInvalidArgument(nil, "QUERY-DGqfd", "Errors.Query.InvalidRequest")
	}
	query, scan := prepareOrgUniqueQuery()
	stmt, args, err := query.Where(
		sq.And{
			sq.Eq{
				OrgColumnInstanceID.identifier(): authz.GetInstance(ctx).InstanceID(),
			},
			sq.Or{
				sq.Eq{
					OrgColumnDomain.identifier(): domain,
				},
				sq.Eq{
					OrgColumnName.identifier(): name,
				},
			},
		}).ToSql()
	if err != nil {
		return false, errors.ThrowInternal(err, "QUERY-Dgbe2", "Errors.Query.SQLStatement")
	}

	row := q.client.QueryRowContext(ctx, stmt, args...)
	return scan(row)
}

func (q *Queries) ExistsOrg(ctx context.Context, id string) (err error) {
	ctx, span := tracing.NewSpan(ctx)
	defer func() { span.EndWithError(err) }()

	_, err = q.OrgByID(ctx, true, id)
	return err
}

func (q *Queries) SearchOrgs(ctx context.Context, queries *OrgSearchQueries) (orgs *Orgs, err error) {
	ctx, span := tracing.NewSpan(ctx)
	defer func() { span.EndWithError(err) }()

	query, scan := prepareOrgsQuery()
	stmt, args, err := queries.toQuery(query).
		Where(sq.Eq{
			OrgColumnInstanceID.identifier(): authz.GetInstance(ctx).InstanceID(),
		}).ToSql()
	if err != nil {
		return nil, errors.ThrowInvalidArgument(err, "QUERY-wQ3by", "Errors.Query.InvalidRequest")
	}

	rows, err := q.client.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, errors.ThrowInternal(err, "QUERY-M6mYN", "Errors.Internal")
	}
	orgs, err = scan(rows)
	if err != nil {
		return nil, err
	}
	orgs.LatestSequence, err = q.latestSequence(ctx, orgsTable)
	return orgs, err
}

func NewOrgDomainSearchQuery(method TextComparison, value string) (SearchQuery, error) {
	return NewTextQuery(OrgColumnDomain, value, method)
}

func NewOrgNameSearchQuery(method TextComparison, value string) (SearchQuery, error) {
	return NewTextQuery(OrgColumnName, value, method)
}

func NewOrgIDsSearchQuery(ids ...string) (SearchQuery, error) {
	list := make([]interface{}, len(ids))
	for i, value := range ids {
		list[i] = value
	}
	return NewListQuery(OrgColumnID, list, ListIn)
}

func prepareOrgsQuery() (sq.SelectBuilder, func(*sql.Rows) (*Orgs, error)) {
	return sq.Select(
			OrgColumnID.identifier(),
			OrgColumnCreationDate.identifier(),
			OrgColumnChangeDate.identifier(),
			OrgColumnResourceOwner.identifier(),
			OrgColumnState.identifier(),
			OrgColumnSequence.identifier(),
			OrgColumnName.identifier(),
			OrgColumnDomain.identifier(),
			countColumn.identifier()).
			From(orgsTable.identifier()).PlaceholderFormat(sq.Dollar),
		func(rows *sql.Rows) (*Orgs, error) {
			orgs := make([]*Org, 0)
			var count uint64
			for rows.Next() {
				org := new(Org)
				err := rows.Scan(
					&org.ID,
					&org.CreationDate,
					&org.ChangeDate,
					&org.ResourceOwner,
					&org.State,
					&org.Sequence,
					&org.Name,
					&org.Domain,
					&count,
				)
				if err != nil {
					return nil, err
				}
				orgs = append(orgs, org)
			}

			if err := rows.Close(); err != nil {
				return nil, errors.ThrowInternal(err, "QUERY-QMXJv", "Errors.Query.CloseRows")
			}

			return &Orgs{
				Orgs: orgs,
				SearchResponse: SearchResponse{
					Count: count,
				},
			}, nil
		}
}

func prepareOrgQuery() (sq.SelectBuilder, func(*sql.Row) (*Org, error)) {
	return sq.Select(
			OrgColumnID.identifier(),
			OrgColumnCreationDate.identifier(),
			OrgColumnChangeDate.identifier(),
			OrgColumnResourceOwner.identifier(),
			OrgColumnState.identifier(),
			OrgColumnSequence.identifier(),
			OrgColumnName.identifier(),
			OrgColumnDomain.identifier(),
		).
			From(orgsTable.identifier()).PlaceholderFormat(sq.Dollar),
		func(row *sql.Row) (*Org, error) {
			o := new(Org)
			err := row.Scan(
				&o.ID,
				&o.CreationDate,
				&o.ChangeDate,
				&o.ResourceOwner,
				&o.State,
				&o.Sequence,
				&o.Name,
				&o.Domain,
			)
			if err != nil {
				if errs.Is(err, sql.ErrNoRows) {
					return nil, errors.ThrowNotFound(err, "QUERY-iTTGJ", "Errors.Org.NotFound")
				}
				return nil, errors.ThrowInternal(err, "QUERY-pWS5H", "Errors.Internal")
			}
			return o, nil
		}
}

func prepareOrgWithDomainsQuery() (sq.SelectBuilder, func(*sql.Row) (*Org, error)) {
	return sq.Select(
			OrgColumnID.identifier(),
			OrgColumnCreationDate.identifier(),
			OrgColumnChangeDate.identifier(),
			OrgColumnResourceOwner.identifier(),
			OrgColumnState.identifier(),
			OrgColumnSequence.identifier(),
			OrgColumnName.identifier(),
			OrgColumnDomain.identifier(),
		).
			From(orgsTable.identifier()).
			LeftJoin(join(OrgDomainOrgIDCol, OrgColumnID)).
			PlaceholderFormat(sq.Dollar),
		func(row *sql.Row) (*Org, error) {
			o := new(Org)
			err := row.Scan(
				&o.ID,
				&o.CreationDate,
				&o.ChangeDate,
				&o.ResourceOwner,
				&o.State,
				&o.Sequence,
				&o.Name,
				&o.Domain,
			)
			if err != nil {
				if errs.Is(err, sql.ErrNoRows) {
					return nil, errors.ThrowNotFound(err, "QUERY-iTTGJ", "Errors.Org.NotFound")
				}
				return nil, errors.ThrowInternal(err, "QUERY-pWS5H", "Errors.Internal")
			}
			return o, nil
		}
}

func prepareOrgUniqueQuery() (sq.SelectBuilder, func(*sql.Row) (bool, error)) {
	return sq.Select(uniqueColumn.identifier()).
			From(orgsTable.identifier()).PlaceholderFormat(sq.Dollar),
		func(row *sql.Row) (isUnique bool, err error) {
			err = row.Scan(&isUnique)
			if err != nil {
				return false, errors.ThrowInternal(err, "QUERY-e6EiG", "Errors.Internal")
			}
			return isUnique, err
		}
}
