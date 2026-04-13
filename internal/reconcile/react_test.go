package reconcile

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	platformv1alpha1 "github.com/ajaypathak/kubeintent/api/v1alpha1"
)

func TestReact_ViolationThenRateLimit(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = autoscalingv2.AddToScheme(scheme)
	_ = platformv1alpha1.AddToScheme(scheme)

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-kubeintent-hpa",
			Namespace: "default",
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MaxReplicas: 5,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "myapp",
			},
		},
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "app",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
					}},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hpa, deploy).
		Build()

	r := &AppIntentReconciler{
		Client: cl,
		Scheme: scheme,
	}

	targetMs := int32(80)
	maxReplicas := int32(10)
	effectivePolicy := &platformv1alpha1.IntentPolicy{
		LatencyTargetMs: &targetMs,
	}
	effectiveAutoscaling := &platformv1alpha1.AutoscalingPolicy{
		Enabled:     true,
		MaxReplicas: &maxReplicas,
	}

	intent := &platformv1alpha1.AppIntent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp",
			Namespace: "default",
		},
		Spec: platformv1alpha1.AppIntentSpec{
			TargetRef: platformv1alpha1.TargetRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "myapp",
			},
		},
		Status: platformv1alpha1.AppIntentStatus{
			Compliance: &platformv1alpha1.Compliance{Overall: "Violating"},
			ObservedState: &platformv1alpha1.ObservedState{
				Latency: &platformv1alpha1.Latency{
					P99: (143 * time.Millisecond).String(),
				},
			},
		},
	}

	ctx := context.Background()

	// First call: should scale HPA maxReplicas from 5 to 6.
	r.react(ctx, intent, effectivePolicy, effectiveAutoscaling)

	if len(intent.Status.Decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(intent.Status.Decisions))
	}
	d := intent.Status.Decisions[0]
	if d.Action != "ScaleReplicas" {
		t.Errorf("expected ScaleReplicas action, got %s", d.Action)
	}
	if d.FromValue != "5" || d.ToValue != "6" {
		t.Errorf("expected 5→6, got %s→%s", d.FromValue, d.ToValue)
	}

	// Verify HPA was patched.
	var updatedHPA autoscalingv2.HorizontalPodAutoscaler
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "default", Name: "myapp-kubeintent-hpa"}, &updatedHPA); err != nil {
		t.Fatalf("failed to get HPA: %v", err)
	}
	if updatedHPA.Spec.MaxReplicas != 6 {
		t.Errorf("expected HPA maxReplicas=6, got %d", updatedHPA.Spec.MaxReplicas)
	}

	// Second call: should be rate-limited (NoOp).
	r.react(ctx, intent, effectivePolicy, effectiveAutoscaling)

	if len(intent.Status.Decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %d", len(intent.Status.Decisions))
	}
	d2 := intent.Status.Decisions[1]
	if d2.Action != "NoOp" {
		t.Errorf("expected NoOp action (rate limited), got %s", d2.Action)
	}
}
