package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func clusterVersionFixture() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "config.openshift.io/v1", "kind": "ClusterVersion",
		"metadata": map[string]interface{}{"name": "version"},
		"spec": map[string]interface{}{"channel": "stable-4.22", "overrides": []interface{}{
			map[string]interface{}{"group": "apps", "kind": "Deployment", "namespace": "platform", "name": "example", "unmanaged": true},
		}},
		"status": map[string]interface{}{
			"desired": map[string]interface{}{"version": "4.22.15"},
			"history": []interface{}{
				map[string]interface{}{"version": "4.22.15", "state": "Partial"},
				map[string]interface{}{"version": "4.22.14", "state": "Completed"},
			},
			"availableUpdates": []interface{}{map[string]interface{}{"version": "4.22.20"}},
			"conditions":       []interface{}{map[string]interface{}{"type": "Upgradeable", "status": "False", "reason": "ClusterVersionOverridesSet"}},
		},
	}}
}

func TestClusterVersionEvidence(t *testing.T) {
	expect(t, scrape(t, testCollector()),
		`upgrade_checker_clusterversion_info{channel="stable-4.22",desired_version="4.22.15",last_completed_version="4.22.14"} 1`,
		`upgrade_checker_clusterversion_upgradeable 0`,
		`upgrade_checker_clusterversion_condition_info{condition="Upgradeable",reason="ClusterVersionOverridesSet",status="False"} 1`,
		`upgrade_checker_clusterversion_condition{condition="Failing",status="Unknown"} 1`,
		`upgrade_checker_clusterversion_unmanaged_overrides 1`,
		`upgrade_checker_clusterversion_unmanaged_override{group="apps",kind="Deployment",name="example",namespace="platform"} 1`,
		`upgrade_checker_clusterversion_available_update_info{version="4.22.20"} 1`,
	)
}

func TestClusterVersionUnknownAndRecovery(t *testing.T) {
	c := testCollector()
	resource := c.dynamic.Resource(clusterVersions)
	version, err := resource.Get(context.Background(), "version", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(version.Object, "status")
	delete(version.Object, "spec")
	if _, err := resource.Update(context.Background(), version, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	expect(t, scrape(t, c), `upgrade_checker_clusterversion_upgradeable -1`, `upgrade_checker_clusterversion_unmanaged_overrides 0`)
	version.Object["status"] = map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Upgradeable", "status": "True"}}}
	if _, err := resource.Update(context.Background(), version, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	expect(t, scrape(t, c), `upgrade_checker_clusterversion_upgradeable 1`)
}

func TestClusterVersionFailureDoesNotReportGo(t *testing.T) {
	for _, failure := range []string{"forbidden", "invalid_status"} {
		t.Run(failure, func(t *testing.T) {
			c := testCollector()
			if failure == "forbidden" {
				c.dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "clusterversions", func(ktesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewForbidden(clusterVersions.GroupResource(), "version", fmt.Errorf("denied"))
				})
			} else {
				version := clusterVersionFixture()
				version.Object["status"] = map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Upgradeable", "status": "invalid"}}}
				if _, err := c.dynamic.Resource(clusterVersions).Update(context.Background(), version, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			body := scrape(t, c)
			expect(t, body, `upgrade_checker_collection_success{resource="clusterversion"} 0`, `upgrade_checker_node_ready{node="crc"} 1`)
			if strings.Contains(body, "upgrade_checker_clusterversion_upgradeable ") {
				t.Fatal("failed read emitted an upgradeability decision")
			}
		})
	}
}
