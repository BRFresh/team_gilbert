package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

func testCollector() *readinessCollector {
	kube := kubefake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "crc"}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}},
		&policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: "blocked", Namespace: "test"}},
		&policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: "allowed", Namespace: "other"}, Status: policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: 2}},
	)
	operator := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
		"metadata": map[string]interface{}{"name": "console"},
		"status": map[string]interface{}{"conditions": []interface{}{
			map[string]interface{}{"type": "Available", "status": "True"},
			map[string]interface{}{"type": "Degraded", "status": "False"},
		}},
	}}
	count := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apiserver.openshift.io/v1", "kind": "APIRequestCount",
		"metadata": map[string]interface{}{"name": "widgets.v1.example.com"},
		"status":   map[string]interface{}{"removedInRelease": "4.20", "requestCount": int64(12), "currentHour": map[string]interface{}{"requestCount": int64(3)}},
	}}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		clusterOperators: "ClusterOperatorList", apiRequestCounts: "APIRequestCountList",
	}, operator, count)
	return &readinessCollector{kube: kube, dynamic: dynamicClient, timeout: time.Second}
}

func scrape(t *testing.T, collector *readinessCollector) string {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("scrape: %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/plain") {
		t.Fatal("missing Prometheus content type")
	}
	return recorder.Body.String()
}

func expect(t *testing.T, body string, samples ...string) {
	t.Helper()
	for _, sample := range samples {
		if !strings.Contains(body, sample+"\n") {
			t.Errorf("missing sample %s in:\n%s", sample, body)
		}
	}
}

func TestMetricsAndFreshData(t *testing.T) {
	c := testCollector()
	body := scrape(t, c)
	expect(t, body,
		`upgrade_checker_node_ready{node="crc"} 1`,
		`upgrade_checker_pdb_blocked{namespace="test",pdb="blocked"} 1`,
		`upgrade_checker_pdb_disruptions_allowed{namespace="other",pdb="allowed"} 2`,
		`upgrade_checker_clusteroperator_condition{condition="Available",operator="console",status="True"} 1`,
		`upgrade_checker_clusteroperator_condition{condition="Degraded",operator="console",status="True"} 0`,
		`upgrade_checker_clusteroperator_condition{condition="Upgradeable",operator="console",status="Unknown"} 1`,
		`upgrade_checker_apirequestcount_info{name="widgets.v1.example.com",removed_in_release="4.20"} 1`,
		`upgrade_checker_api_requests_window{name="widgets.v1.example.com"} 12`,
		`upgrade_checker_api_requests_current_hour{name="widgets.v1.example.com"} 3`,
	)
	for _, resource := range []string{"nodes", "poddisruptionbudgets", "clusteroperators", "apirequestcounts"} {
		expect(t, body, fmt.Sprintf("upgrade_checker_collection_success{resource=%q} 1", resource))
	}
	if err := c.kube.CoreV1().Nodes().Delete(context.Background(), "crc", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(scrape(t, c), `node="crc"`) {
		t.Fatal("deleted node retained in metrics")
	}
}

func TestCollectionFailureAndRecovery(t *testing.T) {
	c := testCollector()
	fail := true
	c.dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "clusteroperators", func(action ktesting.Action) (bool, runtime.Object, error) {
		if fail {
			return true, nil, apierrors.NewForbidden(clusterOperators.GroupResource(), "", fmt.Errorf("denied"))
		}
		return false, nil, nil
	})
	body := scrape(t, c)
	expect(t, body, `upgrade_checker_collection_success{resource="clusteroperators"} 0`, `upgrade_checker_node_ready{node="crc"} 1`)
	if strings.Contains(body, "upgrade_checker_clusteroperator_condition{") {
		t.Fatal("failed collection published operator data")
	}
	fail = false
	expect(t, scrape(t, c), `upgrade_checker_collection_success{resource="clusteroperators"} 1`)
}

func TestMissingAndInvalidCounts(t *testing.T) {
	c := testCollector()
	resource := c.dynamic.Resource(apiRequestCounts)
	count, err := resource.Get(context.Background(), "widgets.v1.example.com", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(count.Object, "status")
	if _, err := resource.Update(context.Background(), count, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	body := scrape(t, c)
	expect(t, body, `upgrade_checker_collection_success{resource="apirequestcounts"} 1`)
	if strings.Contains(body, "upgrade_checker_api_requests_window{") {
		t.Fatal("missing count was reported as zero")
	}
	count.Object["status"] = map[string]interface{}{"requestCount": "invalid"}
	if _, err := resource.Update(context.Background(), count, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	expect(t, scrape(t, c), `upgrade_checker_collection_success{resource="apirequestcounts"} 0`)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAPITimeout(t *testing.T) {
	config := &rest.Config{Host: "https://example.invalid", Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	kube, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	body := scrape(t, &readinessCollector{kube: kube, dynamic: client, timeout: 20 * time.Millisecond})
	if time.Since(start) > time.Second {
		t.Fatal("scrape did not respect timeout")
	}
	for _, resource := range []string{"nodes", "poddisruptionbudgets", "clusteroperators", "apirequestcounts"} {
		expect(t, body, fmt.Sprintf("upgrade_checker_collection_success{resource=%q} 0", resource))
	}
}
