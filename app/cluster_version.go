package main

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	clusterVersions           = schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusterversions"}
	clusterVersionInfo        = metric("clusterversion_info", "Desired and last completed OpenShift versions and update channel.", "desired_version", "last_completed_version", "channel")
	clusterVersionCondition   = metric("clusterversion_condition", "ClusterVersion conditions by status; missing standard conditions are Unknown.", "condition", "status")
	clusterVersionReason      = metric("clusterversion_condition_info", "Reported ClusterVersion condition status and reason.", "condition", "status", "reason")
	clusterUpgradeable        = metric("clusterversion_upgradeable", "ClusterVersion Upgradeable condition: 1=True, 0=False, -1=Unknown or missing. Not a complete upgrade readiness assessment.")
	clusterAvailableUpdate    = metric("clusterversion_available_update_info", "Update version listed by ClusterVersion in availableUpdates; check RetrievedUpdates before use.", "version")
	clusterUnmanagedOverride  = metric("clusterversion_unmanaged_override", "Whether a release resource is configured as unmanaged (1 or 0). Not a general unsupported-resource scan.", "group", "kind", "namespace", "name")
	clusterUnmanagedOverrides = metric("clusterversion_unmanaged_overrides", "Number of release resources configured as unmanaged.")
)

// Only the fields needed for metrics; no OpenShift client library is required.
type clusterVersionFields struct {
	Spec struct {
		Channel   string `json:"channel"`
		Overrides []struct {
			Group     string `json:"group"`
			Kind      string `json:"kind"`
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			Unmanaged bool   `json:"unmanaged"`
		} `json:"overrides"`
	} `json:"spec"`
	Status struct {
		Desired struct {
			Version string `json:"version"`
		} `json:"desired"`
		History []struct {
			Version string `json:"version"`
			State   string `json:"state"`
		} `json:"history"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"conditions"`
		AvailableUpdates []struct {
			Version string `json:"version"`
		} `json:"availableUpdates"`
	} `json:"status"`
}

func (c *readinessCollector) collectClusterVersion(ctx context.Context, ch chan<- prometheus.Metric) error {
	object, err := c.dynamic.Resource(clusterVersions).Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return err
	}
	var version clusterVersionFields
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &version); err != nil {
		return fmt.Errorf("decode ClusterVersion: %w", err)
	}

	// Validate before emitting data so malformed conditions cannot look healthy.
	for _, condition := range version.Status.Conditions {
		if condition.Type == "" || (condition.Status != "True" && condition.Status != "False" && condition.Status != "Unknown") {
			return fmt.Errorf("invalid ClusterVersion condition %q", condition.Type)
		}
	}
	lastCompleted := ""
	// OpenShift returns history newest first. Desired can be an in-progress update.
	for _, entry := range version.Status.History {
		if entry.State == "Completed" {
			lastCompleted = entry.Version
			break
		}
	}
	gauge(ch, clusterVersionInfo, 1, version.Status.Desired.Version, lastCompleted, version.Spec.Channel)
	conditions := map[string]string{"Available": "Unknown", "Progressing": "Unknown", "Failing": "Unknown", "Upgradeable": "Unknown", "RetrievedUpdates": "Unknown"}
	for _, condition := range version.Status.Conditions {
		conditions[condition.Type] = condition.Status
		gauge(ch, clusterVersionReason, 1, condition.Type, condition.Status, condition.Reason)
	}
	for kind, value := range conditions {
		for _, status := range []string{"True", "False", "Unknown"} {
			gauge(ch, clusterVersionCondition, boolValue(value == status), kind, status)
		}
	}
	upgradeable := -1.0
	if conditions["Upgradeable"] == "True" {
		upgradeable = 1
	}
	if conditions["Upgradeable"] == "False" {
		upgradeable = 0
	}
	gauge(ch, clusterUpgradeable, upgradeable)
	for _, update := range version.Status.AvailableUpdates {
		if update.Version != "" {
			gauge(ch, clusterAvailableUpdate, 1, update.Version)
		}
	}
	unmanaged := 0.0
	for _, override := range version.Spec.Overrides {
		gauge(ch, clusterUnmanagedOverride, boolValue(override.Unmanaged), override.Group, override.Kind, override.Namespace, override.Name)
		unmanaged += boolValue(override.Unmanaged)
	}
	gauge(ch, clusterUnmanagedOverrides, unmanaged)
	return nil
}
