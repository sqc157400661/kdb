package main

import (
	//+kubebuilder:scaffold:imports
	"fmt"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/sqc157400661/helper/kube"
	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/version"
	conf "github.com/sqc157400661/kdb/pkg/config"
	"github.com/sqc157400661/kdb/pkg/controller"
	"github.com/sqc157400661/kdb/pkg/featuregate"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"strings"
)

var setupLog = ctrl.Log.WithName("setup")

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Long:  `Print version of DB-Operator`,
	Run: func(cmd *cobra.Command, args []string) {
		version.PrintVersionInfo()
	},
}

type Options struct {
	MetricsAddr             string
	ProbeAddr               string
	ListenPort              int
	WebhookListenPort       int
	LeaderElection          bool
	LeaderElectionNamespace string
	MaxConcurrentReconciles int
	CertDir                 string
	FeatureGates            string
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{
		Development: true,
	})))
	cmd := newRootCmd()
	if err := cmd.Execute(); err != nil {
		setupLog.Error(err, "newRootCmd err")
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var rootCmd = &cobra.Command{
		Use:          "manager",
		Short:        "manages KDB-Operator",
		Long:         `manages KDB-Operator`,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		Version:      fmt.Sprintf("%#v", version.CurrentVersion),
	}

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(NewOperatorCommand())
	return rootCmd
}

func NewOperatorCommand() *cobra.Command {
	var operatorOptions = &Options{}
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Run DB-Operator",
		Long:  `Run DB-Operator`,
		Run: func(cmd *cobra.Command, args []string) {
			var err error
			var scheme *runtime.Scheme
			scheme, err = createScheme()
			if err != nil {
				setupLog.Error(err, "createScheme err")
				utilruntime.Must(err)
			}
			mgr, err := createManager(operatorOptions, scheme)
			if err != nil {
				setupLog.Error(err, "unable to start manager")
				utilruntime.Must(err)
			}

			err = addControllersToManager(mgr)
			if err != nil {
				setupLog.Error(err, "unable to add controllers")
				utilruntime.Must(err)
			}
			if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
				setupLog.Error(err, "unable to set up health check")
				utilruntime.Must(err)
			}
			if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
				setupLog.Error(err, "unable to set up ready check")
				utilruntime.Must(err)
			}

			setupLog.Info("starting manager")
			if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
				setupLog.Error(err, "problem running manager")
				utilruntime.Must(err)
			}
		},
	}
	bindArgs(cmd.Flags(), operatorOptions)
	return cmd
}

func bindArgs(flags *pflag.FlagSet, option *Options) {
	// Bind options to arguments.
	flags.StringVar(&option.MetricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flags.StringVar(&option.ProbeAddr, "probe-address", ":8081", "The address the probe endpoint binds to.")
	flags.IntVar(&option.MaxConcurrentReconciles, "concurrency", 64, "The max concurrency of each controller.")
	flags.IntVar(&option.ListenPort, "listen-port", 9443, "The port for operator to listen.")
	flags.IntVar(&option.WebhookListenPort, "webhook-listen-port", 0, "The port for webhook to listen. If not specified, "+
		"webhooks will serve on operator's port. Set to -1 to disable the webhooks (for debug purpose).")
	flags.StringVar(&option.CertDir, "cert-dir", "/etc/operator/certs", "Directory that stores the cert files.")
	flags.BoolVar(&option.LeaderElection, "enable-leader-election", false, "Enable leader election for controller manager.")
	flags.StringVar(&option.LeaderElectionNamespace, "leader-election-namespace", "", "The namespace where leader election happens. "+
		"If not specified, the namespace where this operator's running is used.")
	flags.StringVar(&option.FeatureGates, "feature-gates", "", "Feature gates to enable.")
}

// createScheme creates a scheme containing the resource types required by the
// KDB Operator.  This includes any custom resource types specific to the KDB
// Operator, as well as any standard Kubernetes resource types.
func createScheme() (*runtime.Scheme, error) {
	// create a new scheme specifically for this manager
	pgoScheme := runtime.NewScheme()

	if err := clientgoscheme.AddToScheme(pgoScheme); err != nil {
		return nil, err
	}

	// add custom resource types to the default scheme
	if err := v1.AddToScheme(pgoScheme); err != nil {
		return nil, err
	}

	return pgoScheme, nil
}

// createManager creates a new controller runtime manager for the KDB Operator.  The
// manager returned is configured specifically for the KDB Operator, and includes any
// controllers that will be responsible for managing KDB dbs instance using the custom resource.
func createManager(opt *Options, scheme *runtime.Scheme) (manager.Manager, error) {
	// Enable feature gates.
	err := featuregate.AddAndSetFeatureGates(strings.ReplaceAll(opt.FeatureGates, " ", ""))
	if err != nil {
		return nil, err
	}
	return ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Namespace:               conf.K8SNamespace,
		MetricsBindAddress:      opt.MetricsAddr,
		Port:                    opt.ListenPort,
		HealthProbeBindAddress:  opt.ProbeAddr,
		CertDir:                 opt.CertDir,
		LeaderElection:          opt.LeaderElection,
		LeaderElectionNamespace: opt.LeaderElectionNamespace,
		LeaderElectionID:        "www.kdb.com",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
}

// addControllersToManager adds all KDB-Operator controllers to the provided controller
// runtime manager.
func addControllersToManager(mgr manager.Manager) (err error) {
	helper, err := kube.NewDefaultReconcileHelperWithManager(mgr)
	if err != nil {
		err = errors.Wrap(err, "Unable to new defaultReconcileHelper.")
		return
	}
	if err = (&controller.KDBInstanceReconciler{
		ReconcileHelper: helper,
		Owner:           controller.KDBInstanceControllerName,
		Recorder:        mgr.GetEventRecorderFor(controller.KDBInstanceControllerName),
		//Tracer:          otel.Tracer(postgrescluster.ControllerName),
	}).SetupWithManager(mgr); err != nil {
		err = errors.Wrap(err, "unable to create KDBInstance controller")
		return
	}
	return
}
