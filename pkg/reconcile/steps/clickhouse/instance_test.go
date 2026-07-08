package clickhouse_test

import (
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	"github.com/sqc157400661/kdb/pkg/controller"
	reconcile_context "github.com/sqc157400661/kdb/pkg/reconcile/context"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps"
	clickhousesteps "github.com/sqc157400661/kdb/pkg/reconcile/steps/clickhouse"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps/mysql"
	"github.com/sqc157400661/kdb/pkg/reconcile/steps/pg"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ steps.InstanceStepper = (*clickhousesteps.InstanceStepManager)(nil)

func TestControllerRoutingSelectsExpectedStepManager(t *testing.T) {
	tests := []struct {
		name   string
		engine string
		want   any
	}{
		{
			name:   "mysql",
			engine: naming.MySQLEngine,
			want:   (*mysql.InstanceStepManager)(nil),
		},
		{
			name:   "clickhouse",
			engine: naming.ClickHouseEngine,
			want:   (*clickhousesteps.InstanceStepManager)(nil),
		},
		{
			name:   "postgresql",
			engine: naming.PostgresEngine,
			want:   (*pg.InstanceStepManager)(nil),
		},
		{
			name:   "pg",
			engine: naming.PostgresEnginePG,
			want:   (*pg.InstanceStepManager)(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &v1.KDBInstance{}
			instance.Spec.Engine = tt.engine

			got, err := controller.NewInstanceStepManager(instance)
			if err != nil {
				t.Fatalf("expected route for %s engine: %v", tt.engine, err)
			}

			switch tt.want.(type) {
			case *mysql.InstanceStepManager:
				if _, ok := got.(*mysql.InstanceStepManager); !ok {
					t.Fatalf("expected mysql step manager, got %T", got)
				}
			case *clickhousesteps.InstanceStepManager:
				if _, ok := got.(*clickhousesteps.InstanceStepManager); !ok {
					t.Fatalf("expected clickhouse step manager, got %T", got)
				}
			case *pg.InstanceStepManager:
				if _, ok := got.(*pg.InstanceStepManager); !ok {
					t.Fatalf("expected pg step manager, got %T", got)
				}
			}
		})
	}
}

func TestControllerRoutingRejectsUnknownEngine(t *testing.T) {
	instance := &v1.KDBInstance{}
	instance.Spec.Engine = "unknown"

	if _, err := controller.NewInstanceStepManager(instance); err == nil {
		t.Fatalf("expected unknown engine to be rejected")
	}
}

func TestClickHouseNoOpStepNamesAndPassBehavior(t *testing.T) {
	manager := &clickhousesteps.InstanceStepManager{}
	tests := []struct {
		name string
		bind func() kube.BindFunc
	}{
		{name: "SetGlobalConfig", bind: manager.SetGlobalConfig},
		{name: "EnsureLeader", bind: manager.EnsureLeader},
		{name: "SetRbac", bind: manager.SetRbac},
		{name: "SetMonitor", bind: manager.SetMonitor},
		{name: "ScaleDownInstance", bind: manager.ScaleDownInstance},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extracted := kube.ExtractStepsFromBindFunc(tt.bind())
			if len(extracted) != 1 {
				t.Fatalf("expected one step, got %d", len(extracted))
			}
			if !strings.Contains(extracted[0].Name(), "ClickHouse") {
				t.Fatalf("expected ClickHouse in step name, got %q", extracted[0].Name())
			}

			flow := &passFlow{}
			result, err := extracted[0].Execute((*reconcile_context.InstanceContext)(nil), flow)
			if err != nil {
				t.Fatalf("expected no-op step to pass without error: %v", err)
			}
			if result != (reconcile.Result{}) {
				t.Fatalf("expected empty pass result, got %#v", result)
			}
			if flow.passCalls != 1 {
				t.Fatalf("expected one Pass call, got %d", flow.passCalls)
			}
		})
	}
}

func TestClickHouseEnsureLeaderAndProxySQLAreSafeNoOps(t *testing.T) {
	manager := &clickhousesteps.InstanceStepManager{}
	for _, bind := range []kube.BindFunc{
		manager.EnsureLeader(),
	} {
		step := kube.ExtractStepsFromBindFunc(bind)[0]
		flow := &passFlow{}
		if _, err := step.Execute((*reconcile_context.InstanceContext)(nil), flow); err != nil {
			t.Fatalf("expected %s to be a safe no-op: %v", step.Name(), err)
		}
		if flow.passCalls != 1 {
			t.Fatalf("expected %s to return Pass once, got %d", step.Name(), flow.passCalls)
		}
	}
}

type passFlow struct {
	passCalls int
}

func (f *passFlow) Logger() logr.Logger {
	return logr.Discard()
}

func (f *passFlow) RetryAfter(duration time.Duration, msg string, kvs ...interface{}) (reconcile.Result, error) {
	return reconcile.Result{RequeueAfter: duration}, nil
}

func (f *passFlow) Retry(msg string, kvs ...interface{}) (reconcile.Result, error) {
	return reconcile.Result{Requeue: true}, nil
}

func (f *passFlow) Continue(msg string, kvs ...interface{}) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (f *passFlow) Pass() (reconcile.Result, error) {
	f.passCalls++
	return reconcile.Result{}, nil
}

func (f *passFlow) Wait(msg string, kvs ...interface{}) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (f *passFlow) Break(msg string, kvs ...interface{}) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (f *passFlow) Error(err error, msg string, kvs ...interface{}) (reconcile.Result, error) {
	return reconcile.Result{}, err
}

func (f *passFlow) RetryErr(err error, msg string, kvs ...interface{}) (reconcile.Result, error) {
	return reconcile.Result{RequeueAfter: time.Second}, nil
}

func (f *passFlow) WithLogger(log logr.Logger) kube.Flow {
	return f
}

func (f *passFlow) WithLoggerValues(keyAndValues ...interface{}) kube.Flow {
	return f
}
