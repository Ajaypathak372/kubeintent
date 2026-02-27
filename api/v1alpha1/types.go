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
	Enabled                 bool   `json:"enabled,omitempty"`
	MinReplicas             *int32 `json:"minReplicas,omitempty"`
	MaxReplicas             *int32 `json:"maxReplicas,omitempty"`
	CPUUtilizationTargetPct *int32 `json:"cpuUtilizationTargetPct,omitempty"`
}

type IntentPolicy struct {
	Availability      string             `json:"availability,omitempty"`
	LatencyTargetMs   *int32             `json:"latencyTargetMs,omitempty"`
	MaxMonthlyCostUSD *float64           `json:"maxMonthlyCostUSD,omitempty"`
	SecurityTier      string             `json:"securityTier,omitempty"`
	Autoscaling       *AutoscalingPolicy `json:"autoscaling,omitempty"`
}

type AppIntentSpec struct {
	TargetRef         TargetRef    `json:"targetRef"`
	RuntimeProfileRef string       `json:"runtimeProfileRef,omitempty"`
	Policy            IntentPolicy `json:"policy"`
}

type AppIntentStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
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
