package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupMonitorSpec defines the desired state of BackupMonitor
type BackupMonitorSpec struct {
	// APIEndpoint is the URL of the external API to send notifications to
	// +optional
	APIEndpoint string `json:"apiEndpoint,omitempty"`

	// ResyncPeriodMinutes defines how often to check for new deployments (default: 5)
	// +optional
	// +kubebuilder:default:=5
	ResyncPeriodMinutes int `json:"resyncPeriodMinutes,omitempty"`

	// EnabledNamespaces limits processing to specific namespaces (empty means all namespaces)
	// +optional
	EnabledNamespaces []string `json:"enabledNamespaces,omitempty"`
}

// ProcessedDeployment tracks a deployment that has been processed
type ProcessedDeployment struct {
	// Name of the deployment
	Name string `json:"name"`

	// Namespace of the deployment
	Namespace string `json:"namespace"`

	// Timestamp when the deployment was processed
	Timestamp string `json:"timestamp"`
}

// BackupMonitorStatus defines the observed state of BackupMonitor
type BackupMonitorStatus struct {
	// ProcessedDeployments is a list of deployments that have been processed
	ProcessedDeployments []ProcessedDeployment `json:"processedDeployments,omitempty"`

	// LastResyncTime is the last time the controller checked for deployments
	LastResyncTime metav1.Time `json:"lastResyncTime,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// BackupMonitor is the Schema for the backupmonitors API
type BackupMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupMonitorSpec   `json:"spec,omitempty"`
	Status BackupMonitorStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BackupMonitorList contains a list of BackupMonitor
type BackupMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupMonitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BackupMonitor{}, &BackupMonitorList{})
}