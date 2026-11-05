package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var (
	clusterOperators       = schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusteroperators"}
	apiRequestCounts       = schema.GroupVersionResource{Group: "apiserver.openshift.io", Version: "v1", Resource: "apirequestcounts"}
	collectionSuccess      = metric("collection_success", "Whether this resource type was collected successfully (1 or 0).", "resource")
	nodeReady              = metric("node_ready", "Whether the node Ready condition is True (1 or 0). Missing or unknown is 0.", "node")
	pdbAllowed             = metric("pdb_disruptions_allowed", "Number of disruptions currently allowed by the PDB.", "namespace", "pdb")
	pdbBlocked             = metric("pdb_blocked", "Whether the PDB currently allows zero disruptions (1 or 0).", "namespace", "pdb")
	operatorCondition      = metric("clusteroperator_condition", "Operator condition by status; exactly one status is 1. Missing conditions are Unknown.", "operator", "condition", "status")
	apiInfo                = metric("apirequestcount_info", "API request count resource and reported removal release; empty release means none reported.", "name", "removed_in_release")
	apiRequests            = metric("api_requests_window", "Requests in the APIRequestCount status reporting window; not a cumulative counter.", "name")
	apiRequestsCurrentHour = metric("api_requests_current_hour", "Requests in the current hour; resets on the hour.", "name")
)

func metric(name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc("upgrade_checker_"+name, help, labels, nil)
}

func gauge(ch chan<- prometheus.Metric, desc *prometheus.Desc, value float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

// Each scrape uses fresh data. A failed resource does not hide the other checks.
type readinessCollector struct {
	kube    kubernetes.Interface
	dynamic dynamic.Interface
	timeout time.Duration
}

func (c *readinessCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{collectionSuccess, nodeReady, pdbAllowed, pdbBlocked, operatorCondition, apiInfo, apiRequests, apiRequestsCurrentHour} {
		ch <- desc
	}
}

func (c *readinessCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	checks := []struct {
		resource string
		collect  func(context.Context, chan<- prometheus.Metric) error
	}{
		{"nodes", c.collectNodes},
		{"poddisruptionbudgets", c.collectPDBs},
		{"clusteroperators", c.collectOperators},
		{"apirequestcounts", c.collectAPIRequests},
	}
	// Run the four independent reads concurrently under one timeout.
	var wg sync.WaitGroup
	for _, check := range checks {
		wg.Go(func() {
			err := check.collect(ctx, ch)
			gauge(ch, collectionSuccess, boolValue(err == nil), check.resource)
			if err != nil {
				log.Printf("collect %s: %v", check.resource, err)
			}
		})
	}
	wg.Wait()
}

func (c *readinessCollector) collectNodes(ctx context.Context, ch chan<- prometheus.Metric) error {
	nodes, err := c.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, node := range nodes.Items {
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				ready = condition.Status == corev1.ConditionTrue
			}
		}
		gauge(ch, nodeReady, boolValue(ready), node.Name)
	}
	return nil
}

func (c *readinessCollector) collectPDBs(ctx context.Context, ch chan<- prometheus.Metric) error {
	pdbs, err := c.kube.PolicyV1().PodDisruptionBudgets(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, pdb := range pdbs.Items {
		gauge(ch, pdbAllowed, float64(pdb.Status.DisruptionsAllowed), pdb.Namespace, pdb.Name)
		gauge(ch, pdbBlocked, boolValue(pdb.Status.DisruptionsAllowed == 0), pdb.Namespace, pdb.Name)
	}
	return nil
}

func (c *readinessCollector) collectOperators(ctx context.Context, ch chan<- prometheus.Metric) error {
	operators, err := c.dynamic.Resource(clusterOperators).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, operator := range operators.Items {
		conditions, _, err := unstructured.NestedSlice(operator.Object, "status", "conditions")
		if err != nil {
			return fmt.Errorf("%s conditions: %w", operator.GetName(), err)
		}
		values := map[string]string{"Available": "Unknown", "Progressing": "Unknown", "Degraded": "Unknown", "Upgradeable": "Unknown"}
		for _, entry := range conditions {
			condition, ok := entry.(map[string]interface{})
			if !ok {
				return fmt.Errorf("%s: invalid condition", operator.GetName())
			}
			kind, _, err := unstructured.NestedString(condition, "type")
			if err != nil || kind == "" {
				return fmt.Errorf("%s: invalid condition type", operator.GetName())
			}
			status, _, err := unstructured.NestedString(condition, "status")
			if err != nil || (status != "True" && status != "False" && status != "Unknown") {
				return fmt.Errorf("%s: invalid status for %s", operator.GetName(), kind)
			}
			values[kind] = status
		}
		for kind, value := range values {
			for _, status := range []string{"True", "False", "Unknown"} {
				gauge(ch, operatorCondition, boolValue(value == status), operator.GetName(), kind, status)
			}
		}
	}
	return nil
}

func (c *readinessCollector) collectAPIRequests(ctx context.Context, ch chan<- prometheus.Metric) error {
	counts, err := c.dynamic.Resource(apiRequestCounts).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, count := range counts.Items {
		release, _, err := unstructured.NestedString(count.Object, "status", "removedInRelease")
		if err != nil {
			return fmt.Errorf("%s removal release: %w", count.GetName(), err)
		}
		gauge(ch, apiInfo, 1, count.GetName(), release)
		for _, field := range []struct {
			desc *prometheus.Desc
			path []string
		}{
			{apiRequests, []string{"status", "requestCount"}},
			{apiRequestsCurrentHour, []string{"status", "currentHour", "requestCount"}},
		} {
			value, found, err := unstructured.NestedInt64(count.Object, field.path...)
			if err != nil {
				return fmt.Errorf("%s request count: %w", count.GetName(), err)
			}
			// An absent count is unknown, not zero.
			if found {
				gauge(ch, field.desc, float64(value), count.GetName())
			}
		}
	}
	return nil
}
