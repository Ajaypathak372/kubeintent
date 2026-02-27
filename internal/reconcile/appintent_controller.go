package reconcile

import (
	"context"
	"fmt"
	"sort"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/ajaypathak/kubeintent/api/v1alpha1"
)

const (
	managedLabelKey = "kubeintent.io/managed"
	appIntentLabel  = "kubeintent.io/app-intent"
)

type AppIntentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

	metaSetCondition(&intent.Status.Conditions, metav1.Condition{
		Type:               "PolicyApplied",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            "Managed resources are in desired state",
		LastTransitionTime: metav1.Now(),
	})
	metaSetCondition(&intent.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Healthy",
		Message:            "AppIntent successfully reconciled",
		LastTransitionTime: metav1.Now(),
	})
	intent.Status.ObservedGeneration = intent.Generation
	if err := r.Status().Update(ctx, &intent); err != nil {
		logger.Error(err, "updating AppIntent status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
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
		np.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
			From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": intent.Spec.TargetRef.Name}}}},
		}}
		securityTier := "baseline"
		if policy != nil && policy.SecurityTier != "" {
			securityTier = policy.SecurityTier
		}
		if securityTier == "strict" {
			// deny-all egress for strict by default
			np.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{}
		} else {
			// baseline/hardened: allow egress (can be tightened in future)
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
		maxReplicas := int32(10)
		if scaling.MinReplicas != nil {
			minReplicas = *scaling.MinReplicas
		}
		if scaling.MaxReplicas != nil {
			maxReplicas = *scaling.MaxReplicas
		}
		targetCPU := int32(70)
		if scaling.CPUUtilizationTargetPct != nil {
			targetCPU = *scaling.CPUUtilizationTargetPct
		}
		hpa.Spec.MinReplicas = &minReplicas
		hpa.Spec.MaxReplicas = maxReplicas
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
	metaSetCondition(&intent.Status.Conditions, metav1.Condition{
		Type:               "Degraded",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
	metaSetCondition(&intent.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
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

func metaSetCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	meta.SetStatusCondition(conditions, condition)
}

func intstrFromInt(v int) intstr.IntOrString {
	return intstr.FromInt(v)
}
