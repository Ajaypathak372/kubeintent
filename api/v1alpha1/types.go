package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type TargetRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

type AutoscalingPolicy struct {
	// Enabled indicates whether autoscaling is active for the workload.
	// When true, the system dynamically adjusts replicas based on metrics.
	// Example: true.
	Enabled bool `json:"enabled,omitempty"`

	// MinReplicas is the minimum number of replicas the workload can scale down to.
	// This ensures baseline availability even under low traffic.
	// Must be >= 1 if autoscaling is enabled. Example: 2.
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of replicas the workload can scale up to.
	// This limits resource usage and cost during high load.
	// Must be >= MinReplicas. Example: 10.
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// CPUUtilizationTargetPct is the target average CPU utilization percentage per pod.
	// The autoscaler adjusts replicas to maintain this target.
	// Valid range: 1–100. Example: 70.
	CPUUtilizationTargetPct *int32 `json:"cpuUtilizationTargetPct,omitempty"`
}

type IntentPolicy struct {
	// Availability defines the desired availability level for the service.
	// It may map to internal SLO tiers such as "high", "medium", or "low".
	// Higher availability may increase cost due to redundancy.
	// Example: "high".
	Availability string `json:"availability,omitempty"`

	// LatencyTargetMs is the p99 latency budget for this service in milliseconds.
	// The operator considers the AppIntent in violation when observed p99 latency
	// exceeds this value. Lower values enforce stricter performance requirements.
	// Typical values: 50–500. Example: 100.
	LatencyTargetMs *int32 `json:"latencyTargetMs,omitempty"`

	// MaxMonthlyCostUSD is the maximum allowed monthly cost for running this service in USD.
	// If the estimated cost exceeds this value, the operator may take corrective actions
	// such as scaling down resources or adjusting configurations.
	// Typical values: 10–1000. Example: 200.
	MaxMonthlyCostUSD *float64 `json:"maxMonthlyCostUSD,omitempty"`

	// SecurityTier defines the desired security posture for the service.
	// It may correspond to predefined levels such as "standard", "hardened", or "restricted".
	// Higher tiers may enforce stricter policies and increase operational overhead.
	// Example: "restricted".
	SecurityTier string `json:"securityTier,omitempty"`

	// Autoscaling defines the autoscaling behavior and limits for the service.
	// When enabled, the system automatically adjusts replica counts based on load.
	// If not specified, autoscaling behavior defaults to system or runtime profile settings.
	Autoscaling *AutoscalingPolicy `json:"autoscaling,omitempty"`
}

type AppIntentSpec struct {
	TargetRef         TargetRef    `json:"targetRef"`
	RuntimeProfileRef string       `json:"runtimeProfileRef,omitempty"`
	Policy            IntentPolicy `json:"policy"`
}

// AppIntentStatus reflects the observed state of an AppIntent, including
// feedback-loop telemetry, recent controller decisions, and a compliance
// summary for the declared intent.
type AppIntentStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`

	// ObservedState is the most recent runtime observation for the target
	// workload, populated by the feedback-loop reconciler.
	ObservedState *ObservedState `json:"observedState,omitempty"`

	// Decisions is a bounded history of the most recent closed-loop
	// decisions the controller has taken. Capped at maxDecisions entries;
	// use AppendDecision to mutate.
	Decisions []Decision `json:"decisions,omitempty"`

	// Compliance summarises how the workload is meeting its declared
	// intent overall.
	Compliance *Compliance `json:"compliance,omitempty"`
}

// ObservedState holds the most recent observed runtime state for the target
// workload. All fields are optional; a nil pointer means "not observed".
type ObservedState struct {
	// LastObservedAt is the wall-clock time at which the telemetry adapter
	// produced this observation.
	LastObservedAt metav1.Time `json:"lastObservedAt,omitempty"`

	// Latency holds observed request-latency percentiles for the workload.
	Latency *Latency `json:"latency,omitempty"`

	// CurrentReplicas is the observed replica count on the target
	// Deployment. Pointer so that "0 replicas" is distinguishable from
	// "not observed".
	CurrentReplicas *int32 `json:"currentReplicas,omitempty"`

	// CurrentMonthlyCostUSD is the estimated run-rate cost of the
	// workload, extrapolated from the most recent observation window.
	CurrentMonthlyCostUSD *float64 `json:"currentMonthlyCostUSD,omitempty"`

	// ObservedRPS is the observed steady-state request rate.
	ObservedRPS *float64 `json:"observedRPS,omitempty"`
}

// Latency holds observed request-latency percentiles as Go duration
// strings (e.g. "42ms", "1.2s"). Parseable by time.ParseDuration.
type Latency struct {
	P99 string `json:"p99,omitempty"`
	P50 string `json:"p50,omitempty"`
}

// Decision records a single closed-loop action taken (or considered) by
// the controller. Decisions form an append-only audit trail on the status
// subresource, bounded to the most recent maxDecisions entries.
type Decision struct {
	// ID is a stable identifier, formatted as "dec-<RFC3339-timestamp>".
	ID string `json:"id,omitempty"`

	// At is the time the decision was made.
	At metav1.Time `json:"at,omitempty"`

	// Action is the kind of decision. One of: ScaleReplicas, NoOp, Blocked.
	Action string `json:"action,omitempty"`

	// Reason is a multi-line, human-readable explanation.
	Reason string `json:"reason,omitempty"`

	// FromValue is the prior value (e.g. "3") as a stringified scalar so
	// that a single Decision type can describe heterogeneous transitions.
	FromValue string `json:"fromValue,omitempty"`

	// ToValue is the new value (e.g. "5"), stringified for the same reason.
	ToValue string `json:"toValue,omitempty"`

	// Inputs captures the observed values that drove the decision, keyed
	// by a short name (e.g. "p99", "cpuUtil", "rps").
	Inputs map[string]string `json:"inputs,omitempty"`
}

// Compliance summarises how the workload is meeting its declared intent.
type Compliance struct {
	// Overall is one of: Meeting, AtRisk, Violating, Unknown.
	Overall string `json:"overall,omitempty"`
}

// maxDecisions is the cap on the number of entries retained in
// AppIntentStatus.Decisions. Older entries are dropped on append.
const maxDecisions = 50

// AppendDecision appends d to s.Decisions and trims the slice to the most
// recent maxDecisions entries (oldest-first eviction).
func (s *AppIntentStatus) AppendDecision(d Decision) {
	s.Decisions = append(s.Decisions, d)
	if len(s.Decisions) > maxDecisions {
		s.Decisions = s.Decisions[len(s.Decisions)-maxDecisions:]
	}
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type AppIntent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AppIntentSpec   `json:"spec,omitempty"`
	Status AppIntentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AppIntentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AppIntent `json:"items"`
}

type RuntimeDefaults struct {
	Availability string             `json:"availability,omitempty"`
	SecurityTier string             `json:"securityTier,omitempty"`
	Autoscaling  *AutoscalingPolicy `json:"autoscaling,omitempty"`
}

type RuntimeProfileSpec struct {
	Defaults RuntimeDefaults `json:"defaults,omitempty"`
}

// +kubebuilder:object:root=true
type RuntimeProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RuntimeProfileSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type RuntimeProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RuntimeProfile `json:"items"`
}

type NamespaceIntentSpec struct {
	Priority *int32       `json:"priority,omitempty"`
	Policy   IntentPolicy `json:"policy,omitempty"`
}

// +kubebuilder:object:root=true
type NamespaceIntent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              NamespaceIntentSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type NamespaceIntentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NamespaceIntent `json:"items"`
}

type DriftExceptionSpec struct {
	AppIntentRef string   `json:"appIntentRef"`
	ExpiresAt    string   `json:"expiresAt"`
	Fields       []string `json:"fields"`
	Reason       string   `json:"reason"`
}

// +kubebuilder:object:root=true
type DriftException struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DriftExceptionSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type DriftExceptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DriftException `json:"items"`
}

func (in *AppIntent) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(AppIntent)
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
	if in.Status.ObservedState != nil {
		obs := *in.Status.ObservedState
		if in.Status.ObservedState.Latency != nil {
			lat := *in.Status.ObservedState.Latency
			obs.Latency = &lat
		}
		if in.Status.ObservedState.CurrentReplicas != nil {
			v := *in.Status.ObservedState.CurrentReplicas
			obs.CurrentReplicas = &v
		}
		if in.Status.ObservedState.CurrentMonthlyCostUSD != nil {
			v := *in.Status.ObservedState.CurrentMonthlyCostUSD
			obs.CurrentMonthlyCostUSD = &v
		}
		if in.Status.ObservedState.ObservedRPS != nil {
			v := *in.Status.ObservedState.ObservedRPS
			obs.ObservedRPS = &v
		}
		out.Status.ObservedState = &obs
	}
	if in.Status.Decisions != nil {
		out.Status.Decisions = make([]Decision, len(in.Status.Decisions))
		for i := range in.Status.Decisions {
			out.Status.Decisions[i] = in.Status.Decisions[i]
			if in.Status.Decisions[i].Inputs != nil {
				m := make(map[string]string, len(in.Status.Decisions[i].Inputs))
				for k, v := range in.Status.Decisions[i].Inputs {
					m[k] = v
				}
				out.Status.Decisions[i].Inputs = m
			}
		}
	}
	if in.Status.Compliance != nil {
		c := *in.Status.Compliance
		out.Status.Compliance = &c
	}
	return out
}

func (in *AppIntentList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(AppIntentList)
	*out = *in
	if in.Items != nil {
		out.Items = make([]AppIntent, len(in.Items))
		copy(out.Items, in.Items)
	}
	return out
}

func (in *RuntimeProfile) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(RuntimeProfile)
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	return out
}

func (in *RuntimeProfileList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(RuntimeProfileList)
	*out = *in
	if in.Items != nil {
		out.Items = make([]RuntimeProfile, len(in.Items))
		copy(out.Items, in.Items)
	}
	return out
}

func (in *NamespaceIntent) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(NamespaceIntent)
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	return out
}

func (in *NamespaceIntentList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(NamespaceIntentList)
	*out = *in
	if in.Items != nil {
		out.Items = make([]NamespaceIntent, len(in.Items))
		copy(out.Items, in.Items)
	}
	return out
}

func (in *DriftException) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(DriftException)
	*out = *in
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()
	return out
}

func (in *DriftExceptionList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(DriftExceptionList)
	*out = *in
	if in.Items != nil {
		out.Items = make([]DriftException, len(in.Items))
		copy(out.Items, in.Items)
	}
	return out
}
