package projection

import (
	"context"

	"github.com/zitadel/zitadel/internal/eventstore"
	old_handler "github.com/zitadel/zitadel/internal/eventstore/handler"
	"github.com/zitadel/zitadel/internal/eventstore/handler/v2"
	exec "github.com/zitadel/zitadel/internal/repository/execution"
	"github.com/zitadel/zitadel/internal/repository/instance"
	"github.com/zitadel/zitadel/internal/zerrors"
)

const (
	ExecutionTable            = "projections.executions"
	ExecutionIDCol            = "id"
	ExecutionCreationDateCol  = "creation_date"
	ExecutionChangeDateCol    = "change_date"
	ExecutionResourceOwnerCol = "resource_owner"
	ExecutionInstanceIDCol    = "instance_id"
	ExecutionSequenceCol      = "sequence"
	ExecutionTargetsCol       = "targets"
	ExecutionIncludesCol      = "includes"
)

type executionProjection struct{}

func newExecutionProjection(ctx context.Context, config handler.Config) *handler.Handler {
	return handler.NewHandler(ctx, &config, new(executionProjection))
}

func (*executionProjection) Name() string {
	return ExecutionTable
}

func (*executionProjection) Init() *old_handler.Check {
	return handler.NewTableCheck(
		handler.NewTable([]*handler.InitColumn{
			handler.NewColumn(ExecutionIDCol, handler.ColumnTypeText),
			handler.NewColumn(ExecutionCreationDateCol, handler.ColumnTypeTimestamp),
			handler.NewColumn(ExecutionChangeDateCol, handler.ColumnTypeTimestamp),
			handler.NewColumn(ExecutionResourceOwnerCol, handler.ColumnTypeText),
			handler.NewColumn(ExecutionInstanceIDCol, handler.ColumnTypeText),
			handler.NewColumn(ExecutionSequenceCol, handler.ColumnTypeInt64),
			handler.NewColumn(ExecutionTargetsCol, handler.ColumnTypeTextArray, handler.Nullable()),
			handler.NewColumn(ExecutionIncludesCol, handler.ColumnTypeTextArray, handler.Nullable()),
		},
			handler.NewPrimaryKey(ExecutionInstanceIDCol, ExecutionResourceOwnerCol, ExecutionIDCol),
		),
	)
}

func (p *executionProjection) Reducers() []handler.AggregateReducer {
	return []handler.AggregateReducer{
		{
			Aggregate: exec.AggregateType,
			EventReducers: []handler.EventReducer{
				{
					Event:  exec.SetEventType,
					Reduce: p.reduceExecutionSet,
				},
				{
					Event:  exec.RemovedEventType,
					Reduce: p.reduceExecutionRemoved,
				},
			},
		},
		{
			Aggregate: instance.AggregateType,
			EventReducers: []handler.EventReducer{
				{
					Event:  instance.InstanceRemovedEventType,
					Reduce: reduceInstanceRemovedHelper(ExecutionInstanceIDCol),
				},
			},
		},
	}
}

func (p *executionProjection) reduceExecutionSet(event eventstore.Event) (*handler.Statement, error) {
	e, ok := event.(*exec.SetEvent)
	if !ok {
		return nil, zerrors.ThrowInvalidArgumentf(nil, "HANDL-jiozfjitr5", "reduce.wrong.event.type% s", exec.SetEventType)
	}
	columns := []handler.Column{
		handler.NewCol(ExecutionInstanceIDCol, e.Aggregate().InstanceID),
		handler.NewCol(ExecutionResourceOwnerCol, e.Aggregate().ResourceOwner),
		handler.NewCol(ExecutionIDCol, e.Aggregate().ID),
		handler.NewCol(ExecutionCreationDateCol, handler.OnlySetValueOnInsert(ExecutionTable, e.CreationDate())),
		handler.NewCol(ExecutionChangeDateCol, e.CreationDate()),
		handler.NewCol(ExecutionSequenceCol, e.Sequence()),
		handler.NewCol(ExecutionTargetsCol, e.Targets),
		handler.NewCol(ExecutionIncludesCol, e.Includes),
	}
	return handler.NewUpsertStatement(e, columns[0:3], columns), nil
}

func (p *executionProjection) reduceExecutionRemoved(event eventstore.Event) (*handler.Statement, error) {
	e, ok := event.(*exec.RemovedEvent)
	if !ok {
		return nil, zerrors.ThrowInvalidArgumentf(nil, "HANDL-t51ipx55r2", "reduce.wrong.event.type %s", exec.RemovedEventType)
	}
	return handler.NewDeleteStatement(
		e,
		[]handler.Condition{
			handler.NewCond(ExecutionInstanceIDCol, e.Aggregate().InstanceID),
			handler.NewCond(ExecutionResourceOwnerCol, e.Aggregate().ResourceOwner),
			handler.NewCond(ExecutionIDCol, e.Aggregate().ID),
		},
	), nil
}
