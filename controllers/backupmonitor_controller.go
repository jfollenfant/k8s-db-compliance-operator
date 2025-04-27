package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
//	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	backupv1alpha1 "github.com/jfollenfant/k8s-db-compliance-operator/api/v1alpha1"
)

const (
	// Define the external API endpoint
	externalAPIEndpoint = "https://your-external-api.example.com/notify"
)

// BackupPayload represents the data structure we'll send to the external API
type BackupPayload struct {
	DeploymentName string   `json:"deploymentName"`
	Namespace      string   `json:"namespace"`
	ServiceFQDNs   []string `json:"serviceFQDNs"`
	PodIPs         []string `json:"podIPs"`
	DatabaseType   string   `json:"databaseType"`
	DatabasePort   int32    `json:"databasePort"`
	PVCNames       []string `json:"pvcNames"`
}

// BackupMonitorReconciler reconciles a BackupMonitor object
type BackupMonitorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=backup.example.com,resources=backupmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=backup.example.com,resources=backupmonitors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=backup.example.com,resources=backupmonitors/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch

func (r *BackupMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling BackupMonitor")

	// Fetch our BackupMonitor CR
	var backupMonitor backupv1alpha1.BackupMonitor
	if err := r.Get(ctx, req.NamespacedName, &backupMonitor); err != nil {
		// Handle error or not found case
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// List deployments with the 'backup=yes' label
	deploymentList := &appsv1.DeploymentList{}
	if err := r.List(ctx, deploymentList, client.MatchingLabels{"backup": "yes"}); err != nil {
		logger.Error(err, "Failed to list deployments")
		return ctrl.Result{}, err
	}

	for _, deployment := range deploymentList.Items {
		// Check if we've already processed this deployment
		if hasProcessed(backupMonitor.Status.ProcessedDeployments, deployment.Name, deployment.Namespace) {
			continue
		}

		// Check if the deployment has associated PVCs
		pvcNames, err := r.findAssociatedPVCs(ctx, &deployment)
		if err != nil {
			logger.Error(err, "Failed to find PVCs", "deployment", deployment.Name)
			continue
		}

		// Skip if no PVCs found
		if len(pvcNames) == 0 {
			logger.Info("No PVCs found for deployment", "deployment", deployment.Name)
			continue
		}

		// Get database info from deployment annotations or labels
		dbType := getAnnotationOrLabel(deployment, "db-type", "unknown")
		dbPortStr := getAnnotationOrLabel(deployment, "db-port", "5432") // Default to Postgres port
		dbPort := parsePort(dbPortStr)

		// Get pod IPs for this deployment
		podIPs, err := r.getPodIPs(ctx, &deployment)
		if err != nil {
			logger.Error(err, "Failed to get pod IPs", "deployment", deployment.Name)
			continue
		}

		// Get service FQDNs for this deployment
		serviceFQDNs, err := r.getServiceFQDNs(ctx, &deployment)
		if err != nil {
			logger.Error(err, "Failed to get service FQDNs", "deployment", deployment.Name)
			continue
		}

		// Prepare the payload for the external API
		payload := BackupPayload{
			DeploymentName: deployment.Name,
			Namespace:      deployment.Namespace,
			ServiceFQDNs:   serviceFQDNs,
			PodIPs:         podIPs,
			DatabaseType:   dbType,
			DatabasePort:   dbPort,
			PVCNames:       pvcNames,
		}

		// Send the notification to external API
		if err := r.notifyExternalAPI(payload); err != nil {
			logger.Error(err, "Failed to notify external API", "deployment", deployment.Name)
			continue
		}

		// Update our BackupMonitor status to track that we've processed this deployment
		backupMonitor.Status.ProcessedDeployments = append(backupMonitor.Status.ProcessedDeployments, 
			backupv1alpha1.ProcessedDeployment{
				Name:      deployment.Name,
				Namespace: deployment.Namespace,
				Timestamp: time.Now().Format(time.RFC3339),
			})
		
		if err := r.Status().Update(ctx, &backupMonitor); err != nil {
			logger.Error(err, "Failed to update BackupMonitor status")
			return ctrl.Result{}, err
		}

		logger.Info("Successfully processed deployment", "deployment", deployment.Name)
	}

	// Requeue to check periodically
	return ctrl.Result{RequeueAfter: time.Duration(5) * time.Minute}, nil
}

// findAssociatedPVCs finds PVCs associated with a deployment
func (r *BackupMonitorReconciler) findAssociatedPVCs(ctx context.Context, deployment *appsv1.Deployment) ([]string, error) {
	var pvcNames []string

	// List pods controlled by this deployment
	podList := &corev1.PodList{}
	labels := deployment.Spec.Selector.MatchLabels
	
	if err := r.List(ctx, podList, client.InNamespace(deployment.Namespace), client.MatchingLabels(labels)); err != nil {
		return nil, err
	}

	// Check volumes in pod specs
	for _, pod := range podList.Items {
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil {
				pvcNames = append(pvcNames, volume.PersistentVolumeClaim.ClaimName)
			}
		}
	}

	return pvcNames, nil
}

// getPodIPs gets the IPs of pods belonging to a deployment
func (r *BackupMonitorReconciler) getPodIPs(ctx context.Context, deployment *appsv1.Deployment) ([]string, error) {
	var podIPs []string

	// List pods controlled by this deployment
	podList := &corev1.PodList{}
	labels := deployment.Spec.Selector.MatchLabels
	
	if err := r.List(ctx, podList, client.InNamespace(deployment.Namespace), client.MatchingLabels(labels)); err != nil {
		return nil, err
	}

	// Extract pod IPs
	for _, pod := range podList.Items {
		if pod.Status.PodIP != "" {
			podIPs = append(podIPs, pod.Status.PodIP)
		}
	}

	return podIPs, nil
}

// getServiceFQDNs finds services associated with deployment and returns their FQDNs
func (r *BackupMonitorReconciler) getServiceFQDNs(ctx context.Context, deployment *appsv1.Deployment) ([]string, error) {
	var serviceFQDNs []string

	// List services in the same namespace
	serviceList := &corev1.ServiceList{}
	if err := r.List(ctx, serviceList, client.InNamespace(deployment.Namespace)); err != nil {
		return nil, err
	}

	// Check if service selector matches deployment labels
	for _, service := range serviceList.Items {
		if selectorMatchesLabels(service.Spec.Selector, deployment.Spec.Template.Labels) {
			// Format: service-name.namespace.svc.cluster.local
			fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, service.Namespace)
			serviceFQDNs = append(serviceFQDNs, fqdn)
		}
	}

	return serviceFQDNs, nil
}

// notifyExternalAPI sends the payload to the external API
func (r *BackupMonitorReconciler) notifyExternalAPI(payload BackupPayload) error {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// Send HTTP POST request
	resp, err := http.Post(externalAPIEndpoint, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check if request was successful
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("external API returned non-success status: %d", resp.StatusCode)
	}

	return nil
}

// Helper functions
func selectorMatchesLabels(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return len(selector) > 0
}

func hasProcessed(processed []backupv1alpha1.ProcessedDeployment, name, namespace string) bool {
	for _, p := range processed {
		if p.Name == name && p.Namespace == namespace {
			return true
		}
	}
	return false
}

func getAnnotationOrLabel(deployment appsv1.Deployment, key, defaultValue string) string {
	// Check annotations first
	if value, ok := deployment.Annotations[key]; ok && value != "" {
		return value
	}
	
	// Then check labels
	if value, ok := deployment.Labels[key]; ok && value != "" {
		return value
	}
	
	return defaultValue
}

func parsePort(portStr string) int32 {
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return 5432 // Default to postgres port
	}
	return int32(port)
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.BackupMonitor{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}