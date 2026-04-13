package main

import (
	"errors"
	"flag"
	"os"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	platformv1alpha1 "github.com/ajaypathak/kubeintent/api/v1alpha1"
	"github.com/ajaypathak/kubeintent/internal/costmodel"
	"github.com/ajaypathak/kubeintent/internal/reconcile"
	"github.com/ajaypathak/kubeintent/internal/telemetry"
)

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var prometheusURL string
	var costModelConfig string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&prometheusURL, "prometheus-url", "http://prometheus.monitoring.svc:9090", "Prometheus server URL for telemetry queries.")
	flag.StringVar(&costModelConfig, "cost-model-config", "/etc/kubeintent/cost-model.yaml", "Path to cost model node pool configuration file.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	var cm *costmodel.CostModel
	cm, err := costmodel.LoadFromFile(costModelConfig)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			setupLog.Info("cost model config not found, cost projection disabled", "path", costModelConfig)
		} else {
			setupLog.Error(err, "unable to load cost model config")
			os.Exit(1)
		}
	} else {
		for _, pool := range cm.NodePools {
			setupLog.Info("loaded node pool", "name", pool.Name, "hourlyUSD", pool.HourlyUSD, "cpuCores", pool.CPUCores, "memoryGB", pool.MemoryGB)
		}
	}

	scheme := clientgoscheme.Scheme
	_ = policyv1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	_ = autoscalingv2.AddToScheme(scheme)
	_ = platformv1alpha1.AddToScheme(scheme)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                ctrl.Options{}.Metrics,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "kubeintent.io",
	})
	if err != nil {
		os.Exit(1)
	}

	promClient, err := telemetry.NewPrometheusClient(prometheusURL)
	if err != nil {
		os.Exit(1)
	}

	if err = (&reconcile.AppIntentReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("kubeintent"),
		Telemetry: telemetry.NewPrometheusProvider(promClient),
		CostModel: cm,
	}).SetupWithManager(mgr); err != nil {
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		os.Exit(1)
	}
}
