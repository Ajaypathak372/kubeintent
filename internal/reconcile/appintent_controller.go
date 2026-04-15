package reconcile

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/ajaypathak/kubeintent/api/v1alpha1"
	"github.com/ajaypathak/kubeintent/internal/costmodel"
	"github.com/ajaypathak/kubeintent/internal/telemetry"
)

const (
	managedLabelKey = "kubeintent.io/managed"
	appIntentLabel  = "kubeintent.io/app-intent"
	reactCooldown   = 2 * time.Minute
)

type AppIntentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Telemetry telemetry.TelemetryProvider
	CostModel *costmodel.CostModel

	lastActionMu sync.Mutex
	lastAction   map[types.NamespacedName]time.Time
}

func (r *AppIntentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.AppIntent{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Complete(r)
}

func (r *AppIntentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var intent platformv1alpha1.AppIntent
	if err := r.Get(ctx, req.NamespacedName, &intent); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if intent.Spec.TargetRef.Kind != "Deployment" || intent.Spec.TargetRef.APIVersion != "apps/v1" {
		return r.markDegraded(ctx, &intent, "UnsupportedTarget", "only apps/v1 Deployment is supported in v0.1")
	}

	var target appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: intent.Namespace, Name: intent.Spec.TargetRef.Name}, &target); err != nil {
		if errors.IsNotFound(err) {
			return r.markDegraded(ctx, &intent, "TargetNotFound", err.Error())
		}
		return ctrl.Result{}, err
	}

	effectiveAutoscaling, effectivePolicy := r.effectivePolicy(ctx, &intent)
	labels := map[string]string{
		managedLabelKey: "true",
		appIntentLabel:  intent.Name,
	}

	if err := r.reconcilePDB(ctx, &intent, labels); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileNetworkPolicy(ctx, &intent, effectivePolicy, labels); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileHPA(ctx, &intent, &target, effectiveAutoscaling, labels); err != nil {
		return ctrl.Result{}, err
	}

	// Emit Materialized event only on the first successful materialization
	// (i.e. PolicyApplied was not yet True).
	prevApplied := meta.FindStatusCondition(intent.Status.Conditions, "PolicyApplied")
	if r.Recorder != nil && (prevApplied == nil || prevApplied.Status != metav1.ConditionTrue) {
		r.Recorder.Event(&intent, corev1.EventTypeNormal, "Materialized",
			"PDB, NetworkPolicy, and HPA are in desired state")
	}

	r.observe(ctx, &intent, &target, effectivePolicy)
	r.react(ctx, &intent, effectivePolicy, effectiveAutoscaling)

	// Track previous compliance to detect transitions for events.
	prevCompliance := ""
	if prev := meta.FindStatusCondition(intent.Status.Conditions, "IntentMet"); prev != nil {
		if prev.Status == metav1.ConditionTrue {
			prevCompliance = "Meeting"
		} else {
			prevCompliance = prev.Reason // "AtRisk" or "Violating"
		}
	}

	now := metav1.Now()

	setCondition(&intent.Status.Conditions, metav1.Condition{
		Type:    "PolicyApplied",
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciled",
		Message: "Managed resources are in desired state",
	})
	setCondition(&intent.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Healthy",
		Message: "AppIntent successfully reconciled",
	})

	// IntentMet condition: reflects whether the workload meets its declared intent.
	compliance := "Unknown"
	if intent.Status.Compliance != nil {
		compliance = intent.Status.Compliance.Overall
	}
	switch compliance {
	case "Meeting":
		setCondition(&intent.Status.Conditions, metav1.Condition{
			Type:    "IntentMet",
			Status:  metav1.ConditionTrue,
			Reason:  "Meeting",
			Message: "Workload meets all declared intent targets",
		})
	case "AtRisk":
		setCondition(&intent.Status.Conditions, metav1.Condition{
			Type:    "IntentMet",
			Status:  metav1.ConditionFalse,
			Reason:  "AtRisk",
			Message: "Workload approaching intent violation thresholds",
		})
	case "Violating":
		setCondition(&intent.Status.Conditions, metav1.Condition{
			Type:    "IntentMet",
			Status:  metav1.ConditionFalse,
			Reason:  "Violating",
			Message: "Workload is violating declared intent targets",
		})
	default:
		setCondition(&intent.Status.Conditions, metav1.Condition{
			Type:    "IntentMet",
			Status:  metav1.ConditionUnknown,
			Reason:  "NoTelemetry",
			Message: "Telemetry not yet available",
		})
	}

	// Record events on state transitions.
	r.recordTransitionEvents(&intent, prevCompliance, compliance, now)

	intent.Status.ObservedGeneration = intent.Generation

	desiredStatus := intent.Status
	if err := r.Status().Update(ctx, &intent); err != nil {
		if errors.IsConflict(err) {
			logger.Info("status update conflict, retrying once")
			if err := r.Get(ctx, req.NamespacedName, &intent); err != nil {
				return ctrl.Result{}, err
			}
			intent.Status = desiredStatus
			if err := r.Status().Update(ctx, &intent); err != nil {
				logger.Error(err, "updating AppIntent status (retry)")
				return ctrl.Result{}, err
			}
		} else {
			logger.Error(err, "updating AppIntent status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *AppIntentReconciler) effectivePolicy(ctx context.Context, intent *platformv1alpha1.AppIntent) (*platformv1alpha1.AutoscalingPolicy, *platformv1alpha1.IntentPolicy) {
	// Precedence: AppIntent > NamespaceIntent > RuntimeProfile with namespace guardrail constraints
	var profilePolicy platformv1alpha1.IntentPolicy
	if intent.Spec.RuntimeProfileRef != "" {
		var profile platformv1alpha1.RuntimeProfile
		if err := r.Get(ctx, types.NamespacedName{Name: intent.Spec.RuntimeProfileRef}, &profile); err == nil {
			profilePolicy.Availability = profile.Spec.Defaults.Availability
			profilePolicy.SecurityTier = profile.Spec.Defaults.SecurityTier
			profilePolicy.Autoscaling = profile.Spec.Defaults.Autoscaling
		}
	}

	var nsPolicy platformv1alpha1.IntentPolicy
	if nsIntent, _ := r.selectNamespaceIntent(ctx, intent.Namespace); nsIntent != nil {
		nsPolicy = nsIntent.Spec.Policy
	}

	candidate := profilePolicy
	candidate = mergeIntentPolicy(candidate, nsPolicy)
	candidate = mergeIntentPolicy(candidate, intent.Spec.Policy)
	effective := constrainIntentPolicy(candidate, nsPolicy)
	return effective.Autoscaling, &effective
}

func (r *AppIntentReconciler) selectNamespaceIntent(ctx context.Context, namespace string) (*platformv1alpha1.NamespaceIntent, error) {
	var niList platformv1alpha1.NamespaceIntentList
	if err := r.List(ctx, &niList, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	if len(niList.Items) == 0 {
		return nil, nil
	}
	sort.SliceStable(niList.Items, func(i, j int) bool {
		pi := int32(0)
		pj := int32(0)
		if niList.Items[i].Spec.Priority != nil {
			pi = *niList.Items[i].Spec.Priority
		}
		if niList.Items[j].Spec.Priority != nil {
			pj = *niList.Items[j].Spec.Priority
		}
		if pi == pj {
			return niList.Items[i].Name < niList.Items[j].Name
		}
		return pi > pj
	})
	return &niList.Items[0], nil
}

func mergeIntentPolicy(base, override platformv1alpha1.IntentPolicy) platformv1alpha1.IntentPolicy {
	out := base
	if override.Availability != "" {
		out.Availability = override.Availability
	}
	if override.SecurityTier != "" {
		out.SecurityTier = override.SecurityTier
	}
	if override.LatencyTargetMs != nil {
		out.LatencyTargetMs = override.LatencyTargetMs
	}
	if override.MaxMonthlyCostUSD != nil {
		out.MaxMonthlyCostUSD = override.MaxMonthlyCostUSD
	}
	if override.Autoscaling != nil {
		out.Autoscaling = override.Autoscaling
	}
	return out
}

func constrainIntentPolicy(candidate, guardrail platformv1alpha1.IntentPolicy) platformv1alpha1.IntentPolicy {
	out := candidate
	out.Autoscaling = constrainAutoscaling(out.Autoscaling, guardrail.Autoscaling)

	if securityRank(guardrail.SecurityTier) > securityRank(out.SecurityTier) {
		out.SecurityTier = guardrail.SecurityTier
	}
	if guardrail.MaxMonthlyCostUSD != nil {
		if out.MaxMonthlyCostUSD == nil || *out.MaxMonthlyCostUSD > *guardrail.MaxMonthlyCostUSD {
			v := *guardrail.MaxMonthlyCostUSD
			out.MaxMonthlyCostUSD = &v
		}
	}
	return out
}

func constrainAutoscaling(candidate *platformv1alpha1.AutoscalingPolicy, guardrail *platformv1alpha1.AutoscalingPolicy) *platformv1alpha1.AutoscalingPolicy {
	if candidate == nil {
		return guardrail
	}
	if guardrail == nil {
		return candidate
	}
	out := *candidate
	if guardrail.Enabled {
		out.Enabled = true
	}
	if guardrail.MinReplicas != nil {
		if out.MinReplicas == nil || *out.MinReplicas < *guardrail.MinReplicas {
			v := *guardrail.MinReplicas
			out.MinReplicas = &v
		}
	}
	if guardrail.MaxReplicas != nil {
		if out.MaxReplicas == nil || *out.MaxReplicas > *guardrail.MaxReplicas {
			v := *guardrail.MaxReplicas
			out.MaxReplicas = &v
		}
	}
	if out.MinReplicas != nil && out.MaxReplicas != nil && *out.MinReplicas > *out.MaxReplicas {
		v := *out.MinReplicas
		out.MaxReplicas = &v
	}
	return &out
}

func securityRank(tier string) int {
	switch tier {
	case "strict":
		return 3
	case "hardened":
		return 2
	case "baseline":
		return 1
	default:
		return 0
	}
}

func (r *AppIntentReconciler) reconcilePDB(ctx context.Context, intent *platformv1alpha1.AppIntent, labels map[string]string) error {
	name := fmt.Sprintf("%s-kubeintent-pdb", intent.Spec.TargetRef.Name)
	pdb := &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: intent.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pdb, func() error {
		pdb.Labels = mergeLabels(pdb.Labels, labels)
		if err := controllerutil.SetControllerReference(intent, pdb, r.Scheme); err != nil {
			return err
		}
		min := intstrFromInt(1)
		pdb.Spec.MinAvailable = &min
		pdb.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": intent.Spec.TargetRef.Name}}
		return nil
	})
	return err
}

func (r *AppIntentReconciler) reconcileNetworkPolicy(ctx context.Context, intent *platformv1alpha1.AppIntent, policy *platformv1alpha1.IntentPolicy, labels map[string]string) error {
	name := fmt.Sprintf("%s-kubeintent-netpol", intent.Spec.TargetRef.Name)
	np := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: intent.Namespace}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		np.Labels = mergeLabels(np.Labels, labels)
		if err := controllerutil.SetControllerReference(intent, np, r.Scheme); err != nil {
			return err
		}
		np.Spec.PodSelector = metav1.LabelSelector{MatchLabels: map[string]string{"app": intent.Spec.TargetRef.Name}}
		np.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
		securityTier := "baseline"
		if policy != nil && policy.SecurityTier != "" {
			securityTier = policy.SecurityTier
		}
		if securityTier == "strict" {
			// strict: restrict ingress to same-app only, deny-all egress
			np.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{
					{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": intent.Spec.TargetRef.Name}}},
				},
			}}
			np.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{}
		} else {
			// baseline/hardened: allow all ingress and egress (can be tightened in future)
			np.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
			np.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{}}
		}
		return nil
	})
	return err
}

func (r *AppIntentReconciler) reconcileHPA(ctx context.Context, intent *platformv1alpha1.AppIntent, target *appsv1.Deployment, scaling *platformv1alpha1.AutoscalingPolicy, labels map[string]string) error {
	name := fmt.Sprintf("%s-kubeintent-hpa", intent.Spec.TargetRef.Name)
	hpa := &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: intent.Namespace}}

	if scaling == nil || !scaling.Enabled {
		if err := r.Delete(ctx, hpa); err != nil && !errors.IsNotFound(err) {
			return err
		}
		return nil
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, hpa, func() error {
		hpa.Labels = mergeLabels(hpa.Labels, labels)
		if err := controllerutil.SetControllerReference(intent, hpa, r.Scheme); err != nil {
			return err
		}
		minReplicas := int32(2)
		specMax := int32(10)
		if scaling.MinReplicas != nil {
			minReplicas = *scaling.MinReplicas
		}
		if scaling.MaxReplicas != nil {
			specMax = *scaling.MaxReplicas
		}
		targetCPU := int32(70)
		if scaling.CPUUtilizationTargetPct != nil {
			targetCPU = *scaling.CPUUtilizationTargetPct
		}
		hpa.Spec.MinReplicas = &minReplicas
		// Set maxReplicas conservatively: start at minReplicas for new HPAs.
		// If the react phase has already bumped maxReplicas, preserve it
		// (but never exceed the spec ceiling).
		if hpa.Spec.MaxReplicas == 0 {
			// New HPA — start conservative.
			hpa.Spec.MaxReplicas = minReplicas
		}
		if hpa.Spec.MaxReplicas < minReplicas {
			hpa.Spec.MaxReplicas = minReplicas
		}
		if hpa.Spec.MaxReplicas > specMax {
			hpa.Spec.MaxReplicas = specMax
		}
		hpa.Spec.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       target.Name,
		}
		hpa.Spec.Metrics = []autoscalingv2.MetricSpec{{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: "cpu",
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &targetCPU,
				},
			},
		}}
		return nil
	})
	return err
}

func (r *AppIntentReconciler) markDegraded(ctx context.Context, intent *platformv1alpha1.AppIntent, reason, message string) (ctrl.Result, error) {
	setCondition(&intent.Status.Conditions, metav1.Condition{
		Type:    "Degraded",
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})
	setCondition(&intent.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	intent.Status.ObservedGeneration = intent.Generation
	if err := r.Status().Update(ctx, intent); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func mergeLabels(existing map[string]string, add map[string]string) map[string]string {
	if existing == nil {
		existing = map[string]string{}
	}
	for k, v := range add {
		existing[k] = v
	}
	return existing
}

// setCondition wraps meta.SetStatusCondition, letting it manage
// LastTransitionTime: the timestamp is bumped only when the Status
// actually changes, not on every reconcile.
func setCondition(conditions *[]metav1.Condition, c metav1.Condition) {
	if c.LastTransitionTime.IsZero() {
		c.LastTransitionTime = metav1.Now()
	}
	meta.SetStatusCondition(conditions, c)
}

// recordTransitionEvents emits Kubernetes events when compliance state
// changes between reconcile passes.
func (r *AppIntentReconciler) recordTransitionEvents(intent *platformv1alpha1.AppIntent, prev, current string, at metav1.Time) {
	if r.Recorder == nil || prev == current {
		return
	}
	switch current {
	case "Violating":
		r.Recorder.Event(intent, corev1.EventTypeWarning, "IntentViolated",
			"Compliance transitioned to Violating — workload is breaching declared intent")
	case "AtRisk":
		r.Recorder.Event(intent, corev1.EventTypeWarning, "IntentAtRisk",
			"Compliance transitioned to AtRisk — workload approaching intent thresholds")
	case "Meeting":
		r.Recorder.Event(intent, corev1.EventTypeNormal, "IntentMet",
			"Compliance transitioned to Meeting — workload meets declared intent")
	}
}

func intstrFromInt(v int) intstr.IntOrString {
	return intstr.FromInt(v)
}

func (r *AppIntentReconciler) observe(ctx context.Context, intent *platformv1alpha1.AppIntent, target *appsv1.Deployment, effectivePolicy *platformv1alpha1.IntentPolicy) {
	logger := log.FromContext(ctx)

	obs := &platformv1alpha1.ObservedState{
		LastObservedAt: metav1.Now(),
		Latency:        &platformv1alpha1.Latency{},
	}

	var p99 time.Duration
	var p99OK, p50OK, rpsOK bool

	if r.Telemetry != nil {
		var err error
		p99, err = r.Telemetry.ObserveLatencyP99(ctx, intent)
		if err != nil {
			logger.Info("telemetry: p99 query failed", "error", err)
			obs.Latency.P99Display = "—"
		} else {
			p99OK = true
			obs.Latency.P99 = p99.String()
			obs.Latency.P99Display = p99.Truncate(time.Millisecond).String()
		}

		p50, err := r.Telemetry.ObserveLatencyP50(ctx, intent)
		if err != nil {
			logger.Info("telemetry: p50 query failed", "error", err)
			obs.Latency.P50 = "0s"
		} else {
			p50OK = true
			obs.Latency.P50 = p50.String()
		}

		rps, err := r.Telemetry.ObserveRPS(ctx, intent)
		if err != nil {
			logger.Info("telemetry: RPS query failed", "error", err)
			zero := float64(0)
			obs.ObservedRPS = &zero
		} else {
			rpsOK = true
			obs.ObservedRPS = &rps
		}
	}

	// Replica count: prefer readyReplicas, fall back to spec.
	replicas := target.Status.ReadyReplicas
	if replicas == 0 && target.Spec.Replicas != nil {
		replicas = *target.Spec.Replicas
	}
	obs.CurrentReplicas = &replicas

	// Cost projection from first container's resource requests.
	if r.CostModel != nil && len(target.Spec.Template.Spec.Containers) > 0 {
		c := target.Spec.Template.Spec.Containers[0]
		cpuReq := c.Resources.Requests[corev1.ResourceCPU]
		memReq := c.Resources.Requests[corev1.ResourceMemory]
		cost, err := r.CostModel.ProjectMonthlyCost(cpuReq, memReq, replicas)
		if err != nil {
			logger.Info("cost projection failed", "error", err)
		} else {
			obs.CurrentMonthlyCostUSD = &cost
			obs.CostDisplay = fmt.Sprintf("$%.2f", cost)
		}
	}

	intent.Status.ObservedState = obs

	telemetryAvailable := p99OK || p50OK || rpsOK
	intent.Status.Compliance = computeCompliance(effectivePolicy, obs, p99OK, p99, telemetryAvailable)

	// Structured log line per observation.
	var costUSD float64
	if obs.CurrentMonthlyCostUSD != nil {
		costUSD = *obs.CurrentMonthlyCostUSD
	}
	complianceStr := "Unknown"
	if intent.Status.Compliance != nil {
		complianceStr = intent.Status.Compliance.Overall
	}
	logger.Info("observed workload state",
		"intent", intent.Name,
		"p99", obs.Latency.P99,
		"replicas", replicas,
		"costUSD", fmt.Sprintf("%.2f", costUSD),
		"compliance", complianceStr,
	)
}

func computeCompliance(policy *platformv1alpha1.IntentPolicy, obs *platformv1alpha1.ObservedState, p99OK bool, p99 time.Duration, telemetryAvailable bool) *platformv1alpha1.Compliance {
	if !telemetryAvailable {
		return &platformv1alpha1.Compliance{Overall: "Unknown"}
	}

	overall := "Meeting"

	// Latency: compare observed p99 against the effective target.
	if p99OK && policy != nil && policy.LatencyTargetMs != nil {
		targetMs := float64(*policy.LatencyTargetMs)
		observedMs := float64(p99.Milliseconds())
		if observedMs > targetMs {
			overall = complianceWorst(overall, "Violating")
		} else if observedMs > targetMs*0.9 {
			overall = complianceWorst(overall, "AtRisk")
		}
	}

	// Cost: compare projected monthly cost against the effective budget.
	if policy != nil && policy.MaxMonthlyCostUSD != nil && obs.CurrentMonthlyCostUSD != nil {
		budget := *policy.MaxMonthlyCostUSD
		cost := *obs.CurrentMonthlyCostUSD
		if cost > budget {
			overall = complianceWorst(overall, "Violating")
		} else if cost > budget*0.9 {
			overall = complianceWorst(overall, "AtRisk")
		}
	}

	return &platformv1alpha1.Compliance{Overall: overall}
}

func complianceWorst(a, b string) string {
	rank := map[string]int{"Meeting": 0, "AtRisk": 1, "Violating": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// react takes action based on observed compliance violations.
// Current actions:
//   - Latency violation: bump HPA maxReplicas by 1 (rate-limited)
//   - Cost violation: record Blocked decision (hard constraint)
//   - Both: cost takes precedence (block)
func (r *AppIntentReconciler) react(ctx context.Context, intent *platformv1alpha1.AppIntent, effectivePolicy *platformv1alpha1.IntentPolicy, effectiveAutoscaling *platformv1alpha1.AutoscalingPolicy) {
	logger := log.FromContext(ctx)

	if intent.Status.Compliance == nil {
		return
	}
	overall := intent.Status.Compliance.Overall
	if overall == "Meeting" || overall == "Unknown" {
		return
	}

	// Determine violation causes.
	latencyViolation := false
	costViolation := false

	if intent.Status.ObservedState != nil && effectivePolicy != nil {
		if effectivePolicy.LatencyTargetMs != nil && intent.Status.ObservedState.Latency != nil {
			p99, err := time.ParseDuration(intent.Status.ObservedState.Latency.P99)
			if err == nil {
				targetMs := float64(*effectivePolicy.LatencyTargetMs)
				if float64(p99.Milliseconds()) > targetMs {
					latencyViolation = true
				}
			}
		}
		if effectivePolicy.MaxMonthlyCostUSD != nil && intent.Status.ObservedState.CurrentMonthlyCostUSD != nil {
			if *intent.Status.ObservedState.CurrentMonthlyCostUSD > *effectivePolicy.MaxMonthlyCostUSD {
				costViolation = true
			}
		}
	}

	now := metav1.Now()

	// Cost violation: always block — hard constraint, never scale into budget overrun.
	if costViolation {
		decision := platformv1alpha1.Decision{
			ID:     fmt.Sprintf("react-%d", now.UnixMilli()),
			At:     now,
			Action: "Blocked",
			Reason: "Monthly cost exceeds budget; scaling inhibited",
		}
		if effectivePolicy.MaxMonthlyCostUSD != nil {
			decision.Inputs = map[string]string{
				"budget": fmt.Sprintf("%.2f", *effectivePolicy.MaxMonthlyCostUSD),
				"actual": fmt.Sprintf("%.2f", *intent.Status.ObservedState.CurrentMonthlyCostUSD),
			}
		}
		intent.Status.AppendDecision(decision)
		if r.Recorder != nil {
			r.Recorder.Eventf(intent, corev1.EventTypeWarning, "ActionBlocked",
				"Scaling blocked: cost $%s exceeds budget $%s", decision.Inputs["actual"], decision.Inputs["budget"])
		}
		logger.Info("react: blocked by cost violation",
			"budget", decision.Inputs["budget"],
			"actual", decision.Inputs["actual"],
		)
		return
	}

	// Rate limit: one react decision per intent per cooldown (applies to all paths).
	key := types.NamespacedName{Namespace: intent.Namespace, Name: intent.Name}
	r.lastActionMu.Lock()
	if r.lastAction == nil {
		r.lastAction = make(map[types.NamespacedName]time.Time)
	}
	last, hasLast := r.lastAction[key]
	r.lastActionMu.Unlock()
	if hasLast && time.Since(last) < reactCooldown {
		return
	}

	// Only proceed if there's an actionable violation.
	if !latencyViolation && !costViolation {
		return
	}

	// Mark this react evaluation so cooldown applies to all decision paths.
	r.lastActionMu.Lock()
	r.lastAction[key] = time.Now()
	r.lastActionMu.Unlock()

	// Latency violation: try to scale up.
	if latencyViolation {
		// Ensure HPA exists and is managed by us.
		if effectiveAutoscaling == nil || !effectiveAutoscaling.Enabled {
			decision := platformv1alpha1.Decision{
				ID:     fmt.Sprintf("react-%d", now.UnixMilli()),
				At:     now,
				Action: "NoOp",
				Reason: "Latency violation but autoscaling is disabled",
			}
			intent.Status.AppendDecision(decision)
			logger.Info("react: autoscaling disabled, cannot scale")
			return
		}

		hpaName := fmt.Sprintf("%s-kubeintent-hpa", intent.Spec.TargetRef.Name)
		var hpa autoscalingv2.HorizontalPodAutoscaler
		if err := r.Get(ctx, types.NamespacedName{Namespace: intent.Namespace, Name: hpaName}, &hpa); err != nil {
			logger.Info("react: HPA not found, skipping", "error", err)
			return
		}

		currentMax := hpa.Spec.MaxReplicas
		newMax := currentMax + 1

		// Respect namespace guardrail on maxReplicas.
		if effectiveAutoscaling.MaxReplicas != nil && newMax > *effectiveAutoscaling.MaxReplicas {
			newMax = *effectiveAutoscaling.MaxReplicas
		}
		if newMax == currentMax {
			decision := platformv1alpha1.Decision{
				ID:     fmt.Sprintf("react-%d", now.UnixMilli()),
				At:     now,
				Action: "NoOp",
				Reason: fmt.Sprintf("Already at maxReplicas ceiling (%d)", currentMax),
			}
			intent.Status.AppendDecision(decision)
			logger.Info("react: at maxReplicas ceiling", "maxReplicas", currentMax)
			return
		}

		// Forward-looking cost guard: would newMax exceed cost budget?
		if r.CostModel != nil && effectivePolicy != nil && effectivePolicy.MaxMonthlyCostUSD != nil {
			if len(intent.Spec.TargetRef.Name) > 0 {
				var target appsv1.Deployment
				if err := r.Get(ctx, types.NamespacedName{Namespace: intent.Namespace, Name: intent.Spec.TargetRef.Name}, &target); err == nil {
					if len(target.Spec.Template.Spec.Containers) > 0 {
						c := target.Spec.Template.Spec.Containers[0]
						cpuReq := c.Resources.Requests[corev1.ResourceCPU]
						memReq := c.Resources.Requests[corev1.ResourceMemory]
						projectedCost, err := r.CostModel.ProjectMonthlyCost(cpuReq, memReq, newMax)
						if err == nil && projectedCost > *effectivePolicy.MaxMonthlyCostUSD {
							decision := platformv1alpha1.Decision{
								ID:     fmt.Sprintf("react-%d", now.UnixMilli()),
								At:     now,
								Action: "Blocked",
								Reason: fmt.Sprintf("Scaling to %d replicas would exceed cost budget ($%.2f > $%.2f)", newMax, projectedCost, *effectivePolicy.MaxMonthlyCostUSD),
								Inputs: map[string]string{
									"projectedCost": fmt.Sprintf("%.2f", projectedCost),
									"budget":        fmt.Sprintf("%.2f", *effectivePolicy.MaxMonthlyCostUSD),
								},
							}
							intent.Status.AppendDecision(decision)
							if r.Recorder != nil {
								r.Recorder.Eventf(intent, corev1.EventTypeWarning, "ActionBlocked",
									"Scaling to %d replicas blocked: projected cost $%.2f exceeds budget $%.2f", newMax, projectedCost, *effectivePolicy.MaxMonthlyCostUSD)
							}
							logger.Info("react: blocked by forward cost guard",
								"projectedCost", projectedCost,
								"budget", *effectivePolicy.MaxMonthlyCostUSD,
							)
							return
						}
					}
				}
			}
		}

		// Patch HPA maxReplicas.
		patch := client.MergeFrom(hpa.DeepCopy())
		hpa.Spec.MaxReplicas = newMax
		if err := r.Patch(ctx, &hpa, patch); err != nil {
			logger.Error(err, "react: failed to patch HPA maxReplicas")
			return
		}

		decision := platformv1alpha1.Decision{
			ID:        fmt.Sprintf("react-%d", now.UnixMilli()),
			At:        now,
			Action:    "ScaleReplicas",
			Reason:    "Latency p99 exceeds target; increased HPA maxReplicas",
			FromValue: fmt.Sprintf("%d", currentMax),
			ToValue:   fmt.Sprintf("%d", newMax),
			Inputs: map[string]string{
				"observedP99": intent.Status.ObservedState.Latency.P99,
				"targetMs":    fmt.Sprintf("%d", *effectivePolicy.LatencyTargetMs),
			},
		}
		intent.Status.AppendDecision(decision)
		if r.Recorder != nil {
			r.Recorder.Eventf(intent, corev1.EventTypeNormal, "DecisionMade",
				"ScaleReplicas: HPA maxReplicas %d -> %d (p99 %s > %dms target)",
				currentMax, newMax, intent.Status.ObservedState.Latency.P99, *effectivePolicy.LatencyTargetMs)
		}
		logger.Info("react: scaled HPA maxReplicas",
			"from", currentMax,
			"to", newMax,
			"reason", "latency violation",
		)
	}
}
